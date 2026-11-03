package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMobileDashboardRejectsCookieOnly(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/mobile/dashboard?tenantId=other", nil)
	r.AddCookie(&http.Cookie{Name: "rl_session", Value: "website-session"})
	w := httptest.NewRecorder()
	MobileDashboard(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only request: status %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing cache protection")
	}
}
func TestMobileDashboardReadOnly(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/mobile/dashboard", nil)
		w := httptest.NewRecorder()
		MobileDashboard(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status %d", method, w.Code)
		}
	}
}
