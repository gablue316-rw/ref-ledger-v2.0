package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/mobileauth"
	"ref-ledger-v2/internal/model"
)

const auth0RegistrationLock = "auth0-registration"

var errAuth0EmailUnverified = errors.New("Verify your email in Auth0, then sign in again.")
var errAuth0EmailInUse = errors.New("A Ref Ledger account already uses this email. Contact Ref Ledger support to link your existing account.")
var errAuth0AccountLimit = errors.New("Ref Ledger has reached its account limit.")
var errAuth0AccountDenied = errors.New("This Ref Ledger account cannot sign in.")
var errAuth0Profile = errors.New("Could not verify your Auth0 profile. Sign out and sign in again.")

type auth0Profile struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

type auth0Registration struct {
	maxAccounts int64
}

func configureAuth0Registration() (*auth0Registration, error) {
	switch os.Getenv("AUTH0_SELF_SERVICE_SIGNUP") {
	case "", "false":
		return nil, nil
	case "true":
	default:
		return nil, errors.New("AUTH0_SELF_SERVICE_SIGNUP must be true or false")
	}
	if os.Getenv("WEB_AUTH_MODE") != "auth0" {
		return nil, errors.New("Auth0 signup requires WEB_AUTH_MODE=auth0")
	}

	value := strings.TrimSpace(os.Getenv("MAX_USER_ACCOUNTS"))
	if value == "" {
		value = "10"
	}
	maxAccounts, err := strconv.ParseInt(value, 10, 64)
	if err != nil || maxAccounts < 0 {
		return nil, errors.New("MAX_USER_ACCOUNTS must be a nonnegative integer")
	}
	return &auth0Registration{maxAccounts: maxAccounts}, nil
}

func auth0Users() *mongo.Collection {
	return database.Client.Database(database.GetMongoDbName()).Collection("users")
}

func auth0SignupLocks() *mongo.Collection {
	return database.Client.Database(database.GetMongoDbName()).Collection("accountRegistration")
}

func (registration *auth0Registration) initialize(ctx context.Context) error {
	if registration.maxAccounts == 0 {
		return nil
	}
	_, err := auth0SignupLocks().UpdateOne(ctx, bson.M{"_id": auth0RegistrationLock},
		bson.M{"$setOnInsert": bson.M{"revision": int64(0)}}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}

func auth0IdentityFilter(identity mobileauth.Identity) bson.M {
	return bson.M{"mobileIdentity.issuer": identity.Issuer, "mobileIdentity.subject": identity.Subject}
}

// Preserve compatibility for migrated accounts without an emailVerified field.
func auth0AccountAllowed(user model.User) bool {
	return user.Username != "" && user.TenantID != "" && user.TenantID != "na" &&
		(user.Role == "user" || user.Role == "readonly" || user.Role == "admin") &&
		(user.EmailVerified == nil || *user.EmailVerified)
}

func findAuth0Account(ctx context.Context, identity mobileauth.Identity) (model.User, error) {
	var user model.User
	err := auth0Users().FindOne(ctx, auth0IdentityFilter(identity)).Decode(&user)
	if err != nil {
		return model.User{}, err
	}
	if !auth0AccountAllowed(user) {
		return model.User{}, errAuth0AccountDenied
	}
	return user, nil
}

func (registration *auth0Registration) account(ctx context.Context, identity mobileauth.Identity, profile auth0Profile) (model.User, error) {
	if !profile.EmailVerified {
		return model.User{}, errAuth0EmailUnverified
	}
	username := strings.ToLower(strings.TrimSpace(profile.Email))
	address, err := mail.ParseAddress(username)
	if err != nil || address.Address != username || len(username) > 254 || profile.Subject != identity.Subject {
		return model.User{}, errAuth0Profile
	}

	name := strings.TrimSpace(profile.Name)
	if name == "" || len(name) > 254 {
		name = username
	}
	verified := true
	now := time.Now().UTC()
	user := model.User{
		Username: username, Name: name, TenantID: primitive.NewObjectID().Hex(),
		Role: "user", CreatedAt: now, EmailVerified: &verified, EmailVerifiedAt: &now,
	}
	document := struct {
		model.User     `bson:",inline"`
		MobileIdentity bson.M `bson:"mobileIdentity"`
	}{
		User:           user,
		MobileIdentity: bson.M{"issuer": identity.Issuer, "subject": identity.Subject},
	}

	create := func(ctx context.Context) (model.User, error) {
		linked, err := findAuth0Account(ctx, identity)
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return linked, err
		}
		// A verified email can identify a conflict, but cannot grant access to an existing ledger.
		err = auth0Users().FindOne(ctx, bson.M{"username": username}).Err()
		if err == nil {
			return model.User{}, errAuth0EmailInUse
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return model.User{}, err
		}
		if registration.maxAccounts > 0 {
			count, err := auth0Users().CountDocuments(ctx, bson.M{})
			if err != nil {
				return model.User{}, err
			}
			if count >= registration.maxAccounts {
				return model.User{}, errAuth0AccountLimit
			}
		}
		_, err = auth0Users().InsertOne(ctx, document)
		return user, err
	}

	if registration.maxAccounts == 0 {
		user, err = create(ctx)
	} else {
		// Take a write lock before reading the count so concurrent web and mobile
		// registrations across replicas cannot both take the last slot.
		session, sessionErr := database.Client.StartSession()
		if sessionErr != nil {
			return model.User{}, sessionErr
		}
		defer session.EndSession(ctx)
		var result interface{}
		result, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
			lock, err := auth0SignupLocks().UpdateOne(tx, bson.M{"_id": auth0RegistrationLock},
				bson.M{"$inc": bson.M{"revision": 1}})
			if err != nil {
				return nil, err
			}
			if lock.MatchedCount != 1 {
				return nil, errors.New("Auth0 registration lock is missing")
			}
			return create(tx)
		})
		if err == nil {
			user = result.(model.User)
		}
	}
	if mongo.IsDuplicateKeyError(err) {
		linked, lookupErr := findAuth0Account(ctx, identity)
		if errors.Is(lookupErr, mongo.ErrNoDocuments) {
			return model.User{}, errAuth0EmailInUse
		}
		return linked, lookupErr
	}
	return user, err
}

func auth0RegistrationFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAuth0EmailUnverified), errors.Is(err, errAuth0AccountDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, errAuth0EmailInUse), errors.Is(err, errAuth0AccountLimit):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errAuth0Profile):
		http.Error(w, err.Error(), http.StatusBadGateway)
	default:
		http.Error(w, "Account setup is temporarily unavailable. Please try again.", http.StatusServiceUnavailable)
	}
}

func (auth *webAuth0) mobileRegistration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	identity, token, ok := authenticateMobile(w, r.WithContext(ctx))
	if !ok {
		return
	}
	if identity.Issuer != auth.issuer {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_, err := findAuth0Account(ctx, identity)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		auth0RegistrationFailure(w, err)
		return
	}
	profile, err := auth.getMobileProfile(ctx, token, identity.Subject)
	if err == nil {
		_, err = auth.registration.account(ctx, identity, profile)
	}
	if err != nil {
		auth0RegistrationFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (auth *webAuth0) getMobileProfile(ctx context.Context, token, subject string) (auth0Profile, error) {
	var profile auth0Profile
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.issuer+"userinfo", nil)
	if err != nil {
		return profile, errAuth0Profile
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := auth.httpClient.Do(req)
	if err != nil {
		return profile, errAuth0Profile
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&profile) != nil ||
		profile.Subject != subject || profile.Email == "" {
		return auth0Profile{}, errAuth0Profile
	}
	return profile, nil
}
