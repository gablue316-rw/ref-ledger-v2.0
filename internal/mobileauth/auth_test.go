package mobileauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/validator"
	"gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"
)

func TestAuth0Tokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
		badKey bool
		valid  bool
	}{
		{"valid", func(map[string]interface{}) {}, false, true},
		{"expired", func(p map[string]interface{}) { p["exp"] = time.Now().Unix() - 300 }, false, false},
		{"missing expiry", func(p map[string]interface{}) { delete(p, "exp") }, false, false},
		{"wrong issuer", func(p map[string]interface{}) { p["iss"] = "https://other.auth0.com/" }, false, false},
		{"wrong audience", func(p map[string]interface{}) { p["aud"] = "other" }, false, false},
		{"wrong native client", func(p map[string]interface{}) { p["azp"] = "other" }, false, false},
		{"missing permission", func(p map[string]interface{}) { p["scope"] = "openid profile" }, false, false},
		{"missing subject", func(p map[string]interface{}) { delete(p, "sub") }, false, false},
		{"future not before", func(p map[string]interface{}) { p["nbf"] = time.Now().Unix() + 600 }, false, false},
		{"forged signature", func(map[string]interface{}) {}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]interface{}{"iss": "https://example.auth0.com/", "sub": "auth0|test", "aud": []string{"ref-ledger-dev", "https://example.auth0.com/userinfo"}, "azp": "mobile", "scope": "openid dashboard:read", "exp": time.Now().Unix() + 300}
			tc.change(p)
			signingKey := key
			if tc.badKey {
				signingKey = other
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, nil)
			if err != nil {
				t.Fatal(err)
			}
			token, err := jwt.Signed(signer).Claims(p).CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			checker, err := validator.New(func(context.Context) (interface{}, error) { return &key.PublicKey, nil }, validator.RS256, "https://example.auth0.com/", []string{"ref-ledger-dev"}, validator.WithCustomClaims(func() validator.CustomClaims { return &mobileClaims{expectedClient: "mobile"} }))
			if err != nil {
				t.Fatal(err)
			}
			v := &Validator{jwt: checker}
			identity, err := v.Validate(context.Background(), token)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && identity.Subject != "auth0|test" {
				t.Fatal("incorrect identity")
			}
		})
	}
}
func TestConfiguration(t *testing.T) {
	for _, issuer := range []string{"", "http://example.auth0.com/", "https://example.auth0.com", "https://example.auth0.com/path/", "https://example.auth0.com/?query=1"} {
		if _, err := New(Config{Issuer: issuer, Audience: "api", MobileClientID: "client"}); err == nil {
			t.Fatalf("accepted invalid issuer %s", issuer)
		}
	}
	v, err := New(Config{Issuer: "https://example.auth0.com/", Audience: "api", MobileClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "not.a.jwt", "Bearer token"} {
		if _, err := v.Validate(context.Background(), token); err == nil {
			t.Fatal("accepted malformed token")
		}
	}
}
