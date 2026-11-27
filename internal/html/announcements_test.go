package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ref-ledger-v2/internal/database"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type memAnnouncementStore struct {
	mu   sync.Mutex
	docs map[string]announcement
}

func (m *memAnnouncementStore) list(context.Context) ([]announcement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []announcement
	for _, a := range m.docs {
		if !a.Cleared {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memAnnouncementStore) get(_ context.Context, id string) (announcement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.docs[id]
	if !ok {
		return a, errAnnouncementNotFound
	}
	return a, nil
}

func (m *memAnnouncementStore) save(_ context.Context, a announcement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.Version > 1 && m.docs[a.ID].Version != a.Version-1 {
		return errAnnouncementConflict
	}
	m.docs[a.ID] = a
	return nil
}

type memDirectory struct {
	users    map[string]announcementUser
	userIDs  map[string]announcementUser
	sessions map[string]announcementSession
}

func (d *memDirectory) userByID(_ context.Context, tenantID, userID string) (announcementUser, error) {
	if u, ok := d.userIDs[tenantID+"\x00"+userID]; ok {
		return u, nil
	}
	return announcementUser{}, errAnnouncementUserUnknown
}

func (d *memDirectory) resolveUser(_ context.Context, username string) (announcementUser, error) {
	if u, ok := d.users[strings.ToLower(username)]; ok {
		return u, nil
	}
	return announcementUser{}, errAnnouncementUserUnknown
}

func (d *memDirectory) sessionByTarget(_ context.Context, id string) (announcementSession, error) {
	if s, ok := d.sessions[id]; ok {
		return s, nil
	}
	return announcementSession{}, errAnnouncementSessionBad
}

func (d *memDirectory) sessionsForUser(_ context.Context, tenant, username string) ([]announcementSession, error) {
	out := []announcementSession{}
	for _, s := range d.sessions {
		if s.TenantID == tenant && s.Username == username {
			out = append(out, s)
		}
	}
	return out, nil
}

var annClock sync.RWMutex

func setAnnNow(p *time.Time, v time.Time) { annClock.Lock(); *p = v; annClock.Unlock() }

var (
	annNow    = time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	annAlice  = announcementUser{TenantID: "t1", UserID: "u-alice", Username: "alice@example.test"}
	annBob    = announcementUser{TenantID: "t1", UserID: "u-bob", Username: "bob@example.test"}
	annCarol  = announcementUser{TenantID: "t2", UserID: "u-carol", Username: "carol@example.test"}
	annLaptop = announcementRecipient{TenantID: "t1", UserID: "u-alice", Username: annAlice.Username, TargetID: "target-laptop"}
	annPhone  = announcementRecipient{TenantID: "t1", UserID: "u-alice", Username: annAlice.Username, TargetID: "target-phone"}
)

func newAnnouncementTestService() (*announcementService, *time.Time) {
	now := annNow
	svc := &announcementService{
		store: &memAnnouncementStore{docs: map[string]announcement{}},
		dir: &memDirectory{
			users: map[string]announcementUser{annAlice.Username: annAlice, annBob.Username: annBob, annCarol.Username: annCarol},
			userIDs: map[string]announcementUser{
				"t1\x00u-alice": annAlice,
				"t1\x00u-bob":   annBob,
				"t2\x00u-carol": annCarol,
			},
			sessions: map[string]announcementSession{
				"target-laptop": {TargetID: "target-laptop", TenantID: "t1", UserID: "u-alice", Username: annAlice.Username, Platform: "web", ExpiresAt: annNow.Add(time.Hour)},
				"target-phone":  {TargetID: "target-phone", TenantID: "t1", UserID: "u-alice", Username: annAlice.Username, Platform: "android", ExpiresAt: annNow.Add(time.Hour)},
				"target-old":    {TargetID: "target-old", TenantID: "t1", UserID: "u-alice", Username: annAlice.Username, ExpiresAt: annNow.Add(-time.Minute)},
			},
		},
	}
	svc.now = func() time.Time { annClock.RLock(); defer annClock.RUnlock(); return now }
	return svc, &now
}

// monitorCall plays the role of Monitor #19 calling the management API.
func monitorCall(t *testing.T, svc *announcementService, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, announcementAdminPrefix+path, &buf)
	req.Header.Set("Authorization", "******")
	rec := httptest.NewRecorder()
	svc.adminHandler(func(r *http.Request) bool { return r.Header.Get("Authorization") == "******" },
		func(*http.Request) string { return "monitor-test" })(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func visible(t *testing.T, svc *announcementService, r announcementRecipient) string {
	t.Helper()
	v, err := svc.current(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return v.Message
}

func TestAnnouncementAdminRequiresAuthorization(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	req := httptest.NewRequest(http.MethodGet, announcementAdminPrefix, nil)
	rec := httptest.NewRecorder()
	svc.adminHandler(func(*http.Request) bool { return false }, func(*http.Request) string { return "" })(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAnnouncementAdminDiagnostics(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", token)
	oldClient, oldWriter := database.Client, log.Writer()
	defer func() { database.Client = oldClient; log.SetOutput(oldWriter) }()
	disconnected, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, authorization, host string
		proto                     []string
		tls, client               bool
		path                      string
		status                    int
		diagnostic                string
	}{
		{name: "missing bearer before readiness", host: "example.test", status: 401, diagnostic: "authorization=missing transport=rejected"},
		{name: "wrong bearer", authorization: "******", host: "example.test", proto: []string{"http-secret"}, status: 401, diagnostic: "authorization=rejected transport=rejected"},
		{name: "proxy rejection", authorization: "Bearer " + token, host: "example.test", proto: []string{"http", "https"}, status: 401, diagnostic: `authorization=accepted transport=rejected direct_tls=no forwarded_proto_count=2 forwarded_proto_values=["http","https"]`},
		{name: "comma separated proxy rejection", authorization: "Bearer " + token, host: "example.test", proto: []string{"http, https"}, status: 401, diagnostic: `forwarded_proto_values=["http,https"]`},
		{name: "untrusted proxy contents", authorization: "Bearer " + token, host: "example.test", proto: []string{"credential-secret\n" + token}, status: 401, diagnostic: `forwarded_proto_values=["[redacted]"]`},
		{name: "https readiness", authorization: "Bearer " + token, host: "example.test", proto: []string{" HTTPS "}, status: 503, diagnostic: "database=not_initialized"},
		{name: "direct tls readiness", authorization: "Bearer " + token, host: "example.test", tls: true, status: 503, diagnostic: "database=not_initialized"},
		{name: "loopback readiness", authorization: "Bearer " + token, host: "localhost:8080", status: 503, diagnostic: "database=not_initialized"},
		{name: "ready reaches handler without version", authorization: "Bearer " + token, host: "example.test", proto: []string{"https"}, client: true, path: "/not/available", status: 404},
		{name: "database operation failure", authorization: "Bearer " + token, host: "example.test", proto: []string{"https"}, client: true, status: 500, diagnostic: "database=operation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database.Client = nil
			if tt.client {
				database.Client = disconnected
			}
			var output bytes.Buffer
			log.SetOutput(&output)
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+announcementAdminPrefix+tt.path+"?username=query-secret", strings.NewReader("body-secret"))
			req.Host = tt.host
			req.Header.Set("Authorization", tt.authorization)
			req.Header.Set("Cookie", "rl_session=cookie-secret")
			req.Header.Set("X-Request-ID", "untrusted-correlation-secret")
			req.Header.Set("X-Ref-Ledger-Admin-Actor", "actor-secret")
			for _, value := range tt.proto {
				req.Header.Add("X-Forwarded-Proto", value)
			}
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			rec := httptest.NewRecorder()
			announcementAdminHandler(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%q", rec.Code, tt.status, rec.Body.String())
			}
			id := rec.Header().Get("X-Request-ID")
			if _, err := uuid.Parse(id); err != nil {
				t.Fatalf("invalid generated request ID %q", id)
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security response headers")
			}
			if tt.diagnostic != "" && (!strings.Contains(output.String(), tt.diagnostic) || !strings.Contains(output.String(), "request_id="+id)) {
				t.Fatalf("unexpected diagnostic: %s", output.String())
			}
			for _, secret := range []string{token, "invalid-secret", "http-secret", "credential-secret", "query-secret", "body-secret", "cookie-secret", "untrusted-correlation-secret", "actor-secret"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("diagnostic leaked %q: %s", secret, output.String())
				}
			}
		})
	}
}

func TestSafeAnnouncementForwardedProto(t *testing.T) {
	for _, input := range [][]string{
		{strings.Repeat("x", 10000)},
		{"http, credential-secret"},
		{"http\nhttps"},
		{"httpſ"},
	} {
		formatted, _ := safeAnnouncementForwardedProto(input)
		if formatted != `["[redacted]"]` {
			t.Fatalf("unsafe value was not redacted: %q", formatted)
		}
	}
	formatted, truncated := safeAnnouncementForwardedProto(make([]string, 1000))
	if !truncated || len(formatted) > 256 {
		t.Fatalf("unbounded diagnostic: %q truncated=%t", formatted, truncated)
	}
}

func TestThanksgivingDateOnlyRangeIsInclusiveInConfiguredTimezone(t *testing.T) {
	svc, now := newAnnouncementTestService()
	setAnnNow(now, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	code, out := monitorCall(t, svc, "POST", "", map[string]any{
		"message": "Happy Thanksgiving", "audience": "system",
		"startsAt": "2026-11-26", "endsAt": "2026-11-26", "timezone": "America/New_York"})
	if code != http.StatusCreated {
		t.Fatalf("publish = %d %v", code, out)
	}
	ny, _ := time.LoadLocation("America/New_York")
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 25, 23, 59, 59, 0, ny), ""},
		{time.Date(2026, 11, 26, 0, 0, 0, 0, ny), "Happy Thanksgiving"},
		{time.Date(2026, 11, 26, 23, 59, 59, 0, ny), "Happy Thanksgiving"},
		{time.Date(2026, 11, 27, 0, 0, 0, 0, ny), ""},
	} {
		setAnnNow(now, c.at)
		if got := visible(t, svc, annLaptop); got != c.want {
			t.Fatalf("at %v got %q want %q", c.at, got, c.want)
		}
	}
}

func TestAnnouncementValidation(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	for name, body := range map[string]map[string]any{
		"empty message":      {"message": " ", "audience": "system"},
		"bad audience":       {"message": "x", "audience": "everyone"},
		"date without zone":  {"message": "x", "audience": "system", "endsAt": "2027-01-01"},
		"bad zone":           {"message": "x", "audience": "system", "endsAt": "2027-01-01", "timezone": "Nope/Zone"},
		"end before start":   {"message": "x", "audience": "system", "startsAt": "2027-01-02", "endsAt": "2027-01-01", "timezone": "UTC"},
		"already expired":    {"message": "x", "audience": "system", "endsAt": "2026-11-01", "timezone": "UTC"},
		"user missing name":  {"message": "x", "audience": "user"},
		"control characters": {"message": "bad\x00text", "audience": "system"},
	} {
		if code, _ := monitorCall(t, svc, "POST", "", body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, code)
		}
	}
	if code, _ := monitorCall(t, svc, "POST", "", map[string]any{"message": "x", "audience": "user", "tenantId": "t1", "userId": "unknown", "username": "nobody@example.test"}); code != http.StatusNotFound {
		t.Errorf("unknown user status = %d", code)
	}
}

func TestSessionAudienceReachesOnlyTargetedSession(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	code, out := monitorCall(t, svc, "POST", "", map[string]any{
		"message": "laptop only", "audience": "session", "sessionTargetId": "target-laptop", "username": annAlice.Username})
	if code != http.StatusCreated {
		t.Fatalf("publish = %d %v", code, out)
	}
	if visible(t, svc, annLaptop) != "laptop only" {
		t.Fatal("laptop must receive it")
	}
	sameUserOtherSession := visible(t, svc, annPhone)
	otherUser := visible(t, svc, announcementRecipient{TenantID: "t1", UserID: "u-bob", Username: annBob.Username, TargetID: "target-laptop"})
	otherTenant := visible(t, svc, announcementRecipient{TenantID: "t2", UserID: "u-alice", TargetID: "target-laptop"})
	noSession := visible(t, svc, announcementRecipient{TenantID: "t1", UserID: "u-alice"})
	replacement := visible(t, svc, announcementRecipient{TenantID: "t1", UserID: "u-alice", TargetID: "new-signin"})
	if sameUserOtherSession != "" || otherUser != "" || otherTenant != "" || noSession != "" || replacement != "" {
		t.Fatalf("leak: %q %q %q %q %q", sameUserOtherSession, otherUser, otherTenant, noSession, replacement)
	}
}

func TestSessionPublishRejectsInvalidTargets(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	for name, body := range map[string]map[string]any{
		"unknown":       {"sessionTargetId": "missing"},
		"expired":       {"sessionTargetId": "target-old"},
		"wrong user":    {"sessionTargetId": "target-laptop", "username": annBob.Username},
		"wrong tenant":  {"sessionTargetId": "target-laptop", "tenantId": "t2"},
		"wrong user id": {"sessionTargetId": "target-laptop", "userId": "u-bob"},
	} {
		body["message"], body["audience"] = "x", "session"
		if code, _ := monitorCall(t, svc, "POST", "", body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d", name, code)
		}
	}
	if code, _ := monitorCall(t, svc, "POST", "", map[string]any{"message": "x", "audience": "session"}); code != http.StatusBadRequest {
		t.Errorf("missing target status = %d", code)
	}
}

func TestUserAudienceReachesEverySessionIncludingNewSignIns(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	if code, out := monitorCall(t, svc, "POST", "", map[string]any{"message": "for alice", "audience": "user", "tenantId": "t1", "userId": "u-alice", "username": "ALICE@example.test"}); code != http.StatusCreated {
		t.Fatalf("publish = %d %v", code, out)
	}
	newSignIn := announcementRecipient{TenantID: "t1", UserID: "u-alice", TargetID: "brand-new"}
	for _, r := range []announcementRecipient{annLaptop, annPhone, newSignIn} {
		if visible(t, svc, r) != "for alice" {
			t.Fatalf("%+v did not receive", r)
		}
	}
	for _, r := range []announcementRecipient{
		{TenantID: "t1", UserID: "u-bob", TargetID: "target-laptop"},
		{TenantID: "t2", UserID: "u-alice", TargetID: "target-laptop"},
		{TenantID: "t2", UserID: "u-carol"},
	} {
		if visible(t, svc, r) != "" {
			t.Fatalf("%+v must not receive", r)
		}
	}
}

func TestUserAudienceIsScopedByTenantAndStableUserID(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	otherAccountUser := announcementUser{TenantID: "t2", UserID: "u-alice-t2", Username: annAlice.Username}
	dir := svc.dir.(*memDirectory)
	dir.userIDs["t2\x00u-alice-t2"] = otherAccountUser
	dir.sessions["target-other-tenant"] = announcementSession{
		TargetID: "target-other-tenant", TenantID: "t2", UserID: otherAccountUser.UserID,
		Username: otherAccountUser.Username, ExpiresAt: annNow.Add(time.Hour),
	}

	selected := map[string]any{
		"message": "selected account only", "audience": "user", "tenantId": "t1",
		"userId": "u-alice", "username": annAlice.Username,
	}
	code, out := monitorCall(t, svc, http.MethodPost, "", selected)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d: %v", code, out)
	}
	for _, recipient := range []announcementRecipient{annLaptop, annPhone} {
		if got := visible(t, svc, recipient); got != "selected account only" {
			t.Errorf("selected account session got %q", got)
		}
	}
	if got := visible(t, svc, announcementRecipient{TenantID: "t2", UserID: otherAccountUser.UserID, Username: otherAccountUser.Username, TargetID: "target-other-tenant"}); got != "" {
		t.Errorf("same username in another account received %q", got)
	}

	for name, body := range map[string]map[string]any{
		"tenant mismatch": {"message": "bad", "audience": "user", "tenantId": "t2", "userId": "u-alice"},
		"user mismatch":   {"message": "bad", "audience": "user", "tenantId": "t1", "userId": "u-alice-t2"},
		"name mismatch":   {"message": "bad", "audience": "user", "tenantId": "t1", "userId": "u-alice", "username": annBob.Username},
		"missing IDs":     {"message": "bad", "audience": "user", "username": annAlice.Username},
	} {
		if status, _ := monitorCall(t, svc, http.MethodPost, "", body); status == http.StatusCreated {
			t.Errorf("%s publish must be rejected", name)
		}
	}

	updateTarget := map[string]any{"message": "system baseline", "audience": "system"}
	_, out = monitorCall(t, svc, http.MethodPost, "", updateTarget)
	id := out["id"].(string)
	for name, body := range map[string]map[string]any{
		"tenant mismatch": {"message": "bad update", "audience": "user", "tenantId": "t2", "userId": "u-alice"},
		"user mismatch":   {"message": "bad update", "audience": "user", "tenantId": "t1", "userId": "u-alice-t2"},
		"name mismatch":   {"message": "bad update", "audience": "user", "tenantId": "t1", "userId": "u-alice", "username": annBob.Username},
		"missing IDs":     {"message": "bad update", "audience": "user", "username": annAlice.Username},
	} {
		if status, _ := monitorCall(t, svc, http.MethodPut, "/"+id, body); status == http.StatusOK {
			t.Errorf("%s update must be rejected", name)
		}
	}
}

func TestSystemAudienceUpdateRetargetAndClear(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	_, out := monitorCall(t, svc, "POST", "", map[string]any{"message": "hello", "audience": "system"})
	id := out["id"].(string)
	if visible(t, svc, annLaptop) != "hello" || visible(t, svc, announcementRecipient{TenantID: "t2", UserID: "u-carol"}) != "hello" {
		t.Fatal("system-wide must reach everyone")
	}
	if code, _ := monitorCall(t, svc, "PUT", "/"+id, map[string]any{"message": "hello", "audience": "system", "version": 99}); code != http.StatusConflict {
		t.Fatalf("stale version = %d", code)
	}
	code, out := monitorCall(t, svc, "PUT", "/"+id, map[string]any{"message": "updated", "audience": "user", "tenantId": "t1", "userId": "u-bob", "username": annBob.Username})
	if code != http.StatusOK || out["version"].(float64) != 2 {
		t.Fatalf("update = %d %v", code, out)
	}
	if visible(t, svc, annLaptop) != "" || visible(t, svc, announcementRecipient{TenantID: "t1", UserID: "u-bob"}) != "updated" {
		t.Fatal("retargeting must remove eligibility from alice and grant it to bob")
	}
	code, _ = monitorCall(t, svc, "POST", "/"+id+"/clear", nil)
	if code != http.StatusOK || visible(t, svc, announcementRecipient{TenantID: "t1", UserID: "u-bob"}) != "" {
		t.Fatal("clear must remove for all clients")
	}
	if code, out := monitorCall(t, svc, "GET", "", nil); code != http.StatusOK || len(out["announcements"].([]any)) != 0 {
		t.Fatalf("list after clear = %d %v", code, out)
	}
	if code, _ := monitorCall(t, svc, "PUT", "/"+id, map[string]any{"message": "x", "audience": "system"}); code != http.StatusNotFound {
		t.Fatalf("update after clear = %d", code)
	}
}

func TestPendingAnnouncementStartsAndCancels(t *testing.T) {
	svc, now := newAnnouncementTestService()
	_, out := monitorCall(t, svc, "POST", "", map[string]any{"message": "soon", "audience": "system",
		"startsAt": "2026-11-26T16:00", "endsAt": "2026-11-26T17:00", "timezone": "UTC"})
	id := out["id"].(string)
	if visible(t, svc, annLaptop) != "" {
		t.Fatal("pending must not show")
	}
	setAnnNow(now, annNow.Add(time.Hour+time.Minute))
	if visible(t, svc, annLaptop) != "soon" {
		t.Fatal("must start on schedule")
	}
	setAnnNow(now, annNow.Add(2*time.Hour+time.Minute))
	if visible(t, svc, annLaptop) != "" {
		t.Fatal("must expire")
	}
	setAnnNow(now, annNow)
	_, out = monitorCall(t, svc, "POST", "", map[string]any{"message": "later", "audience": "system", "startsAt": "2026-11-27T00:00", "timezone": "UTC"})
	monitorCall(t, svc, "DELETE", "/"+out["id"].(string), nil)
	setAnnNow(now, annNow.Add(48*time.Hour))
	if visible(t, svc, annLaptop) != "" {
		t.Fatal("cancelled pending must never start")
	}
	_ = id
}

func TestMonitorCanListSessionsForTargeting(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	code, out := monitorCall(t, svc, "GET", "/sessions?username=alice@example.test", nil)
	if code != http.StatusOK || len(out["sessions"].([]any)) != 3 || out["userId"] != "u-alice" {
		t.Fatalf("sessions = %d %v", code, out)
	}
	if strings.Contains(mustJSON(out), "rl_session") {
		t.Fatal("login credentials must never be exposed")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestLongPollReturnsOnPublishAndClear(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	old := announcementPollInterval
	announcementPollInterval = 20 * time.Millisecond
	defer func() { announcementPollInterval = old }()
	handler := svc.currentHandler(func(context.Context) (announcementRecipient, error) { return annLaptop, nil })

	get := func(query string) announcementPayload {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/api/announcements/current"+query, nil))
		var p announcementPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := get(""); p.Announcement != nil {
		t.Fatal("expected none")
	}
	done := make(chan announcementPayload, 1)
	go func() { done <- get("?known=") }()
	time.Sleep(50 * time.Millisecond)
	_, out := monitorCall(t, svc, "POST", "", map[string]any{"message": "live", "audience": "user", "tenantId": "t1", "userId": "u-alice", "username": annAlice.Username})
	var p announcementPayload
	select {
	case p = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not return on publish")
	}
	if p.Announcement == nil || p.Announcement.Message != "live" {
		t.Fatalf("payload = %+v", p)
	}
	go func() { done <- get("?known=" + p.Key) }()
	time.Sleep(50 * time.Millisecond)
	monitorCall(t, svc, "POST", "/"+out["id"].(string)+"/clear", nil)
	select {
	case p = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not return on clear")
	}
	if p.Announcement != nil || p.Key != "" {
		t.Fatalf("expected cleared, got %+v", p)
	}
}

func TestLongPollReportsEndedSession(t *testing.T) {
	svc, _ := newAnnouncementTestService()
	handler := svc.currentHandler(func(context.Context) (announcementRecipient, error) {
		return announcementRecipient{}, errAnnouncementSessionEnd
	})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("X-Ref-Ledger-Session") != "invalid" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestEventStreamDeliversScheduledStartClearAndSessionEnd(t *testing.T) {
	svc, now := newAnnouncementTestService()
	old := announcementPollInterval
	announcementPollInterval = 20 * time.Millisecond
	defer func() { announcementPollInterval = old }()
	var mu sync.Mutex
	revoked := false
	srv := httptest.NewServer(svc.eventsHandler(func(context.Context) (announcementRecipient, error) {
		mu.Lock()
		defer mu.Unlock()
		if revoked {
			return announcementRecipient{}, errAnnouncementSessionEnd
		}
		return annLaptop, nil
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				events <- name + " " + strings.TrimPrefix(line, "data: ")
			}
		}
		close(events)
	}()
	next := func(substr string) {
		t.Helper()
		select {
		case e := <-events:
			if !strings.Contains(e, substr) {
				t.Fatalf("event %q does not contain %q", e, substr)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %q", substr)
		}
	}
	next(`"announcement":null`)
	_, out := monitorCall(t, svc, "POST", "", map[string]any{"message": "scheduled", "audience": "system",
		"startsAt": "2026-11-26T16:00", "timezone": "UTC"})
	setAnnNow(now, annNow.Add(2*time.Hour)) // server time passes the scheduled start
	next("scheduled")
	monitorCall(t, svc, "POST", "/"+out["id"].(string)+"/clear", nil)
	next(`"announcement":null`)
	mu.Lock()
	revoked = true
	mu.Unlock()
	next("session_ended")
}

type memoryAnnouncementDismissals map[string]bool

func (m memoryAnnouncementDismissals) dismissed(_ context.Context, r announcementRecipient, keys []string) (map[string]bool, error) {
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = m[r.TenantID+":"+r.UserID+":"+key]
	}
	return out, nil
}

func (m memoryAnnouncementDismissals) dismiss(_ context.Context, r announcementRecipient, key string) error {
	m[r.TenantID+":"+r.UserID+":"+key] = true
	return nil
}

func TestAnnouncementDismissalBySchedule(t *testing.T) {
	for _, timed := range []bool{false, true} {
		for _, sessions := range [][2]announcementRecipient{{annLaptop, annPhone}, {annPhone, annLaptop}} {
			t.Run(fmt.Sprintf("timed=%t/%s-clears", timed, sessions[0].TargetID), func(t *testing.T) {
				ctx := context.Background()
				service, _ := newAnnouncementTestService()
				service.dismissals = memoryAnnouncementDismissals{}
				request := announcementRequest{Audience: "user", TenantID: annAlice.TenantID, UserID: annAlice.UserID, Message: "Maintenance"}
				if timed {
					request.EndsAt = annNow.Add(time.Hour).Format(time.RFC3339)
					request.Timezone = "UTC"
				}
				announcement, err := service.publish(ctx, request, "monitor")
				if err != nil {
					t.Fatal(err)
				}
				dismiss := func(recipient announcementRecipient) int {
					req := httptest.NewRequest(http.MethodPost, "/api/announcements/dismiss", strings.NewReader(fmt.Sprintf(`{"id":%q}`, announcement.ID)))
					req.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					service.dismissHandler(func(context.Context) (announcementRecipient, error) { return recipient, nil })(response, req)
					return response.Code
				}
				verify := func(recipient announcementRecipient, visible bool) {
					t.Helper()
					for range 2 {
						view, err := service.current(ctx, recipient)
						if err != nil || (view != nil) != visible {
							t.Fatalf("recipient=%+v visible=%t got %v, %v", recipient, visible, view, err)
						}
					}
				}
				unmanaged := sessions[0]
				unmanaged.TargetID = ""
				if status := dismiss(unmanaged); status != http.StatusConflict {
					t.Fatalf("dismiss without session returned %d", status)
				}
				verify(sessions[0], true)
				verify(sessions[1], true)
				if status := dismiss(sessions[0]); status != http.StatusNoContent {
					t.Fatalf("dismiss returned %d", status)
				}
				verify(sessions[0], false)
				verify(sessions[1], true)
				replacement := sessions[0]
				replacement.TargetID = "replacement-session"
				replacement.SignedInAt = annNow.Add(time.Minute)
				verify(replacement, timed)
				if _, err := service.clear(ctx, announcement.ID); err != nil {
					t.Fatal(err)
				}
				verify(sessions[1], false)
			})
		}
	}
}
