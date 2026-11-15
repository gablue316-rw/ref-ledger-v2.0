package main

import (
	"net/http/httptest"
	"strings"
	"testing"
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
