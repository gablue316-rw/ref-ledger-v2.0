package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"errors"
	"go.mongodb.org/mongo-driver/mongo"
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
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	identity, _, ok := authenticateMobile(w, r.WithContext(ctx))
	if !ok {
		return
	}
	if database.Client == nil {
		http.Error(w, "Service unavailable", 503)
		return
	}
	// Apply the same linked-account eligibility rules as web sign-in.
	user, err := findAuth0Account(ctx, identity)
	if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errAuth0AccountDenied) {
		http.Error(w, "Account access denied", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	GetGames(w, r.WithContext(context.WithValue(ctx, mobileAccountContextKey{}, user.TenantID)))
}

func authenticateMobile(w http.ResponseWriter, r *http.Request) (mobileauth.Identity, string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", 401)
		return mobileauth.Identity{}, "", false
	}
	validation, err := configuredMobileValidator()
	if err != nil {
		http.Error(w, "Mobile authentication is not configured", 503)
		return mobileauth.Identity{}, "", false
	}
	identity, err := validation.Validate(r.Context(), parts[1])
	if err != nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", 401)
		return mobileauth.Identity{}, "", false
	}
	return identity, parts[1], true
}
