package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestStructuredLogThresholdCombinations(t *testing.T) {
	tests := []struct {
		threshold int
		captured  []int
	}{
		{LogLevelError, []int{LogLevelError}},
		{LogLevelWarning, []int{LogLevelError, LogLevelWarning}},
		{LogLevelInfo, []int{LogLevelError, LogLevelWarning, LogLevelInfo}},
		{LogLevelDebug, []int{LogLevelError, LogLevelWarning, LogLevelInfo, LogLevelDebug}},
	}
	for _, test := range tests {
		for level := LogLevelError; level <= LogLevelDebug; level++ {
			got := shouldCapture(level, test.threshold)
			want := false
			for _, captured := range test.captured {
				want = want || captured == level
			}
			if got != want {
				t.Errorf("shouldCapture(%d, %d) = %v, want %v", level, test.threshold, got, want)
			}
		}
	}
	if shouldCapture(0, LogLevelDebug) || shouldCapture(LogLevelInfo, 0) {
		t.Fatal("invalid log levels or thresholds must not be captured")
	}
}

func TestStructuredLogFilterCombinesExactFieldsAndDSTDateBounds(t *testing.T) {
	query := LogQuery{
		DateFrom: "2026-03-08", DateThrough: "2026-03-08", TimeZone: "America/New_York",
		Level: LogLevelDebug, Collection: "games", TenantID: "tenant-1",
		SessionID: "sha256:0123456789abcdef", TransactionID: "tx-1", UserID: "user-1",
	}
	filter, err := BuildStructuredLogFilter(query, time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{
		"level": LogLevelDebug, "collection": "games", "tenantId": "tenant-1",
		"sessionId": "sha256:0123456789abcdef", "transactionId": "tx-1", "userId": "user-1",
	} {
		if filter[key] != expected {
			t.Errorf("%s = %v, want exact value %v", key, filter[key], expected)
		}
	}
	dateRange, ok := filter["timestamp"].(bson.M)
	if !ok {
		t.Fatalf("timestamp filter has type %T", filter["timestamp"])
	}
	start, end := dateRange["$gte"].(time.Time), dateRange["$lt"].(time.Time)
	if !start.Equal(time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)) ||
		!end.Equal(time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("DST day bounds = %s to %s", start, end)
	}
}

func TestStructuredLogSingleDateUsesWholeLocalDay(t *testing.T) {
	filter, err := BuildStructuredLogFilter(LogQuery{
		DateFrom: "2026-03-08", TimeZone: "America/New_York",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	dateRange := filter["timestamp"].(bson.M)
	start, end := dateRange["$gte"].(time.Time), dateRange["$lt"].(time.Time)
	if !start.Equal(time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)) ||
		!end.Equal(time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("single-date bounds = %s to %s", start, end)
	}
}

func TestStructuredLogTimeFiltersSupportMinuteAndMidnightCrossing(t *testing.T) {
	single, err := BuildStructuredLogFilter(LogQuery{TimeAt: "23:59", TimeZone: "UTC"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	singleExpr := single["$expr"].(bson.M)
	if _, ok := singleExpr["$eq"]; !ok {
		t.Fatalf("single selected minute is not an exact minute filter: %v", singleExpr)
	}

	ranged, err := BuildStructuredLogFilter(LogQuery{TimeFrom: "23:30", TimeThrough: "00:15", TimeZone: "UTC"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	rangeExpr := ranged["$expr"].(bson.M)
	if _, ok := rangeExpr["$or"]; !ok {
		t.Fatalf("midnight-crossing time filter must use an OR: %v", rangeExpr)
	}
	if _, err := BuildStructuredLogFilter(LogQuery{TimeAt: "12:00", TimeFrom: "11:00", TimeZone: "UTC"}, time.Now().UTC()); err == nil {
		t.Fatal("single time combined with a time range was accepted")
	}
	if _, err := BuildStructuredLogFilter(LogQuery{TimeAt: "25:00", TimeZone: "UTC"}, time.Now().UTC()); err == nil {
		t.Fatal("invalid time was accepted")
	}
}

func TestStructuredLogTimestampAndSensitiveFieldRedaction(t *testing.T) {
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", strings.Repeat("k", 32))
	rawSession := "raw-secret-session-id"
	entry := StructuredLog{
		SchemaVersion: 1, Timestamp: time.Date(2026, 11, 20, 20, 29, 57, 474999999, time.FixedZone("eastern", -5*60*60)),
		Level: LogLevelInfo, Event: "database.write.completed", Message: "Database write completed",
		SessionID: fingerprintSession(rawSession),
		Attributes: cleanLogAttributes(map[string]any{
			"gameId": 1983, "notes": "private text", "token": "private token",
			"association": "MSO", "paymentId": "payment-1",
		}),
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"timestamp":"2026-11-21T01:29:57.474Z"`) {
		t.Fatalf("timestamp does not have UTC millisecond precision: %s", text)
	}
	if strings.Contains(text, rawSession) || strings.Contains(text, "private text") || strings.Contains(text, "private token") {
		t.Fatalf("sensitive values were serialized: %s", text)
	}
	if !strings.Contains(text, `"gameId":1983`) || !strings.Contains(text, `"association":"MSO"`) {
		t.Fatalf("allowlisted business identifiers were lost: %s", text)
	}
	if fingerprintSession(rawSession) == nil || *fingerprintSession(rawSession) == rawSession {
		t.Fatal("session correlation was not made opaque")
	}
}

func TestLogAuditScopeRetainsSafeScopeAndRejectsUnsafeText(t *testing.T) {
	scope := "environment:development;filtered;tenant:tenant-1"
	if got := safeLogScope(scope); got != scope {
		t.Fatalf("scope = %q, want %q", got, scope)
	}
	if got := safeLogScope("environment:development;tenant:mongodb://user:password@host"); got != "[redacted]" {
		t.Fatalf("unsafe scope = %q", got)
	}
}

func TestStructuredLogDateRangeRejectsReversedDates(t *testing.T) {
	_, err := BuildStructuredLogFilter(LogQuery{
		DateFrom: "2026-03-09", DateThrough: "2026-03-08", TimeZone: "UTC",
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("reversed date range was accepted")
	}
}

func TestStructuredLogFailureClassification(t *testing.T) {
	duplicate := mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11001}}}
	category, code := classifyWriteError(duplicate)
	if category != "duplicate_key" || code == nil || *code != 11001 {
		t.Fatalf("duplicate key classification = %q, %v", category, code)
	}
	category, _ = classifyWriteError(errors.New("safe example"))
	if category != "database_error" {
		t.Fatalf("generic write classification = %q", category)
	}
	category, _ = classifyWriteError(context.DeadlineExceeded)
	if reason := sanitizedWriteReason(context.DeadlineExceeded, category); reason != "database operation timed out" {
		t.Fatalf("timeout reason = %q", reason)
	}
	secretError := errors.New("mongodb+srv://user:password@example.test/private database error")
	if reason := sanitizedWriteReason(secretError, "database_error"); strings.Contains(reason, "password") || strings.Contains(reason, secretError.Error()) {
		t.Fatalf("sanitized error reason leaked underlying text: %q", reason)
	}
}
