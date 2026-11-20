package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/validator"
	jose "gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"
)

func TestAuth0SignInPage(t *testing.T) {
	auth := &webAuth0{registration: &auth0Registration{}}
	for _, tc := range []struct {
		query, want string
		dialog      bool
	}{
		{"?notice=verify", "Check your email", true},
		{"?notice=return", "Returning from email verification?", false},
		{"?success=false&message=ATTACKER_TEXT", "This verification link could not be completed", false},
		{"?success=true", "Your next season", false},
	} {
		w := httptest.NewRecorder()
		auth.signInPage(w, httptest.NewRequest("GET", "/account/sign-in"+tc.query, nil))
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, tc.want) {
			t.Fatalf("%s: %d %s", tc.query, w.Code, body)
		}
		if strings.Contains(body, `<dialog`) != tc.dialog {
			t.Fatalf("unexpected dialog: %s", tc.query)
		}
		if strings.Contains(body, "ATTACKER_TEXT") || strings.Contains(body, "Your email is verified") {
			t.Fatal("untrusted query used as verification evidence")
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") {
			t.Fatal("missing privacy/security headers")
		}
		if !strings.Contains(body, `href="/login"`) {
			t.Fatal("no sign-in route")
		}
	}
}

func TestAuth0SignInPageMethod(t *testing.T) {
	w := httptest.NewRecorder()
	(&webAuth0{}).signInPage(w, httptest.NewRequest("POST", "/account/sign-in", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestAuth0SignInEscapesMessage(t *testing.T) {
	w := httptest.NewRecorder()
	renderAuth0SignIn(w, auth0SignInView{Failed: true, Message: `<script>alert("bad")</script>`})
	if strings.Contains(w.Body.String(), `<script>alert`) {
		t.Fatal("unescaped error message")
	}
}

func TestWebAuthValidationFailure(t *testing.T) {
	now := time.Unix(1700000000, 0)
	valid := &validator.ValidatedClaims{RegisteredClaims: validator.RegisteredClaims{
		Subject: "subject", Expiry: now.Unix() + 1, Audience: []string{"client"},
	}}
	missingSubject := *valid
	missingSubject.RegisteredClaims.Subject = ""
	expired := *valid
	expired.RegisteredClaims.Expiry = now.Unix()
	extraAudience := *valid
	extraAudience.RegisteredClaims.Audience = []string{"client", "other"}
	var nilClaims *validator.ValidatedClaims
	for _, tc := range []struct {
		name      string
		validated interface{}
		err       error
		want      string
	}{
		{"valid", valid, nil, ""},
		{"nil", nil, nil, "validated_claims_unexpected_or_nil"},
		{"typed nil", nilClaims, nil, "validated_claims_unexpected_or_nil"},
		{"unexpected", "claims", nil, "validated_claims_unexpected_or_nil"},
		{"subject", &missingSubject, nil, "subject_missing"},
		{"expired", &expired, nil, "token_expired"},
		{"audience count", &extraAudience, nil, "audience_count_unexpected"},
		{"jwks", nil, fmt.Errorf("wrapped: %w", errWebAuthJWKS), "validate_token_jwks"},
		{"signature", nil, fmt.Errorf("wrapped: %w", jose.ErrCryptoFailure), "validate_token_signature"},
		{"signing method", nil, errors.New("signing method is invalid: sensitive detail"), "validate_token_signature"},
		{"issuer", nil, fmt.Errorf("wrapped: %w", jwt.ErrInvalidIssuer), "validate_token_issuer"},
		{"audience", nil, fmt.Errorf("wrapped: %w", jwt.ErrInvalidAudience), "validate_token_audience"},
		{"expired error", nil, fmt.Errorf("wrapped: %w", jwt.ErrExpired), "validate_token_timing"},
		{"not yet valid", nil, jwt.ErrNotValidYet, "validate_token_timing"},
		{"issued in future", nil, jwt.ErrIssuedInTheFuture, "validate_token_timing"},
		{"other", nil, errors.New("sensitive detail"), "validate_token_other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := webAuthValidationFailure(tc.validated, tc.err, now); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWebAuthKeyFuncPreservesUnderlyingError(t *testing.T) {
	cause := errors.New("sensitive provider error")
	keyFunc := webAuthKeyFunc(func(context.Context) (interface{}, error) { return nil, cause })
	_, err := keyFunc(context.Background())
	if !errors.Is(err, errWebAuthJWKS) || !errors.Is(err, cause) {
		t.Fatal("JWKS category or underlying error not preserved")
	}
}

func TestWebAuthCallbackValidationDiagnostics(t *testing.T) {
	key := []byte("a sufficiently long test signing key")
	otherKey := []byte("a different test signing key value")
	state := strings.Repeat("s", 43)
	nonce := strings.Repeat("n", 43)
	verifier := strings.Repeat("v", 43)
	const code = "sensitive-authorization-code"
	const secret = "sensitive-client-secret"

	for _, tc := range []struct {
		name        string
		environment string
		kind        string
		want        string
	}{
		{"jwks", "development", "jwks", "validate_token_jwks"},
		{"signature", "development", "signature", "validate_token_signature"},
		{"signing method", "development", "signing method", "validate_token_signature"},
		{"issuer", "development", "issuer", "validate_token_issuer"},
		{"audience", "development", "audience", "validate_token_audience"},
		{"validator expiry", "development", "validator expiry", "validate_token_timing"},
		{"subject", "development", "subject", "subject_missing"},
		{"callback expiry", "development", "callback expiry", "token_expired"},
		{"audience count", "development", "audience count", "audience_count_unexpected"},
		{"custom claims", "development", "custom claims", "custom_claims_unexpected_or_nil"},
		{"nonce", "development", "nonce", "nonce_mismatch"},
		{"other", "development", "other", "validate_token_other"},
		{"production silent", "production", "subject", ""},
		{"unset silent", "", "subject", ""},
		{"other environment silent", "development-staging", "subject", ""},
		{"explicit development", " Development ", "subject", "subject_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.environment)
			var output bytes.Buffer
			oldOutput := log.Writer()
			oldFlags, oldPrefix := log.Flags(), log.Prefix()
			log.SetOutput(&output)
			log.SetFlags(0)
			log.SetPrefix("")
			t.Cleanup(func() {
				log.SetOutput(oldOutput)
				log.SetFlags(oldFlags)
				log.SetPrefix(oldPrefix)
			})

			var token string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]string{"id_token": token}); err != nil {
					t.Errorf("encode token response: %v", err)
				}
			}))
			defer server.Close()
			issuer := server.URL + "/"
			subject := "subject"
			audience := []string{"client"}
			expiry := time.Now().Add(time.Hour)
			claimsNonce := nonce
			signingKey := key
			signingAlgorithm := jose.HS256
			switch tc.kind {
			case "signature":
				signingKey = otherKey
			case "signing method":
				signingAlgorithm = jose.HS384
			case "issuer":
				issuer = "https://wrong-issuer.example/"
			case "audience":
				audience = []string{"other"}
			case "validator expiry":
				expiry = time.Now().Add(-time.Hour)
			case "subject":
				subject = ""
			case "callback expiry":
				expiry = time.Now().Add(-10 * time.Second)
			case "audience count":
				audience = []string{"client", "other"}
			case "nonce":
				claimsNonce = "different-sensitive-nonce"
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: signingAlgorithm, Key: signingKey}, nil)
			if err != nil {
				t.Fatal(err)
			}
			token, err = jwt.Signed(signer).Claims(map[string]interface{}{
				"iss": issuer, "sub": subject, "aud": audience, "exp": expiry.Unix(), "nonce": claimsNonce,
			}).CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "other" {
				token = "sensitive-invalid-token"
			}
			keyFunc := webAuthKeyFunc(func(context.Context) (interface{}, error) {
				if tc.kind == "jwks" {
					return nil, errors.New("sensitive-jwks-error")
				}
				return key, nil
			})
			opts := []validator.Option{validator.WithAllowedClockSkew(time.Minute)}
			if tc.kind != "custom claims" {
				opts = append(opts, validator.WithCustomClaims(func() validator.CustomClaims { return &webAuthClaims{} }))
			}
			tokens, err := validator.New(keyFunc, validator.HS256, server.URL+"/", []string{"client"}, opts...)
			if err != nil {
				t.Fatal(err)
			}
			auth := &webAuth0{issuer: server.URL + "/", clientID: "client", clientSecret: secret, tokens: tokens, httpClient: server.Client()}
			request := httptest.NewRequest(http.MethodGet, "/auth/auth0/callback?state="+state+"&code="+code, nil)
			cookieValue := strings.Join([]string{state, nonce, verifier}, ".")
			request.AddCookie(&http.Cookie{Name: webAuthCookie, Value: cookieValue})
			response := httptest.NewRecorder()
			auth.callback(response, request)
			if response.Code != http.StatusUnauthorized || response.Body.String() != "Invalid sign-in response.\n" {
				t.Fatalf("unexpected response: %d %q", response.Code, response.Body.String())
			}
			logged := output.String()
			if tc.want == "" {
				if logged != "" {
					t.Fatalf("unexpected diagnostic: %q", logged)
				}
			} else if logged != "Auth0 callback invalid sign-in response: check="+tc.want+"\n" {
				t.Fatalf("unexpected diagnostic for %q: %q", tc.want, logged)
			}
			for _, sensitive := range []string{token, cookieValue, state, nonce, verifier, code, secret, "sensitive-jwks-error", claimsNonce} {
				if strings.Contains(logged, sensitive) {
					t.Fatalf("diagnostic contains sensitive input")
				}
			}
		})
	}
}
