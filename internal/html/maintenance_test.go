package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMaintenanceAdmission(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		guarded      bool
	}{
		{"GET", "/api/dashboard", false}, {"HEAD", "/dashboard", false}, {"OPTIONS", "/api/game-save", false},
		{"POST", "/api/game-save", true}, {"PUT", "/api/game-update", true}, {"PATCH", "/api/games", true},
		{"DELETE", "/api/deletePayment", true}, {"POST", "/api/import/games/commit", true},
		{"GET", "/api/confirmEmail", true}, {"POST", "/api/mobile/register", true},
		{"POST", "/api/login", false}, {"POST", "/api/logout", false},
		{"POST", "/admin/api/admin/maintenance", false}, {"POST", "/administrator", true},
	} {
		for _, failure := range []error{nil, errMaintenanceLocked, errors.New("database unavailable")} {
			t.Run(tc.method+tc.path, func(t *testing.T) {
				admitted, called, released := false, false, false
				h := maintenanceMiddlewareWithAdmission(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }), func() (func(), error) {
					admitted = true
					if failure != nil {
						return nil, failure
					}
					return func() { released = true }, nil
				})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
				blocked := tc.guarded && failure != nil
				if admitted != tc.guarded || called == blocked || released != (tc.guarded && !blocked) {
					t.Fatalf("admitted=%v called=%v released=%v", admitted, called, released)
				}
				if blocked && (w.Code != 503 || w.Header().Get("Cache-Control") != "no-store") {
					t.Fatal("missing maintenance response")
				}
			})
		}
	}
}
