package database

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	LogLevelError   = 1
	LogLevelWarning = 2
	LogLevelInfo    = 3
	LogLevelDebug   = 4
	logRetention    = 90 * 24 * time.Hour
	logPageMax      = 200
)

var ErrStructuredLogValidation = errors.New("structured log validation failure")

var safeLogValue = regexp.MustCompile(`^[[:alnum:] _.,:@/+()#-]{0,200}$`)
var safeEnvironmentValue = regexp.MustCompile(`^[[:alnum:]_.-]{1,64}$`)
var structuredLogsInitialized atomic.Bool

type StructuredLog struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	SchemaVersion int                `bson:"schemaVersion" json:"schemaVersion"`
	Timestamp     time.Time          `bson:"timestamp" json:"-"`
	Environment   string             `bson:"environment" json:"environment"`
	Level         int                `bson:"level" json:"level"`
	Event         string             `bson:"event" json:"event"`
	Message       string             `bson:"message" json:"message"`
	TenantID      *string            `bson:"tenantId" json:"tenantId"`
	UserID        *string            `bson:"userId" json:"userId"`
	SessionID     *string            `bson:"sessionId" json:"sessionId"`
	TransactionID *string            `bson:"transactionId" json:"transactionId"`
	Collection    *string            `bson:"collection" json:"collection"`
	Operation     *string            `bson:"operation" json:"operation"`
	Outcome       *string            `bson:"outcome" json:"outcome"`
	ElapsedMS     *int64             `bson:"elapsedMs" json:"elapsedMs"`
	MongoID       *string            `bson:"mongoId" json:"mongoId"`
	ErrorCategory *string            `bson:"errorCategory" json:"errorCategory"`
	DriverCode    *int64             `bson:"driverCode" json:"driverCode"`
	Attributes    map[string]any     `bson:"attributes" json:"attributes"`
}

func (entry StructuredLog) MarshalJSON() ([]byte, error) {
	type logJSON StructuredLog
	return json.Marshal(struct {
		logJSON
		Timestamp string `json:"timestamp"`
	}{
		logJSON:   logJSON(entry),
		Timestamp: entry.Timestamp.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"),
	})
}

type LogQuery struct {
	DateFrom      string `json:"dateFrom"`
	DateThrough   string `json:"dateThrough"`
	TimeFrom      string `json:"timeFrom"`
	TimeThrough   string `json:"timeThrough"`
	TimeAt        string `json:"timeAt"`
	TimeZone      string `json:"timeZone"`
	Level         int    `json:"level"`
	Collection    string `json:"collection"`
	TenantID      string `json:"tenantId"`
	SessionID     string `json:"sessionId"`
	TransactionID string `json:"transactionId"`
	UserID        string `json:"userId"`
	Offset        int64  `json:"offset"`
	Limit         int64  `json:"limit"`
}

type WriteTrace struct {
	transactionID string
	tenantID      *string
	userID        *string
	sessionID     *string
	collection    *string
	operation     string
	started       time.Time
	attributes    map[string]any
	terminal      sync.Once
}

type structuredLogContextKey struct{}

var allowedLogAttributes = map[string]bool{
	"association": true, "associationId": true, "siteName": true, "levelId": true, "officialId": true,
	"mongoId": true, "gameId": true, "sport": true, "level": true, "site": true,
	"field": true, "gameCount": true, "status": true, "date": true, "taxYear": true,
	"expenseId": true, "expenseCount": true, "skippedCount": true, "failedCount": true,
	"paymentId": true, "attempt": true, "matchedCount": true,
	"modifiedCount": true, "deletedCount": true, "insertedCount": true, "partial": true,
	"affectedCollections": true, "failureCategory": true, "failureReason": true, "driverCode": true,
	"environment": true, "recordCount": true, "appVersion": true, "release": true, "port": true,
	"databaseName": true, "threshold": true, "maintenance": true,
	"savedValues": true, "changedValues": true,
}

func InitializeStructuredLogging(ctx context.Context) error {
	structuredLogsInitialized.Store(false)
	if Client == nil || Database == "" {
		return errors.New("structured log database is not initialized")
	}
	db := Client.Database(Database)
	_, err := db.Collection("refLedgerLogs").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "environment", Value: 1}, {Key: "timestamp", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "environment", Value: 1}, {Key: "level", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "transactionId", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "collection", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "sessionId", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "timestamp", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(int32(logRetention.Seconds()))},
	})
	if err != nil {
		return fmt.Errorf("initialize structured log indexes: %w", err)
	}
	if _, err := db.Collection("refLedgerLogAudit").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "timestamp", Value: -1}},
		Options: options.Index().SetExpireAfterSeconds(int32((365 * 24 * time.Hour).Seconds())),
	}); err != nil {
		return fmt.Errorf("initialize log audit index: %w", err)
	}
	if _, err := db.Collection("refLedgerLogSettings").UpdateOne(ctx,
		bson.M{"_id": captureSettingID()},
		bson.M{"$setOnInsert": bson.M{"threshold": LogLevelInfo, "updatedAt": time.Now().UTC()}},
		options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("initialize log capture threshold: %w", err)
	}
	structuredLogsInitialized.Store(true)
	return nil
}

func LogEnvironment() string {
	environment := strings.TrimSpace(os.Getenv("APP_ENV"))
	if environment == "" {
		return "development"
	}
	if !safeEnvironmentValue.MatchString(environment) {
		return "invalid"
	}
	return environment
}

func captureSettingID() string {
	return "capture:" + LogEnvironment()
}

func CaptureThreshold(ctx context.Context) (int, error) {
	if Client == nil || Database == "" {
		return 0, errors.New("structured log database is not initialized")
	}
	var settings struct {
		Threshold int `bson:"threshold"`
	}
	err := Client.Database(Database).Collection("refLedgerLogSettings").
		FindOne(ctx, bson.M{"_id": captureSettingID()}).Decode(&settings)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return LogLevelInfo, nil
	}
	if err != nil {
		return 0, err
	}
	if settings.Threshold < LogLevelError || settings.Threshold > LogLevelDebug {
		return 0, errors.New("stored structured log threshold is invalid")
	}
	return settings.Threshold, nil
}

func SetCaptureThreshold(ctx context.Context, threshold int) error {
	if threshold < LogLevelError || threshold > LogLevelDebug {
		return errors.New("threshold must be between 1 and 4")
	}
	if Client == nil || Database == "" {
		return errors.New("structured log database is not initialized")
	}
	_, err := Client.Database(Database).Collection("refLedgerLogSettings").UpdateOne(ctx,
		bson.M{"_id": captureSettingID()},
		bson.M{"$set": bson.M{"threshold": threshold, "updatedAt": time.Now().UTC()}},
		options.Update().SetUpsert(true))
	return err
}

func AddLogAudit(ctx context.Context, actor, action, scope, outcome string) error {
	if Client == nil || Database == "" {
		return errors.New("structured log database is not initialized")
	}
	_, err := Client.Database(Database).Collection("refLedgerLogAudit").InsertOne(ctx, bson.M{
		"timestamp":   time.Now().UTC().Truncate(time.Millisecond),
		"environment": LogEnvironment(),
		"actor":       safeLogText(actor),
		"action":      safeLogText(action),
		"scope":       safeLogScope(scope),
		"outcome":     safeLogText(outcome),
	})
	return err
}

func RecordStructuredLog(level int, event, message string, attributes map[string]any) {
	if level < LogLevelError || level > LogLevelDebug {
		return
	}
	if !structuredLogsInitialized.Load() {
		log.Printf("structured log sink unavailable: event=%s category=not_initialized", safeLogText(event))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	threshold, err := CaptureThreshold(ctx)
	if err != nil {
		log.Printf("structured log threshold unavailable: event=%s category=%s", safeLogText(event), SafeErrorCategory(err))
		return
	}
	if !shouldCapture(level, threshold) {
		return
	}
	entry := StructuredLog{
		SchemaVersion: 1,
		Timestamp:     time.Now().UTC().Truncate(time.Millisecond),
		Environment:   LogEnvironment(),
		Level:         level,
		Event:         safeLogText(event),
		Message:       safeLogText(message),
		Attributes:    cleanLogAttributes(attributes),
	}
	if err := insertStructuredLog(ctx, entry); err != nil {
		log.Printf("structured log sink write failed: event=%s category=%s", entry.Event, SafeErrorCategory(err))
	}
}

func WithStructuredLogContext(ctx context.Context, userID, sessionCorrelation string) context.Context {
	return context.WithValue(ctx, structuredLogContextKey{}, struct {
		userID             string
		sessionCorrelation string
	}{userID: userID, sessionCorrelation: sessionCorrelation})
}

func NewWriteTrace(ctx context.Context, operation, collection, tenantID, userID, sessionCorrelation string, attributes map[string]any) *WriteTrace {
	if identity, ok := ctx.Value(structuredLogContextKey{}).(struct {
		userID             string
		sessionCorrelation string
	}); ok {
		if userID == "" {
			userID = identity.userID
		}
		if sessionCorrelation == "" {
			sessionCorrelation = identity.sessionCorrelation
		}
	}
	trace := &WriteTrace{
		transactionID: uuid.NewString(),
		tenantID:      optionalLogText(tenantID),
		userID:        optionalLogText(userID),
		sessionID:     fingerprintSession(sessionCorrelation),
		collection:    optionalLogText(collection),
		operation:     safeLogText(operation),
		started:       time.Now().UTC(),
		attributes:    cleanLogAttributes(attributes),
	}
	trace.emit(LogLevelInfo, "database.write.started", "Database write started", "started", nil, nil, nil, nil, nil)
	return trace
}

func (trace *WriteTrace) Success(attributes map[string]any, mongoID any) {
	trace.Complete(attributes, mongoID, "success")
}

func (trace *WriteTrace) Complete(attributes map[string]any, mongoID any, outcome string) {
	trace.terminal.Do(func() {
		values := mergeLogAttributes(trace.attributes, attributes)
		id := structuredMongoID(mongoID)
		elapsed := time.Since(trace.started).Milliseconds()
		trace.emit(LogLevelInfo, "database.write.completed", "Database write completed", outcome, &elapsed, id, nil, nil, values)
	})
}

func (trace *WriteTrace) EventWithMongoID(level int, event, message string, attributes map[string]any, mongoID any) {
	trace.emit(level, event, message, "", nil, structuredMongoID(mongoID), nil, nil,
		mergeLogAttributes(trace.attributes, attributes))
}

func (trace *WriteTrace) EventForCollectionWithMongoID(collection string, level int, event, message string, attributes map[string]any, mongoID any) {
	trace.emitCollection(optionalLogText(collection), level, event, message, "", nil,
		structuredMongoID(mongoID), nil, nil, mergeLogAttributes(trace.attributes, attributes))
}

func (trace *WriteTrace) Failure(err error, attributes map[string]any, outcome string) {
	trace.terminal.Do(func() {
		category, driverCode := classifyWriteError(err)
		reason := sanitizedWriteReason(err, category)
		values := mergeLogAttributes(trace.attributes, attributes)
		values["failureCategory"] = category
		values["failureReason"] = reason
		if driverCode != nil {
			values["driverCode"] = *driverCode
		}
		elapsed := time.Since(trace.started).Milliseconds()
		trace.emit(LogLevelError, "database.write.failed", "Database write failed: "+reason, "failed", &elapsed, nil, &category, driverCode, values)
		if outcome == "" || category == "unknown_commit_outcome" {
			outcome = writeFailureOutcome(category)
		}
		trace.emit(LogLevelError, "database.write.terminated", "Database write terminated", outcome, &elapsed, nil, &category, driverCode, mergeLogAttributes(values, map[string]any{"outcome": outcome}))
	})
}

func writeFailureOutcome(category string) string {
	if category == "unknown_commit_outcome" {
		return "unknown"
	}
	return "failed"
}

func (trace *WriteTrace) Event(level int, event, message string, attributes map[string]any) {
	trace.emit(level, event, message, "", nil, nil, nil, nil, mergeLogAttributes(trace.attributes, attributes))
}

func (trace *WriteTrace) EventForCollection(collection string, level int, event, message string, attributes map[string]any) {
	trace.emitCollection(optionalLogText(collection), level, event, message, "", nil, nil, nil, nil,
		mergeLogAttributes(trace.attributes, attributes))
}

func (trace *WriteTrace) DiagnosticForCollection(collection, event, message string, err error, attributes map[string]any) {
	category, driverCode := classifyWriteError(err)
	values := mergeLogAttributes(trace.attributes, attributes)
	values["failureCategory"] = category
	values["failureReason"] = sanitizedWriteReason(err, category)
	if driverCode != nil {
		values["driverCode"] = *driverCode
	}
	trace.emitCollection(optionalLogText(collection), LogLevelError, event, message, "", nil, nil,
		optionalLogText(category), driverCode, values)
}

func (trace *WriteTrace) emit(level int, event, message, outcome string, elapsed *int64, mongoID, category *string, driverCode *int64, attributes map[string]any) {
	trace.emitCollection(trace.collection, level, event, message, outcome, elapsed, mongoID, category, driverCode, attributes)
}

func (trace *WriteTrace) emitCollection(collection *string, level int, event, message, outcome string, elapsed *int64, mongoID, category *string, driverCode *int64, attributes map[string]any) {
	if !structuredLogsInitialized.Load() {
		log.Printf("structured log sink unavailable: event=%s category=not_initialized", safeLogText(event))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	threshold, err := CaptureThreshold(ctx)
	if err != nil {
		log.Printf("structured log threshold unavailable: event=%s category=%s", safeLogText(event), SafeErrorCategory(err))
		return
	}
	if !shouldCapture(level, threshold) {
		return
	}
	record := StructuredLog{
		SchemaVersion: 1, Timestamp: time.Now().UTC().Truncate(time.Millisecond),
		Environment: LogEnvironment(),
		Level:       level, Event: safeLogText(event), Message: safeLogText(message),
		TenantID: trace.tenantID, UserID: trace.userID, SessionID: trace.sessionID,
		TransactionID: optionalLogText(trace.transactionID), Collection: collection,
		Operation: optionalLogText(trace.operation), Outcome: optionalLogText(outcome),
		ElapsedMS: elapsed, MongoID: mongoID, ErrorCategory: category, DriverCode: driverCode,
		Attributes: cleanLogAttributes(attributes),
	}
	if err := insertStructuredLog(ctx, record); err != nil {
		log.Printf("structured log sink write failed: event=%s category=%s", record.Event, SafeErrorCategory(err))
	}
}

func insertStructuredLog(ctx context.Context, entry StructuredLog) error {
	if Client == nil || Database == "" {
		return errors.New("structured log database is not initialized")
	}
	_, err := Client.Database(Database).Collection("refLedgerLogs").InsertOne(ctx, entry)
	return err
}

func QueryStructuredLogs(ctx context.Context, query LogQuery, cutoff time.Time) ([]StructuredLog, bool, error) {
	if query.Limit <= 0 || query.Limit > logPageMax {
		query.Limit = 50
	}
	if query.Offset < 0 {
		return nil, false, errors.New("offset cannot be negative")
	}
	filter, err := BuildStructuredLogFilter(query, cutoff)
	if err != nil {
		return nil, false, err
	}
	cursor, err := Client.Database(Database).Collection("refLedgerLogs").Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}, {Key: "_id", Value: -1}}).
			SetSkip(query.Offset).SetLimit(query.Limit+1))
	if err != nil {
		return nil, false, err
	}
	defer cursor.Close(ctx)
	rows := make([]StructuredLog, 0, query.Limit+1)
	for cursor.Next(ctx) {
		var entry StructuredLog
		if err := cursor.Decode(&entry); err != nil {
			return nil, false, err
		}
		rows = append(rows, entry)
	}
	if err := cursor.Err(); err != nil {
		return nil, false, err
	}
	hasMore := int64(len(rows)) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}
	return rows, hasMore, nil
}

func OpenStructuredLogExport(ctx context.Context, query LogQuery, cutoff time.Time, all bool) (*mongo.Cursor, error) {
	filter := bson.M{"environment": LogEnvironment(), "timestamp": bson.M{"$lte": cutoff}}
	if !all {
		var err error
		filter, err = BuildStructuredLogFilter(query, cutoff)
		if err != nil {
			return nil, err
		}
	}
	return Client.Database(Database).Collection("refLedgerLogs").Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}, {Key: "_id", Value: -1}}))
}

func CountStructuredLogs(ctx context.Context, query LogQuery, cutoff time.Time, all bool) (int64, error) {
	filter := bson.M{"environment": LogEnvironment(), "timestamp": bson.M{"$lte": cutoff}}
	if !all {
		var err error
		filter, err = BuildStructuredLogFilter(query, cutoff)
		if err != nil {
			return 0, err
		}
	}
	return Client.Database(Database).Collection("refLedgerLogs").CountDocuments(ctx, filter)
}

func ClearStructuredLogs(ctx context.Context, cutoff time.Time, tenantID string) (int64, error) {
	filter := bson.M{"environment": LogEnvironment(), "timestamp": bson.M{"$lte": cutoff}}
	if tenantID != "" {
		filter["tenantId"] = tenantID
	}
	result, err := Client.Database(Database).Collection("refLedgerLogs").DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}
	return result.DeletedCount, nil
}

func BuildStructuredLogFilter(query LogQuery, cutoff time.Time) (bson.M, error) {
	filter := bson.M{"environment": LogEnvironment(), "timestamp": bson.M{"$lte": cutoff}}
	if query.Level != 0 {
		if query.Level < LogLevelError || query.Level > LogLevelDebug {
			return nil, errors.New("level must be between 1 and 4")
		}
		filter["level"] = query.Level
	}
	for key, value := range map[string]string{
		"collection": query.Collection, "tenantId": query.TenantID,
		"sessionId": query.SessionID, "transactionId": query.TransactionID, "userId": query.UserID,
	} {
		if value != "" {
			if len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
				return nil, fmt.Errorf("invalid %s filter", key)
			}
			filter[key] = value
		}
	}
	zone := query.TimeZone
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, errors.New("invalid time zone")
	}
	if query.DateFrom != "" || query.DateThrough != "" {
		var start, end time.Time
		if query.DateFrom != "" {
			start, err = time.ParseInLocation("2006-01-02", query.DateFrom, location)
			if err != nil {
				return nil, errors.New("invalid start date")
			}
		}
		if query.DateThrough != "" {
			through, parseErr := time.ParseInLocation("2006-01-02", query.DateThrough, location)
			if parseErr != nil {
				return nil, errors.New("invalid end date")
			}
			if query.DateFrom == "" {
				start = through
			}
			end = through.AddDate(0, 0, 1)
		} else {
			end = start.AddDate(0, 0, 1)
		}
		if !start.IsZero() && !end.IsZero() && !start.Before(end) {
			return nil, errors.New("start date must not follow end date")
		}
		rangeFilter := bson.M{"$lte": cutoff}
		if !start.IsZero() {
			rangeFilter["$gte"] = start.UTC()
		}
		if !end.IsZero() {
			rangeFilter["$lt"] = end.UTC()
		}
		filter["timestamp"] = rangeFilter
	}
	timeExpression := bson.M{"$dateToString": bson.M{"date": "$timestamp", "format": "%H:%M", "timezone": location.String()}}
	if query.TimeAt != "" && (query.TimeFrom != "" || query.TimeThrough != "") {
		return nil, errors.New("choose either a single time or a time range")
	}
	if query.TimeAt != "" {
		if _, err := time.Parse("15:04", query.TimeAt); err != nil {
			return nil, errors.New("invalid time")
		}
		filter["$expr"] = bson.M{"$eq": bson.A{timeExpression, query.TimeAt}}
	}
	if query.TimeFrom != "" || query.TimeThrough != "" {
		for _, value := range []string{query.TimeFrom, query.TimeThrough} {
			if value != "" {
				if _, err := time.Parse("15:04", value); err != nil {
					return nil, errors.New("invalid time range")
				}
			}
		}
		if query.TimeFrom != "" && query.TimeThrough != "" && query.TimeFrom > query.TimeThrough {
			filter["$expr"] = bson.M{"$or": bson.A{
				bson.M{"$gte": bson.A{timeExpression, query.TimeFrom}},
				bson.M{"$lte": bson.A{timeExpression, query.TimeThrough}},
			}}
		} else {
			comparisons := bson.A{}
			if query.TimeFrom != "" {
				comparisons = append(comparisons, bson.M{"$gte": bson.A{timeExpression, query.TimeFrom}})
			}
			if query.TimeThrough != "" {
				comparisons = append(comparisons, bson.M{"$lte": bson.A{timeExpression, query.TimeThrough}})
			}
			if len(comparisons) == 1 {
				filter["$expr"] = comparisons[0]
			} else if len(comparisons) == 2 {
				filter["$expr"] = bson.M{"$and": comparisons}
			}
		}
	}
	return filter, nil
}

func optionalLogText(value string) *string {
	value = safeLogText(value)
	if value == "" {
		return nil
	}
	return &value
}

func structuredMongoID(value any) *string {
	switch typed := value.(type) {
	case primitive.ObjectID:
		return optionalLogText(typed.Hex())
	case string:
		return optionalLogText(typed)
	case nil:
		return nil
	default:
		return optionalLogText(fmt.Sprint(typed))
	}
}

func safeLogText(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if len(value) > 200 || !safeLogValue.MatchString(value) ||
		strings.Contains(lower, "mongodb://") ||
		strings.Contains(lower, "mongodb+srv://") ||
		strings.Contains(lower, "bearer ") {
		return "[redacted]"
	}
	return value
}

func safeLogScope(value string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		parts[i] = safeLogText(part)
		if parts[i] == "" || parts[i] == "[redacted]" {
			return "[redacted]"
		}
	}
	return strings.Join(parts, ";")
}

func fingerprintSession(value string) *string {
	key := []byte(os.Getenv("REF_LEDGER_LOG_API_TOKEN"))
	if strings.TrimSpace(value) == "" || len(key) < 32 {
		return nil
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	fingerprint := "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)[:16])
	return &fingerprint
}

func cleanLogAttributes(attributes map[string]any) map[string]any {
	clean := make(map[string]any)
	for key, value := range attributes {
		if !allowedLogAttributes[key] {
			continue
		}
		switch typed := value.(type) {
		case gameLogValues:
			clean[key] = typed
		case gameLogChanges:
			clean[key] = typed
		case string:
			text := safeLogText(typed)
			if text != "[redacted]" {
				clean[key] = text
			}
		case int:
			clean[key] = typed
		case int64:
			clean[key] = typed
		case float64:
			clean[key] = typed
		case bool:
			clean[key] = typed
		case []string:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				if safe := safeLogText(item); safe != "[redacted]" {
					items = append(items, safe)
				}
			}
			clean[key] = items
		}
	}
	return clean
}

func mergeLogAttributes(first, second map[string]any) map[string]any {
	merged := make(map[string]any, len(first)+len(second))
	for key, value := range first {
		merged[key] = value
	}
	for key, value := range second {
		merged[key] = value
	}
	return cleanLogAttributes(merged)
}

func classifyWriteError(err error) (string, *int64) {
	if err == nil {
		return "database_error", nil
	}
	if mongo.IsDuplicateKeyError(err) {
		code := mongoDriverCode(err)
		if code == nil {
			fallbackCode := int64(11000)
			code = &fallbackCode
		}
		return "duplicate_key", code
	}
	var serverError mongo.ServerError
	if errors.As(err, &serverError) && serverError.HasErrorLabel("UnknownTransactionCommitResult") {
		return "unknown_commit_outcome", nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout_or_canceled", nil
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return "connectivity", nil
	}
	var commandError mongo.CommandError
	if errors.As(err, &commandError) {
		code := int64(commandError.Code)
		return "database_error", &code
	}
	if errors.Is(err, ErrStructuredLogValidation) {
		return "validation", nil
	}
	if errors.Is(err, ErrConflict) {
		return "conflict", nil
	}
	return "database_error", nil
}

func mongoDriverCode(err error) *int64 {
	var writeException mongo.WriteException
	if errors.As(err, &writeException) {
		for _, writeError := range writeException.WriteErrors {
			if writeError.Code != 0 {
				code := int64(writeError.Code)
				return &code
			}
		}
		if writeException.WriteConcernError != nil && writeException.WriteConcernError.Code != 0 {
			code := int64(writeException.WriteConcernError.Code)
			return &code
		}
	}
	var bulkWriteException mongo.BulkWriteException
	if errors.As(err, &bulkWriteException) {
		for _, writeError := range bulkWriteException.WriteErrors {
			if writeError.Code != 0 {
				code := int64(writeError.Code)
				return &code
			}
		}
		if bulkWriteException.WriteConcernError != nil && bulkWriteException.WriteConcernError.Code != 0 {
			code := int64(bulkWriteException.WriteConcernError.Code)
			return &code
		}
	}
	var commandError mongo.CommandError
	if errors.As(err, &commandError) && commandError.Code != 0 {
		code := int64(commandError.Code)
		return &code
	}
	return nil
}

func sanitizedWriteReason(err error, category string) string {
	switch category {
	case "duplicate_key":
		return "duplicate key constraint"
	case "timeout_or_canceled":
		if errors.Is(err, context.DeadlineExceeded) {
			return "database operation timed out"
		}
		return "database operation canceled"
	case "connectivity":
		return "database connectivity failure"
	case "unknown_commit_outcome":
		return "transaction commit outcome is unknown"
	case "validation":
		return "database write validation failed"
	case "conflict":
		return "record changed or no longer exists"
	default:
		return "database write failed"
	}
}

func SafeErrorCategory(err error) string {
	category, _ := classifyWriteError(err)
	return category
}

func shouldCapture(level, threshold int) bool {
	return level >= LogLevelError && level <= LogLevelDebug &&
		threshold >= LogLevelError && threshold <= LogLevelDebug &&
		level <= threshold
}
