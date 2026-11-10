package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"ref-ledger-v2/internal/model"
	"testing"
)

func TestMobileGameCookieCannotAuthorizeWrites(t *testing.T) {
	t.Setenv("MAINTENANCE_MODE", "false")
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		called := false
		handler := mobileAccountHandler(func(http.ResponseWriter, *http.Request) { called = true })
		r := httptest.NewRequest(method, "/api/mobile/game?tenantId=other", nil)
		r.AddCookie(&http.Cookie{Name: "rl_session", Value: "web-session"})
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 401 || called {
			t.Fatalf("%s status %d called %v", method, w.Code, called)
		}
	}
}

// Test the same admission boundary used by the application's outer middleware.
func TestMobileWritesRespectMaintenance(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		admitted := false
		handler := maintenanceMiddlewareWithAdmission(mobileAccountHandler(func(http.ResponseWriter, *http.Request) { t.Fatal("write allowed") }), func() (func(), error) { admitted = true; return nil, errors.New("maintenance active") })
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/mobile/game", nil))
		if w.Code != http.StatusServiceUnavailable || !admitted {
			t.Fatalf("status %d, admission checked %v", w.Code, admitted)
		}
	}
}
func TestMobileGameRoleAndValidation(t *testing.T) {
	if mobileCanWrite("readonly") || mobileCanWrite("") || !mobileCanWrite("user") || !mobileCanWrite("admin") {
		t.Fatal("incorrect write roles")
	}
	old := model.GameDescriptor{GameId: "45", Association: "MSO", Status: "Pending", Time: "7:30 PM", NumOfGames: "2"}
	for _, mutate := range []func(*model.GameDescriptor){func(g *model.GameDescriptor) { g.Status = "Paid" }, func(g *model.GameDescriptor) { g.Status = "Delete" }, func(g *model.GameDescriptor) { g.GameId = "46" }, func(g *model.GameDescriptor) { g.Association = "other" }, func(g *model.GameDescriptor) { g.Time = "25:00" }, func(g *model.GameDescriptor) { g.NumOfGames = "0" }} {
		game := old
		mutate(&game)
		if validateMobileGame(game, old) == nil {
			t.Fatalf("accepted invalid change %+v", game)
		}
	}
	for _, status := range []string{"Pending", "Completed", "Cancelled"} {
		g := old
		g.Status = status
		if err := validateMobileGame(g, old); err != nil {
			t.Fatal(err)
		}
	}
	paid := old
	paid.Status = "Paid"
	if validateMobileGame(old, paid) == nil {
		t.Fatal("paid game editable")
	}
}
