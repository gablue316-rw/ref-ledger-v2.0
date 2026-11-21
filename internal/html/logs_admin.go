package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"
)

const logsAdminPrefix = "/api/internal/v1/admin/logs"

const logsAdminTrustedProxyCIDRsEnv = "REF_LEDGER_TRUSTED_PROXY_CIDRS"

const (
	logsAdminCheckAccepted  = "accepted"
	logsAdminCheckRejected  = "rejected"
	logsAdminCheckMissing   = "missing"
	logsAdminCheckIncorrect = "incorrect"
)

func logsAdminAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	authorizationStatus, authorized := logsAdminAuthorization(r)
	versionStatus := logsAdminVersionStatus(r)
	transportAllowed := logsAdminTransportAllowed(r)
	if !authorized || versionStatus != logsAdminCheckAccepted || !transportAllowed {
		logLogsAdminAuthenticationFailure(r, authorizationStatus, versionStatus, transportAllowed)
		http.Error(w, "Administrator service authentication failed.", http.StatusUnauthorized)
		return
	}
	if database.Client == nil || database.Database == "" {
		http.Error(w, "Logging service is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	path := strings.TrimPrefix(r.URL.Path, logsAdminPrefix)
	actor := safeLogsActor(r.Header.Get("X-Ref-Ledger-Admin-Actor"))
	switch {
	case path == "/threshold" && r.Method == http.MethodGet:
		logsThresholdGet(w, r)
	case path == "/threshold" && r.Method == http.MethodPut:
		logsThresholdPut(w, r, actor)
	case path == "/records" && r.Method == http.MethodGet:
		logsRecordsGet(w, r)
	case path == "/export" && r.Method == http.MethodPost:
		logsExport(w, r, actor)
	case path == "/clear" && r.Method == http.MethodPost:
		logsClear(w, r, actor)
	default:
		http.Error(w, "Logging operation is not available.", http.StatusNotFound)
	}
}

func authenticateLogsAdminAPI(r *http.Request) bool {
	_, accepted := logsAdminAuthorization(r)
	return accepted
}

func logsAdminAuthorization(r *http.Request) (string, bool) {
	configured := strings.TrimSpace(os.Getenv("REF_LEDGER_LOG_API_TOKEN"))
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return logsAdminCheckMissing, false
	}
	separator := strings.IndexByte(header, ' ')
	if separator <= 0 || !strings.EqualFold(strings.TrimSpace(header[:separator]), "Bearer") || len(configured) < 32 {
		return logsAdminCheckRejected, false
	}
	token := strings.TrimSpace(header[separator+1:])
	if len(token) != len(configured) {
		return logsAdminCheckRejected, false
	}
	expectedHash := sha256.Sum256([]byte(configured))
	providedHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) == 1 {
		return logsAdminCheckAccepted, true
	}
	return logsAdminCheckRejected, false
}

func logsAdminVersionStatus(r *http.Request) string {
	switch r.Header.Get("X-Ref-Ledger-Logs-Version") {
	case "":
		return logsAdminCheckMissing
	case "1":
		return logsAdminCheckAccepted
	default:
		return logsAdminCheckIncorrect
	}
}

func logLogsAdminAuthenticationFailure(r *http.Request, authorizationStatus, versionStatus string, transportAllowed bool) {
	transportStatus := logsAdminCheckRejected
	if transportAllowed {
		transportStatus = logsAdminCheckAccepted
		log.Printf("administrator service authentication failed: authorization=%s version=%s transport=%s",
			authorizationStatus, versionStatus, transportStatus)
		return
	}

	directTLS := "no"
	if r.TLS != nil {
		directTLS = "yes"
	}
	forwardedProto := "other"
	switch r.Header.Get("X-Forwarded-Proto") {
	case "":
		forwardedProto = logsAdminCheckMissing
	case "https":
		forwardedProto = "https"
	}
	log.Printf("administrator service authentication failed: authorization=%s version=%s transport=%s direct_tls=%s forwarded_proto=%s",
		authorizationStatus, versionStatus, transportStatus, directTLS, forwardedProto)
}

func logsAdminTransportAllowed(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remoteIP := logsAdminRemoteIP(r.RemoteAddr)
	if remoteIP == nil {
		return false
	}
	if remoteIP.IsLoopback() && strings.EqualFold(database.LogEnvironment(), "development") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return false
	}
	trustedProxyCIDRs, err := parseLogsAdminTrustedProxyCIDRs(os.Getenv(logsAdminTrustedProxyCIDRsEnv))
	if err != nil {
		log.Printf("trusted proxy configuration invalid: category=invalid_cidrs")
		return false
	}
	for _, trustedProxyCIDR := range trustedProxyCIDRs {
		if trustedProxyCIDR.Contains(remoteIP) {
			return true
		}
	}
	return false
}

func logsAdminRemoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		remoteAddr = host
	}
	if zoneIndex := strings.LastIndexByte(remoteAddr, '%'); zoneIndex >= 0 {
		remoteAddr = remoteAddr[:zoneIndex]
	}
	return net.ParseIP(strings.Trim(remoteAddr, "[]"))
}

func parseLogsAdminTrustedProxyCIDRs(value string) ([]*net.IPNet, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var trustedProxyCIDRs []*net.IPNet
	for _, value := range strings.Split(value, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("empty trusted proxy CIDR")
		}
		_, trustedProxyCIDR, err := net.ParseCIDR(value)
		if err != nil {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		trustedProxyCIDRs = append(trustedProxyCIDRs, trustedProxyCIDR)
	}
	return trustedProxyCIDRs, nil
}

func safeLogsActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" || len(actor) > 100 {
		return "monitor-admin"
	}
	for _, character := range actor {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			!strings.ContainsRune("@._- ", character) {
			return "monitor-admin"
		}
	}
	key := []byte(os.Getenv("REF_LEDGER_LOG_API_TOKEN"))
	if len(key) < 32 {
		return "monitor-admin"
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(actor))
	return "admin:" + hex.EncodeToString(mac.Sum(nil)[:16])
}

func logsThresholdGet(w http.ResponseWriter, r *http.Request) {
	threshold, err := database.CaptureThreshold(r.Context())
	if err != nil {
		http.Error(w, "Unable to load the capture threshold.", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"threshold": threshold, "levels": []map[string]any{
			{"level": database.LogLevelError, "name": "Error"},
			{"level": database.LogLevelWarning, "name": "Warning"},
			{"level": database.LogLevelInfo, "name": "Info"},
			{"level": database.LogLevelDebug, "name": "Debug"},
		},
		"environment": database.LogEnvironment(),
	})
}

func logsThresholdPut(w http.ResponseWriter, r *http.Request, actor string) {
	var request struct {
		Threshold int `json:"threshold"`
	}
	if !decodeLogsJSON(w, r, &request) {
		return
	}
	if request.Threshold < database.LogLevelError || request.Threshold > database.LogLevelDebug {
		http.Error(w, "Select a capture threshold from Error through Debug.", http.StatusBadRequest)
		return
	}
	if err := database.SetCaptureThreshold(r.Context(), request.Threshold); err != nil {
		_ = database.AddLogAudit(r.Context(), actor, "threshold", "environment:"+database.LogEnvironment(), "failed")
		http.Error(w, "Unable to save the capture threshold.", http.StatusServiceUnavailable)
		return
	}
	if err := database.AddLogAudit(r.Context(), actor, "threshold", "environment:"+database.LogEnvironment(), "success"); err != nil {
		http.Error(w, "Threshold changed, but its audit record could not be saved.", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threshold": request.Threshold, "environment": database.LogEnvironment()})
}

func logsRecordsGet(w http.ResponseWriter, r *http.Request) {
	query, err := parseLogsQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cutoff, err := requestedLogsCutoff(r.URL.Query().Get("cutoff"))
	if err != nil {
		http.Error(w, "Invalid log snapshot cutoff.", http.StatusBadRequest)
		return
	}
	items, hasMore, err := database.QueryStructuredLogs(r.Context(), query, cutoff)
	if err != nil {
		http.Error(w, "Unable to query Ref Ledger logs.", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "hasMore": hasMore, "cutoff": cutoff.Format(time.RFC3339Nano),
		"environment": database.LogEnvironment(), "timeZone": query.TimeZone,
	})
}

func logsExport(w http.ResponseWriter, r *http.Request, actor string) {
	var request struct {
		All     bool              `json:"all"`
		Filters database.LogQuery `json:"filters"`
	}
	if !decodeLogsJSON(w, r, &request) {
		return
	}
	cutoff := logClearCutoff()
	request.Filters.Offset = 0
	request.Filters.Limit = 0
	if _, err := database.BuildStructuredLogFilter(request.Filters, cutoff); !request.All && err != nil {
		http.Error(w, "Invalid log export filters.", http.StatusBadRequest)
		return
	}
	cursor, err := database.OpenStructuredLogExport(r.Context(), request.Filters, cutoff, request.All)
	if err != nil {
		auditLogAction(r.Context(), actor, "export", logsScope(request.All, request.Filters.TenantID), "failed")
		http.Error(w, "Unable to export Ref Ledger logs.", http.StatusServiceUnavailable)
		return
	}
	defer cursor.Close(r.Context())
	expectedCount, err := database.CountStructuredLogs(r.Context(), request.Filters, cutoff, request.All)
	if err != nil {
		auditLogAction(r.Context(), actor, "export", logsScope(request.All, request.Filters.TenantID), "failed")
		http.Error(w, "Unable to count Ref Ledger logs for export.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ref-ledger-logs.jsonl"`)
	w.Header().Set("X-Log-Export-Cutoff", cutoff.Format(time.RFC3339Nano))
	w.Header().Set("X-Log-Export-Scope", logsScope(request.All, request.Filters.TenantID))
	w.Header().Set("X-Log-Export-Count", strconv.FormatInt(expectedCount, 10))
	w.Header().Add("Trailer", "X-Log-Export-Status")
	w.Header().Add("Trailer", "X-Log-Export-Audit")
	encoder := json.NewEncoder(w)
	writeFailed := false
	for cursor.Next(r.Context()) {
		var entry database.StructuredLog
		if err := cursor.Decode(&entry); err != nil {
			writeFailed = true
			break
		}
		if err := encoder.Encode(entry); err != nil {
			writeFailed = true
			break
		}
	}
	if cursor.Err() != nil {
		writeFailed = true
	}
	outcome := "success"
	if writeFailed {
		outcome = "failed"
		w.Header().Set("X-Log-Export-Status", "incomplete")
	} else {
		w.Header().Set("X-Log-Export-Status", "complete")
	}
	if err := database.AddLogAudit(r.Context(), actor, "export", logsScope(request.All, request.Filters.TenantID), outcome); err != nil {
		log.Printf("structured log audit write failed: action=export")
		w.Header().Set("X-Log-Export-Audit", "failed")
	} else {
		w.Header().Set("X-Log-Export-Audit", "recorded")
	}
}

func logsClear(w http.ResponseWriter, r *http.Request, actor string) {
	var request struct {
		Confirmation string `json:"confirmation"`
		TenantID     string `json:"tenantId"`
	}
	if !decodeLogsJSON(w, r, &request) {
		return
	}
	environment := database.LogEnvironment()
	if request.Confirmation != "CLEAR "+environment {
		http.Error(w, "Explicit environment confirmation is required.", http.StatusBadRequest)
		return
	}
	if request.TenantID != "" && (len(request.TenantID) > 200 || strings.ContainsAny(request.TenantID, "\r\n\x00")) {
		http.Error(w, "Invalid tenant scope.", http.StatusBadRequest)
		return
	}
	cutoff := logClearCutoff()
	scope := "environment:" + environment
	if request.TenantID != "" {
		scope += ";tenant:" + request.TenantID
	}
	deleted, err := database.ClearStructuredLogs(r.Context(), cutoff, request.TenantID)
	outcome := "success"
	if err != nil {
		outcome = "failed"
	}
	auditErr := auditLogAction(r.Context(), actor, "clear", scope, outcome)
	if err != nil {
		http.Error(w, "Unable to clear Ref Ledger logs.", http.StatusServiceUnavailable)
		return
	}
	result := map[string]any{
		"deleted": deleted, "cutoff": cutoff.Format(time.RFC3339Nano),
		"environment": environment, "tenantId": nullableLogTenant(request.TenantID),
		"auditRecorded": auditErr == nil,
	}
	if auditErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseLogsQuery(r *http.Request) (database.LogQuery, error) {
	values := r.URL.Query()
	query := database.LogQuery{
		DateFrom: values.Get("dateFrom"), DateThrough: values.Get("dateThrough"),
		TimeFrom: values.Get("timeFrom"), TimeThrough: values.Get("timeThrough"),
		TimeAt: values.Get("timeAt"), TimeZone: values.Get("timeZone"),
		Collection: values.Get("collection"), TenantID: values.Get("tenantId"),
		SessionID: values.Get("sessionId"), TransactionID: values.Get("transactionId"),
		UserID: values.Get("userId"),
	}
	if value := values.Get("level"); value != "" {
		level, err := strconv.Atoi(value)
		if err != nil {
			return query, errors.New("invalid log level")
		}
		query.Level = level
	}
	if value := values.Get("offset"); value != "" {
		offset, err := strconv.ParseInt(value, 10, 64)
		if err != nil || offset < 0 {
			return query, errors.New("invalid log offset")
		}
		query.Offset = offset
	}
	query.Limit = 50
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.ParseInt(value, 10, 64)
		if err != nil || limit < 1 || limit > 200 {
			return query, errors.New("invalid log page size")
		}
		query.Limit = limit
	}
	if _, err := database.BuildStructuredLogFilter(query, time.Now().UTC()); err != nil {
		return query, err
	}
	return query, nil
}

func requestedLogsCutoff(value string) (time.Time, error) {
	if value == "" {
		return time.Now().UTC().Truncate(time.Millisecond).Add(-time.Millisecond), nil
	}
	cutoff, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	if cutoff.After(now) {
		return time.Time{}, errors.New("cutoff is in the future")
	}
	return cutoff.UTC(), nil
}

func logClearCutoff() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond).Add(-time.Millisecond)
}

func logsScope(all bool, tenantID string) string {
	scope := "environment:" + database.LogEnvironment()
	if all {
		scope += ";all-records"
	} else {
		scope += ";filtered"
	}
	if tenantID != "" {
		scope += ";tenant:" + tenantID
	}
	return scope
}

func nullableLogTenant(tenantID string) any {
	if tenantID == "" {
		return nil
	}
	return tenantID
}

func auditLogAction(ctx context.Context, actor, action, scope, outcome string) error {
	if err := database.AddLogAudit(ctx, actor, action, scope, outcome); err != nil {
		log.Printf("structured log audit write failed: action=%s", action)
		return err
	}
	return nil
}

func decodeLogsJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "Invalid JSON request.", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "Provide one JSON object.", http.StatusBadRequest)
		return false
	}
	return true
}
