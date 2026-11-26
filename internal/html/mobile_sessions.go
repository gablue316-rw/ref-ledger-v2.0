package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/mobileauth"
	"ref-ledger-v2/internal/model"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mobileSessionHeader   = "X-Ref-Ledger-Mobile-Session"
	mobileSessionType     = "mobile"
	mobileSessionDuration = 24 * time.Hour
)

var errMobileSessionRequired = errors.New("active mobile session required")

// Mobile sessions live beside web sessions. sessionId is a display ID only; the
// secret the app presents is stored solely as a hash and grants nothing without
// a valid Auth0 token for the same identity.
type mobileSessionResponse struct {
	SessionID    string    `json:"sessionId"`
	SessionToken string    `json:"sessionToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func hashMobileSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func mobileSessions() *mongo.Collection {
	return database.Client.Database(database.GetMongoDbName()).Collection("sessions")
}

func createMobileSession(ctx context.Context, identity mobileauth.Identity, user model.User) (mobileSessionResponse, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return mobileSessionResponse{}, err
	}
	now := time.Now()
	res := mobileSessionResponse{
		SessionID:    uuid.New().String(),
		SessionToken: base64.RawURLEncoding.EncodeToString(secret),
		ExpiresAt:    now.Add(mobileSessionDuration),
	}
	_, err := mobileSessions().InsertOne(ctx, bson.M{
		"sessionId": res.SessionID, "type": mobileSessionType, "platform": "android",
		"username": user.Username, "tenantId": user.TenantID, "role": user.Role,
		"issuer": identity.Issuer, "subject": identity.Subject,
		"mobileTokenHash": hashMobileSessionToken(res.SessionToken),
		"createdAt":       now, "lastActivityAt": now, "expiresAt": res.ExpiresAt,
	})
	if err != nil {
		return mobileSessionResponse{}, err
	}
	// Retire legacy activity markers, which have no session ID.
	if _, err := mobileSessions().DeleteOne(ctx, bson.M{"_id": bson.D{
		{Key: "type", Value: "mobileActivity"}, {Key: "tenantId", Value: user.TenantID}, {Key: "username", Value: user.Username},
	}}); err != nil {
		log.Printf("Unable to remove legacy mobile activity marker: %v", err)
	}
	return res, nil
}

// requireMobileSession accepts a request only when its session secret belongs to
// an unexpired, unrevoked session bound to the authenticated identity and account.
func requireMobileSession(ctx context.Context, r *http.Request, identity mobileauth.Identity, user model.User) error {
	token := strings.TrimSpace(r.Header.Get(mobileSessionHeader))
	if token == "" {
		return errMobileSessionRequired
	}
	now := time.Now()
	err := mobileSessions().FindOneAndUpdate(ctx, bson.M{
		"type": mobileSessionType, "mobileTokenHash": hashMobileSessionToken(token),
		"issuer": identity.Issuer, "subject": identity.Subject,
		"tenantId": user.TenantID, "username": user.Username,
		"expiresAt": bson.M{"$gt": now},
	}, bson.M{"$set": bson.M{"lastActivityAt": now, "expiresAt": now.Add(mobileSessionDuration), "role": user.Role}},
		options.FindOneAndUpdate().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errMobileSessionRequired
	}
	return err
}

func writeMobileSessionError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMobileSessionRequired) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.Header().Set("X-Ref-Ledger-Session", "invalid")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
}

// MobileSession creates (POST) or ends (DELETE) the caller's mobile session.
func MobileSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	identity, _, ok := authenticateMobile(w, r.WithContext(ctx))
	if !ok {
		return
	}
	if database.Client == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodDelete {
		token := strings.TrimSpace(r.Header.Get(mobileSessionHeader))
		if token != "" {
			if _, err := mobileSessions().DeleteOne(ctx, bson.M{
				"type": mobileSessionType, "mobileTokenHash": hashMobileSessionToken(token),
				"issuer": identity.Issuer, "subject": identity.Subject,
			}); err != nil {
				http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	user, err := findMobileAccount(ctx, identity)
	if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errAuth0AccountDenied) {
		http.Error(w, "Account access denied", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	session, err := createMobileSession(ctx, identity, user)
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(session)
}
