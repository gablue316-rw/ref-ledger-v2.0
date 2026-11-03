package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/mobileauth"
)

type mobileAccountContextKey struct{}

var mobileValidatorOnce sync.Once
var mobileValidator *mobileauth.Validator
var mobileValidatorError error

func configuredMobileValidator() (*mobileauth.Validator, error) {
	mobileValidatorOnce.Do(func() {
		mobileValidator, mobileValidatorError = mobileauth.New(mobileauth.Config{
			Issuer: os.Getenv("MOBILE_OIDC_ISSUER"), Audience: os.Getenv("MOBILE_API_AUDIENCE"), MobileClientID: os.Getenv("MOBILE_OIDC_CLIENT_ID"),
		})
	})
	return mobileValidator, mobileValidatorError
}

// MobileDashboard accepts only provider-validated bearer tokens. Website cookies
// and client-supplied account IDs never select the mobile account.
func MobileDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", 405)
		return
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", 401)
		return
	}
	validation, err := configuredMobileValidator()
	if err != nil {
		http.Error(w, "Mobile authentication is not configured", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	identity, err := validation.Validate(ctx, parts[1])
	if err != nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", 401)
		return
	}
	var user struct {
		TenantID string `bson:"tenantId"`
		Role     string `bson:"role"`
	}
	if database.Client == nil {
		http.Error(w, "Service unavailable", 503)
		return
	}
	// Explicit administrator-managed linkage; never link accounts by email alone.
	err = database.Client.Database(database.Database).Collection("users").FindOne(ctx, bson.M{
		"mobileIdentity.issuer": identity.Issuer, "mobileIdentity.subject": identity.Subject,
	}).Decode(&user)
	if err != nil || user.TenantID == "" || user.TenantID == "na" || (user.Role != "user" && user.Role != "readonly" && user.Role != "admin") {
		http.Error(w, "Account access denied", 403)
		return
	}
	GetGames(w, r.WithContext(context.WithValue(ctx, mobileAccountContextKey{}, user.TenantID)))
}
