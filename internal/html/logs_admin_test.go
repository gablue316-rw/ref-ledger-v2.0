package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogsAdminAuthenticationRequiresConfiguredBearerToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", token)
	request := httptest.NewRequest("GET", logsAdminPrefix+"/threshold", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if !authenticateLogsAdminAPI(request) {
		t.Fatal("configured administrator token was rejected")
	}
	request.Header.Set("Authorization", "Bearer wrong")
	if authenticateLogsAdminAPI(request) {
		t.Fatal("invalid administrator token was accepted")
	}
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", "")
	request.Header.Set("Authorization", "Bearer "+token)
	if authenticateLogsAdminAPI(request) {
		t.Fatal("missing server token accepted an administrator request")
	}
}

func TestLogsAdminTransportAndActorValidation(t *testing.T) {
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", strings.Repeat("k", 32))
	request := httptest.NewRequest("GET", "http://localhost"+logsAdminPrefix, nil)
	if !logsAdminTransportAllowed(request) {
		t.Fatal("loopback development transport was rejected")
	}
	request.Host = "logs.example.test"
	if logsAdminTransportAllowed(request) {
		t.Fatal("plain HTTP non-loopback transport was accepted")
	}
	request.Header.Set("X-Forwarded-Proto", "https")
	if !logsAdminTransportAllowed(request) {
		t.Fatal("TLS reverse-proxy transport was rejected")
	}
	actor := safeLogsActor("operator@example.test")
	if actor == "operator@example.test" || !strings.HasPrefix(actor, "admin:") ||
		safeLogsActor("bad\r\nactor") != "monitor-admin" {
		t.Fatal("administrator actor validation did not redact unsafe values")
	}
}

func TestParseLogsQueryValidatesFiltersAndPagination(t *testing.T) {
	request := httptest.NewRequest("GET", logsAdminPrefix+"/records?dateFrom=2026-03-08&dateThrough=2026-03-08&timeFrom=23%3A30&timeThrough=00%3A15&timeZone=America%2FNew_York&level=4&collection=games&tenantId=tenant-a&sessionId=sha256%3Aabc&transactionId=tx-a&userId=user-a&offset=50&limit=25", nil)
	query, err := parseLogsQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if query.Level != 4 || query.Collection != "games" || query.TenantID != "tenant-a" ||
		query.SessionID != "sha256:abc" || query.TransactionID != "tx-a" ||
		query.UserID != "user-a" || query.Offset != 50 || query.Limit != 25 {
		t.Fatalf("filters were not parsed exactly: %+v", query)
	}
	bad := httptest.NewRequest("GET", logsAdminPrefix+"/records?timeAt=25%3A00&timeZone=UTC", nil)
	if _, err := parseLogsQuery(bad); err == nil {
		t.Fatal("invalid time filter was accepted")
	}
	bad = httptest.NewRequest("GET", logsAdminPrefix+"/records?offset=-1", nil)
	if _, err := parseLogsQuery(bad); err == nil {
		t.Fatal("negative offset was accepted")
	}
	if safeLogsActor(strings.Repeat("x", 101)) != "monitor-admin" {
		t.Fatal("overlong actor was not replaced")
	}
}
