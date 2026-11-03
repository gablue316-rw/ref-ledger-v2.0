// Package mobileauth validates Auth0 RS256 access tokens for the mobile API.
package mobileauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
)

type Config struct{ Issuer, Audience, MobileClientID string }
type Identity struct{ Issuer, Subject string }
type Validator struct{ jwt *validator.Validator }
type mobileClaims struct {
	Scope           string `json:"scope"`
	AuthorizedParty string `json:"azp"`
	expectedClient  string
}

func (c *mobileClaims) Validate(context.Context) error {
	if c.AuthorizedParty != c.expectedClient {
		return errors.New("unexpected authorized client")
	}
	for _, scope := range strings.Fields(c.Scope) {
		if scope == "dashboard:read" {
			return nil
		}
	}
	return errors.New("dashboard:read scope required")
}

// New uses only the configured issuer's JWKS; token headers cannot select URLs.
func New(c Config) (*Validator, error) {
	u, err := url.Parse(c.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/" || c.Audience == "" || c.MobileClientID == "" {
		return nil, errors.New("configure an HTTPS Auth0 issuer ending in /, API audience, and native client ID")
	}
	keys := *u
	keys.Path = "/.well-known/jwks.json"
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider := jwks.NewCachingProvider(u, 5*time.Minute, jwks.WithCustomJWKSURI(&keys), jwks.WithCustomClient(client))
	jwt, err := validator.New(provider.KeyFunc, validator.RS256, c.Issuer, []string{c.Audience}, validator.WithCustomClaims(func() validator.CustomClaims { return &mobileClaims{expectedClient: c.MobileClientID} }))
	if err != nil {
		return nil, err
	}
	return &Validator{jwt: jwt}, nil
}
func (v *Validator) Validate(ctx context.Context, token string) (Identity, error) {
	fail := errors.New("invalid mobile access token")
	if v == nil || v.jwt == nil || token == "" || len(token) > 16384 || strings.ContainsAny(token, " \t\r\n") {
		return Identity{}, fail
	}
	result, err := v.jwt.ValidateToken(ctx, token)
	if err != nil {
		return Identity{}, fail
	}
	claims, ok := result.(*validator.ValidatedClaims)
	if !ok || claims.RegisteredClaims.Subject == "" || claims.RegisteredClaims.Expiry <= time.Now().Unix() {
		return Identity{}, fail
	}
	return Identity{Issuer: claims.RegisteredClaims.Issuer, Subject: claims.RegisteredClaims.Subject}, nil
}
