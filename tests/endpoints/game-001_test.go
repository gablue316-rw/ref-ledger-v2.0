package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type endpointConfig struct{ BaseURL, Session, AuthMode, Username, Password string }

func endpointClient(ctx context.Context, cfg endpointConfig) (*http.Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, fmt.Errorf("REF_LEDGER_TEST_BASE_URL must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	base.Path = "/"
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	// Never follow an authentication redirect to Auth0 or another origin.
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	switch cfg.AuthMode {
	case "", "session":
		if cfg.Session == "" {
			return nil, fmt.Errorf("set REF_LEDGER_TEST_SESSION to a current rl_session cookie from an authenticated development browser")
		}
		if strings.ContainsAny(cfg.Session, "\r\n;") {
			return nil, fmt.Errorf("REF_LEDGER_TEST_SESSION must contain only the cookie value")
		}
		jar.SetCookies(base, []*http.Cookie{{Name: "rl_session", Value: cfg.Session, Path: "/", Secure: base.Scheme == "https", HttpOnly: true}})
	case "legacy":
		if cfg.Username == "" || cfg.Password == "" {
			return nil, fmt.Errorf("legacy mode requires REF_LEDGER_TEST_USERNAME and REF_LEDGER_TEST_PASSWORD")
		}
		body, err := json.Marshal(map[string]string{"username": cfg.Username, "password": cfg.Password})
		if err != nil {
			return nil, err
		}
		loginURL := base.ResolveReference(&url.URL{Path: "/api/login"})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("login request failed: %w", err)
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
		response.Body.Close()
		if response.StatusCode == 404 {
			return nil, fmt.Errorf("/api/login is unavailable; Auth0 deployments require session mode, not username/password login")
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("legacy login returned HTTP %d", response.StatusCode)
		}
	default:
		return nil, fmt.Errorf("REF_LEDGER_TEST_AUTH_MODE must be session or legacy")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(&url.URL{Path: "/api/session"}).String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("session request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session check returned HTTP %d; sign in again and update the test session cookie", response.StatusCode)
	}
	var session struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&session); err != nil {
		return nil, fmt.Errorf("invalid session response: %w", err)
	}
	if session.Username == "" || session.Role == "" {
		return nil, fmt.Errorf("session response is missing the authenticated username or role")
	}
	return client, nil
}

// TestLogin is an explicitly selected live endpoint check, not a unit test.
// Ordinary go test ./... never contacts a running app or external database.
func TestLogin(t *testing.T) {
	if os.Getenv("REF_LEDGER_RUN_ENDPOINT_TESTS") != "1" {
		t.Skip("set REF_LEDGER_RUN_ENDPOINT_TESTS=1 to run the live authenticated-session check")
	}
	cfg := endpointConfig{BaseURL: os.Getenv("REF_LEDGER_TEST_BASE_URL"), Session: os.Getenv("REF_LEDGER_TEST_SESSION"), AuthMode: os.Getenv("REF_LEDGER_TEST_AUTH_MODE"), Username: os.Getenv("REF_LEDGER_TEST_USERNAME"), Password: os.Getenv("REF_LEDGER_TEST_PASSWORD")}
	if cfg.BaseURL == "" {
		t.Fatal("REF_LEDGER_TEST_BASE_URL must be explicitly set for a live endpoint test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := endpointClient(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	t.Log("authenticated session verified")
}
