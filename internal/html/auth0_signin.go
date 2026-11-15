package main

import (
	"embed"
	"errors"
	"html/template"
	"net/http"
)

//go:embed auth0SignIn.html
var auth0SignInFiles embed.FS
var auth0SignInTemplate = template.Must(template.ParseFS(auth0SignInFiles, "auth0SignIn.html"))

type auth0SignInView struct {
	Nonce   string
	Verify  bool
	Return  bool
	Failed  bool
	Message string
	Signup  bool
}

func (auth *webAuth0) signInPage(w http.ResponseWriter, r *http.Request) {
	if !webAuthGet(w, r) {
		return
	}
	view := auth0SignInView{Signup: auth.registration != nil}
	switch r.URL.Query().Get("notice") {
	case "verify":
		view.Verify = true
	case "return":
		view.Return = true
	}
	// Provider result flags are display hints only. They never grant access,
	// mark an email verified, or create a local account/session.
	if r.URL.Query().Get("success") == "false" {
		view.Failed = true
		view.Message = "This verification link could not be completed. It may have expired or already been used. Return to sign-in to check your account, or contact support if you still need a new verification email."
	}
	renderAuth0SignIn(w, view)
}

func renderAuth0SignIn(w http.ResponseWriter, view auth0SignInView) {
	nonce, err := newWebAuthValue()
	if err != nil {
		http.Error(w, "Unable to display sign-in. Please try again.", http.StatusServiceUnavailable)
		return
	}
	view.Nonce = nonce
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	_ = auth0SignInTemplate.Execute(w, view)
}

func webRegistrationMessage(err error) string {
	switch {
	case errors.Is(err, errAuth0EmailInUse):
		return "An existing Ref Ledger account uses this email with a different sign-in identity. Contact support to link your account."
	case errors.Is(err, errAuth0AccountLimit):
		return "New account registration is currently full. Please contact support."
	case errors.Is(err, errAuth0AccountDenied):
		return "This account cannot sign in. Please contact support."
	default:
		return "Account setup is temporarily unavailable. Please try signing in again shortly."
	}
}
