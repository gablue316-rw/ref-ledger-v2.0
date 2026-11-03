package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"
	mailer "ref-ledger-v2/internal/email"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

// Validate before creating an account so configuration errors do not create unusable users.
func VerificationConfigured() error {
	_, err := verificationBaseURL()
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("EMAIL_USERNAME")) == "" || strings.TrimSpace(os.Getenv("EMAIL_APP_PASSWORD")) == "" {
		return fmt.Errorf("email credentials are missing")
	}
	return nil
}

func verificationBaseURL() (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("APP_PUBLIC_URL must be an absolute origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return "", fmt.Errorf("APP_PUBLIC_URL requires HTTPS except on localhost")
	}
	return raw, nil
}

// Keep only a hash in MongoDB. The atomic cooldown also prevents concurrent resends.
// A mail failure leaves the user pending; resend is available after one minute.
func SendAccountVerification(ctx context.Context, username, tenantID string) error {
	base, err := verificationBaseURL()
	if err != nil {
		return err
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return err
	}
	token := hex.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	users := database.Client.Database(database.GetMongoDbName()).Collection("users")
	result, err := users.UpdateOne(ctx, bson.M{
		"username": username, "tenantId": tenantID, "emailVerified": false,
		"$or": bson.A{bson.M{"verificationSentAt": bson.M{"$exists": false}}, bson.M{"verificationSentAt": bson.M{"$lte": now.Add(-time.Minute)}}},
	}, bson.M{"$set": bson.M{"verificationTokenHash": hex.EncodeToString(hash[:]), "verificationExpiresAt": now.Add(24 * time.Hour), "verificationSentAt": now}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return nil
	}
	var message mailer.Email
	message.Initialize()
	message.SetTo([]string{username})
	message.SetSubject("Confirm your Ref Ledger email address")
	message.SetBody(fmt.Sprintf("Welcome to Ref Ledger!\n\nConfirm your email address to activate your account:\n\n%s/verifyEmail#token=%s\n\nOpen the link and select Confirm Email. This link expires in 24 hours.\n\nIf you did not create this account, ignore this email.\n", base, token))
	return message.Send()
}

func verificationJSON(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func ConfirmEmailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		verificationJSON(w, 405, "Method not allowed.")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || len(req.Token) != 64 {
		verificationJSON(w, 400, "Invalid or expired confirmation link.")
		return
	}
	if _, err := hex.DecodeString(req.Token); err != nil {
		verificationJSON(w, 400, "Invalid or expired confirmation link.")
		return
	}
	hash := sha256.Sum256([]byte(req.Token))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := database.Client.Database(database.GetMongoDbName()).Collection("users").UpdateOne(ctx,
		bson.M{"emailVerified": false, "verificationTokenHash": hex.EncodeToString(hash[:]), "verificationExpiresAt": bson.M{"$gt": time.Now().UTC()}},
		bson.M{"$set": bson.M{"emailVerified": true, "emailVerifiedAt": time.Now().UTC()}, "$unset": bson.M{"verificationTokenHash": "", "verificationExpiresAt": "", "verificationSentAt": ""}})
	if err != nil {
		verificationJSON(w, 500, "Unable to confirm your email. Please try again.")
		return
	}
	if result.MatchedCount != 1 {
		verificationJSON(w, 400, "This link has expired or has already been used. Sign in if you already confirmed; otherwise request a new email from the login page.")
		return
	}
	verificationJSON(w, 200, "Email confirmed. Your account is now active.")
}

// Require the account password to prevent unauthenticated mail flooding.
func ResendVerificationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		verificationJSON(w, 405, "Method not allowed.")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		verificationJSON(w, 400, "Invalid request.")
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var user struct {
		Username     string `bson:"username"`
		TenantID     string `bson:"tenantId"`
		PasswordHash string `bson:"passwordHash"`
		Verified     *bool  `bson:"emailVerified"`
	}
	err := database.Client.Database(database.GetMongoDbName()).Collection("users").FindOne(ctx, bson.M{"username": req.Username}).Decode(&user)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		verificationJSON(w, 500, "Unable to process request.")
		return
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		verificationJSON(w, 401, "Check your email address and password.")
		return
	}
	if user.Verified != nil && !*user.Verified {
		if err = SendAccountVerification(ctx, user.Username, user.TenantID); err != nil {
			verificationJSON(w, 502, "Unable to send confirmation email. Wait one minute and try again.")
			return
		}
	}
	verificationJSON(w, 200, "If confirmation is still needed, an email has been sent. Please allow one minute between requests and check your spam folder.")
}
