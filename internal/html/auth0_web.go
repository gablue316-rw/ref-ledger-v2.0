package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/mobileauth"
)

const webAuthCookie = "__Host-rl_auth0"
const webAuthCallback = "/auth/auth0/callback"
const mobileSignupPath = "/mobile/create-account"
const mobileSetupPath = "/mobile/setup"

type webAuth0 struct {
	registration *auth0Registration
	issuer       string
	clientID     string
	clientSecret string
	callbackURL  string
	httpClient   *http.Client
	tokens       *validator.Validator
}

type webAuthClaims struct {
	Nonce string `json:"nonce"`
	auth0Profile
}

func (*webAuthClaims) Validate(context.Context) error { return nil }

func configureWebAuth0() (*webAuth0, error) {
	mode := os.Getenv("WEB_AUTH_MODE")
	if mode == "" || mode == "local" {
		return nil, nil
	}
	if mode != "auth0" {
		return nil, errors.New("WEB_AUTH_MODE must be local or auth0")
	}

	auth := &webAuth0{
		issuer:       os.Getenv("WEB_AUTH0_ISSUER"),
		clientID:     os.Getenv("WEB_AUTH0_CLIENT_ID"),
		clientSecret: os.Getenv("WEB_AUTH0_CLIENT_SECRET"),
		callbackURL:  os.Getenv("WEB_AUTH0_CALLBACK_URL"),
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	issuer, err := url.Parse(auth.issuer)
	if err != nil || !validWebAuthURL(issuer, "/") {
		return nil, errors.New("WEB_AUTH0_ISSUER must be an HTTPS origin ending in /")
	}
	callback, err := url.Parse(auth.callbackURL)
	if err != nil || !validWebAuthURL(callback, webAuthCallback) {
		return nil, errors.New("WEB_AUTH0_CALLBACK_URL must be an HTTPS URL ending in " + webAuthCallback)
	}
	if auth.clientID == "" || auth.clientSecret == "" {
		return nil, errors.New("configure WEB_AUTH0_CLIENT_ID and WEB_AUTH0_CLIENT_SECRET")
	}
	if mobileIssuer := os.Getenv("MOBILE_OIDC_ISSUER"); mobileIssuer != "" && mobileIssuer != auth.issuer {
		return nil, errors.New("web and mobile Auth0 issuers must match")
	}

	keys := *issuer
	keys.Path = "/.well-known/jwks.json"
	provider := jwks.NewCachingProvider(issuer, 5*time.Minute,
		jwks.WithCustomJWKSURI(&keys), jwks.WithCustomClient(auth.httpClient))
	auth.tokens, err = validator.New(provider.KeyFunc, validator.RS256, auth.issuer, []string{auth.clientID},
		validator.WithCustomClaims(func() validator.CustomClaims { return &webAuthClaims{} }))
	if err != nil {
		return nil, err
	}
	return auth, nil
}

func ensureWebAuthIdentityIndex(ctx context.Context) error {
	users := database.Client.Database(database.GetMongoDbName()).Collection("users")
	_, err := users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "mobileIdentity.issuer", Value: 1},
			{Key: "mobileIdentity.subject", Value: 1},
		},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{
			"mobileIdentity.issuer":  bson.M{"$type": "string"},
			"mobileIdentity.subject": bson.M{"$type": "string"},
		}),
	})
	return err
}

func validWebAuthURL(u *url.URL, path string) bool {
	return u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		u.Path == path && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func newWebAuthValue() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func setWebAuthCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: webAuthCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func webAuthGet(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (auth *webAuth0) login(w http.ResponseWriter, r *http.Request) {
	auth.start(w, r, false)
}

func (auth *webAuth0) signup(w http.ResponseWriter, r *http.Request) {
	auth.start(w, r, true)
}

func (auth *webAuth0) start(w http.ResponseWriter, r *http.Request, signup bool) {
	if !webAuthGet(w, r) {
		return
	}
	state, stateErr := newWebAuthValue()
	nonce, nonceErr := newWebAuthValue()
	verifier, verifierErr := newWebAuthValue()
	if stateErr != nil || nonceErr != nil || verifierErr != nil {
		http.Error(w, "Could not start sign-in.", http.StatusInternalServerError)
		return
	}

	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {auth.clientID},
		"redirect_uri":          {auth.callbackURL},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"login"},
	}
	if signup {
		query.Set("screen_hint", "signup")
	}
	flow := []string{state, nonce, verifier}
	if auth.registration != nil && r.URL.Query().Get("from") == "mobile" {
		flow = append(flow, "mobile")
	}
	setWebAuthCookie(w, strings.Join(flow, "."), 600)
	http.Redirect(w, r, auth.issuer+"authorize?"+query.Encode(), http.StatusSeeOther)
}

func (auth *webAuth0) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {auth.clientID},
		"client_secret": {auth.clientSecret},
		"redirect_uri":  {auth.callbackURL},
		"code":          {code},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.issuer+"oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := auth.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("Auth0 rejected the authorization code")
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", err
	}
	if body.IDToken == "" {
		return "", errors.New("Auth0 did not return an ID token")
	}
	return body.IDToken, nil
}

func (auth *webAuth0) callback(w http.ResponseWriter, r *http.Request) {
	if !webAuthGet(w, r) {
		return
	}
	setWebAuthCookie(w, "", -1)
	query := r.URL.Query()
	cookie, err := r.Cookie(webAuthCookie)
	if err != nil {
		http.Error(w, "Sign-in expired. Return to /login and try again.", http.StatusBadRequest)
		return
	}
	flow := strings.Split(cookie.Value, ".")
	mobileOnboarding := len(flow) == 4 && flow[3] == "mobile"
	if (len(flow) != 3 && !mobileOnboarding) || len(flow[0]) != 43 || len(flow[1]) != 43 || len(flow[2]) != 43 ||
		subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(flow[0])) != 1 ||
		query.Get("error") != "" || query.Get("code") == "" {
		http.Error(w, "Sign-in did not complete. Return to /login and try again.", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := auth.exchange(ctx, query.Get("code"), flow[2])
	if err != nil {
		http.Error(w, "Could not complete Auth0 sign-in. Return to /login and try again.", http.StatusBadGateway)
		return
	}
	validated, err := auth.tokens.ValidateToken(ctx, token)
	claims, ok := validated.(*validator.ValidatedClaims)
	if err != nil || !ok || claims.RegisteredClaims.Subject == "" ||
		claims.RegisteredClaims.Expiry <= time.Now().Unix() ||
		len(claims.RegisteredClaims.Audience) != 1 {
		http.Error(w, "Invalid sign-in response.", http.StatusUnauthorized)
		return
	}
	profile, ok := claims.CustomClaims.(*webAuthClaims)
	if !ok || subtle.ConstantTimeCompare([]byte(profile.Nonce), []byte(flow[1])) != 1 {
		http.Error(w, "Invalid sign-in response.", http.StatusUnauthorized)
		return
	}
	if !profile.EmailVerified {
		if mobileOnboarding {
			http.Redirect(w, r, mobileSignupPath+"#verify", http.StatusSeeOther)
			return
		}
		http.Error(w, errAuth0EmailUnverified.Error(), http.StatusForbidden)
		return
	}

	identity := mobileauth.Identity{
		Issuer: claims.RegisteredClaims.Issuer, Subject: claims.RegisteredClaims.Subject,
	}
	user, err := findAuth0Account(ctx, identity)
	if errors.Is(err, mongo.ErrNoDocuments) && auth.registration != nil {
		release, lockErr := beginMaintenanceWrite(database.Client.Database(database.Database))
		if lockErr != nil {
			maintenanceUnavailable(w, r)
			return
		}
		defer release()
		profile.Subject = identity.Subject
		user, err = auth.registration.account(ctx, identity, profile.auth0Profile)
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		http.Error(w, "This Auth0 identity is not linked to a Ref Ledger account.", http.StatusForbidden)
		return
	}
	if err != nil {
		auth0RegistrationFailure(w, err)
		return
	}
	if err := createWebSession(w, r.WithContext(ctx), user, true); err != nil {
		http.Error(w, "Could not start the Ref Ledger session.", http.StatusServiceUnavailable)
		return
	}
	if mobileOnboarding {
		http.Redirect(w, r, mobileSetupPath, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
