package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionModeUsesCookieWithoutLegacyLogin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/session" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		cookie, err := r.Cookie("rl_session")
		if err != nil || cookie.Value != "test-session" {
			t.Error("missing session cookie")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"test@example.com","role":"user"}`))
	}))
	defer server.Close()
	if _, err := endpointClient(context.Background(), endpointConfig{BaseURL: server.URL, Session: "test-session"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unexpected request count")
	}
}
func TestLegacyLoginThenVerifySession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			if r.Method != "POST" {
				t.Error("wrong login method")
			}
			http.SetCookie(w, &http.Cookie{Name: "rl_session", Value: "from-login", Path: "/"})
		case "/api/session":
			cookie, err := r.Cookie("rl_session")
			if err != nil || cookie.Value != "from-login" {
				t.Error("login cookie was not retained")
			}
			w.Write([]byte(`{"username":"test@example.com","role":"user"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	if _, err := endpointClient(context.Background(), endpointConfig{BaseURL: server.URL, AuthMode: "legacy", Username: "test@example.com", Password: "test-only"}); err != nil {
		t.Fatal(err)
	}
}
func TestAuth0Legacy404HasActionableError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, err := endpointClient(context.Background(), endpointConfig{BaseURL: server.URL, AuthMode: "legacy", Username: "test@example.com", Password: "test-only"})
	if err == nil || !strings.Contains(err.Error(), "Auth0") {
		t.Fatalf("unexpected error %v", err)
	}
}
func TestSessionRedirectAndExpiryAreRejected(t *testing.T) {
	for _, status := range []int{302, 401} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/authorize")
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := endpointClient(context.Background(), endpointConfig{BaseURL: server.URL, Session: "expired"})
			if err == nil || calls != 1 {
				t.Fatalf("redirect/expiry accepted or followed: %v calls=%d", err, calls)
			}
		})
	}
}
func TestEndpointConfigurationFailsBeforeNetwork(t *testing.T) {
	for _, cfg := range []endpointConfig{{BaseURL: "file:///tmp/test"}, {BaseURL: "https://user:secret@example.com", Session: "x"}, {BaseURL: "http://localhost:8080"}, {BaseURL: "http://localhost:8080", AuthMode: "legacy"}} {
		if _, err := endpointClient(context.Background(), cfg); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
}
