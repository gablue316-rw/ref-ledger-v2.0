package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/mobileauth"
	"ref-ledger-v2/internal/model"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	jose "gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"
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

func TestMobileDashboardAuthenticatedActivity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	mobileValidatorOnce, mobileValidator, mobileValidatorError = sync.Once{}, nil, nil
	defer func() { mobileValidatorOnce, mobileValidator, mobileValidatorError = sync.Once{}, nil, nil }()
	t.Setenv("MOBILE_OIDC_ISSUER", server.URL+"/")
	t.Setenv("MOBILE_API_AUDIENCE", "mobile-api")
	t.Setenv("MOBILE_OIDC_CLIENT_ID", "mobile-client")
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(map[string]interface{}{
		"iss": server.URL + "/", "sub": "linked-subject", "aud": "mobile-api", "azp": "mobile-client",
		"scope": "dashboard:read", "exp": time.Now().Add(time.Minute).Unix(),
	}).CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("games uses linked account", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".users", mtest.FirstBatch, bson.D{
				{Key: "username", Value: "mobile-user"}, {Key: "tenantId", Value: "account-a"}, {Key: "role", Value: "user"},
			}),
			mtest.CreateSuccessResponse(),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch),
		)
		req := httptest.NewRequest(http.MethodGet, "/api/mobile/dashboard?tenantId=account-b", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		MobileDashboard(w, req)
		if w.Code != http.StatusNotFound {
			mt.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 3 || events[1].CommandName != "update" || events[2].CommandName != "find" {
			mt.Fatalf("unexpected commands: %+v", events)
		}
		activity := events[1].Command.Lookup("updates").Array().Index(0).Value().Document().Lookup("u").Document().Lookup("$set").Document()
		gameFilter := events[2].Command.Lookup("filter").Document()
		if activity.Lookup("tenantId").StringValue() != "account-a" || activity.Lookup("username").StringValue() != "mobile-user" || gameFilter.Lookup("tenantId").StringValue() != "account-a" {
			mt.Fatalf("wrong account: activity=%s games=%s", activity, gameFilter)
		}
		if len(w.Result().Cookies()) != 0 {
			mt.Fatal("mobile activity created a web session cookie")
		}
	})
	mt.Run("other mobile requests record activity", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".users", mtest.FirstBatch, bson.D{
				{Key: "username", Value: "mobile-user"}, {Key: "tenantId", Value: "account-a"}, {Key: "role", Value: "readonly"},
			}),
			mtest.CreateSuccessResponse(),
		)
		req := httptest.NewRequest(http.MethodGet, "/api/mobile/game?tenantId=account-b", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		called := false
		mobileAccountHandler(func(w http.ResponseWriter, r *http.Request) {
			called = true
			tenant, err := getTenantId(r)
			if err != nil || tenant != "account-a" {
				mt.Fatalf("tenant=%q err=%v", tenant, err)
			}
		})(w, req)
		events := mt.GetAllStartedEvents()
		if !called || len(events) != 2 || events[1].CommandName != "update" {
			mt.Fatalf("called=%v commands=%+v", called, events)
		}
	})
	mt.Run("invalid token records no activity", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		req := httptest.NewRequest(http.MethodGet, "/api/mobile/dashboard", nil)
		req.Header.Set("Authorization", "******")
		w := httptest.NewRecorder()
		MobileDashboard(w, req)
		if w.Code != http.StatusUnauthorized || len(mt.GetAllStartedEvents()) != 0 {
			mt.Fatalf("status=%d commands=%+v", w.Code, mt.GetAllStartedEvents())
		}
	})
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

func TestMobileAccountActivity(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("linked account records activity", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".users", mtest.FirstBatch, bson.D{
				{Key: "username", Value: "user@example.test"}, {Key: "tenantId", Value: "account-a"}, {Key: "role", Value: "user"},
			}),
			mtest.CreateSuccessResponse(),
		)
		before := time.Now().Add(sessionDuration)
		user, err := findMobileAccount(context.Background(), mobileauth.Identity{Issuer: "https://issuer.example/", Subject: "subject"})
		after := time.Now().Add(sessionDuration)
		if err != nil || user.TenantID != "account-a" {
			mt.Fatalf("user=%+v err=%v", user, err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 2 || events[0].CommandName != "find" || events[1].CommandName != "update" {
			mt.Fatalf("unexpected commands: %+v", events)
		}
		query := events[0].Command.Lookup("filter").Document()
		if query.Lookup("mobileIdentity.issuer").StringValue() != "https://issuer.example/" || query.Lookup("mobileIdentity.subject").StringValue() != "subject" {
			mt.Fatalf("wrong identity filter: %s", query)
		}
		update := events[1].Command.Lookup("updates").Array().Index(0).Value().Document()
		id := update.Lookup("q").Document().Lookup("_id").Document()
		if id.Lookup("type").StringValue() != "mobileActivity" || id.Lookup("tenantId").StringValue() != user.TenantID || id.Lookup("username").StringValue() != user.Username || !update.Lookup("upsert").Boolean() {
			mt.Fatalf("wrong activity key: %s", update)
		}
		changes := update.Lookup("u").Document()
		fields := changes.Lookup("$set").Document()
		if fields.Lookup("tenantId").StringValue() != user.TenantID || fields.Lookup("username").StringValue() != user.Username || fields.Lookup("sessionId").Type != 0 {
			mt.Fatalf("wrong activity fields: %s", fields)
		}
		expiry := changes.Lookup("$max").Document().Lookup("expiresAt").Time()
		if expiry.Before(before.Add(-time.Millisecond)) || expiry.After(after.Add(time.Millisecond)) {
			mt.Fatalf("unexpected expiry %v", expiry)
		}
	})
	for _, tc := range []struct {
		name string
		docs []bson.D
		want error
	}{
		{"unlinked", nil, mongo.ErrNoDocuments},
		{"denied", []bson.D{{{Key: "username", Value: "user"}, {Key: "tenantId", Value: "account-a"}, {Key: "role", Value: "disabled"}}}, errAuth0AccountDenied},
	} {
		mt.Run(tc.name, func(mt *mtest.T) {
			oldClient, oldDB := database.Client, database.Database
			database.Client, database.Database = mt.Client, mt.DB.Name()
			defer func() { database.Client, database.Database = oldClient, oldDB }()
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".users", mtest.FirstBatch, tc.docs...))
			_, err := findMobileAccount(context.Background(), mobileauth.Identity{Issuer: "https://issuer.example/", Subject: "subject"})
			if !errors.Is(err, tc.want) || len(mt.GetAllStartedEvents()) != 1 {
				mt.Fatalf("err=%v commands=%d", err, len(mt.GetAllStartedEvents()))
			}
		})
	}
}

func TestWebSessionStillUsesCookieAndExpiry(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("web sign-in", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		mt.AddMockResponses(mtest.CreateSuccessResponse())
		w := httptest.NewRecorder()
		before := time.Now().Add(sessionDuration)
		err := createWebSession(w, httptest.NewRequest(http.MethodPost, "/login", nil), model.User{Username: "web-user", TenantID: "web-account", Role: "user"}, true)
		after := time.Now().Add(sessionDuration)
		if err != nil {
			mt.Fatal(err)
		}
		cookies := w.Result().Cookies()
		doc := mt.GetStartedEvent().Command.Lookup("documents").Array().Index(0).Value().Document()
		expiry := doc.Lookup("expiresAt").Time()
		if len(cookies) != 1 || cookies[0].Name != "rl_session" || doc.Lookup("sessionId").StringValue() != cookies[0].Value ||
			doc.Lookup("tenantId").StringValue() != "web-account" || expiry.Before(before.Add(-time.Millisecond)) || expiry.After(after.Add(time.Millisecond)) {
			mt.Fatalf("unexpected web session: %s cookies=%v", doc, cookies)
		}
	})
}
