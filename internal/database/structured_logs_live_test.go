package database

import (
	"context"
	"errors"
	"os"
	"ref-ledger-v2/internal/model"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// This opt-in test only targets the separately started, loopback-only verification replica set.
func TestStructuredLoggingLiveWrites(t *testing.T) {
	const uri = "mongodb://127.0.0.1:28179/?replicaSet=loggingVerifyRS&directConnection=true"
	if os.Getenv("STRUCTURED_LOG_LIVE_TEST_URI") != uri {
		t.Skip("set STRUCTURED_LOG_LIVE_TEST_URI to the disposable verification replica set")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("REF_LEDGER_LOG_API_TOKEN", "verification-only-logging-token-2026-unique")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var commitAttempts atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "commitTransaction" {
			commitAttempts.Add(1)
		}
	}}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil || hello.SetName != "loggingVerifyRS" {
		t.Fatalf("not the disposable verification replica set: setName=%q error=%v", hello.SetName, err)
	}
	oldClient, oldDatabase := Client, Database
	oldInitialized := structuredLogsInitialized.Load()
	Client, Database = client, "refledger_logging_verification"
	defer func() {
		Client, Database = oldClient, oldDatabase
		structuredLogsInitialized.Store(oldInitialized)
	}()
	if err := InitializeStructuredLogging(ctx); err != nil {
		t.Fatal(err)
	}
	db := client.Database(Database)
	tenant := "live-test-" + primitive.NewObjectID().Hex()
	sport := "verify-" + primitive.NewObjectID().Hex()
	if _, err := db.Collection("roles").InsertOne(ctx, bson.M{
		"officialRoles": bson.A{bson.M{"sport": sport, "roles": bson.A{}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("games").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "association", Value: 1}, {Key: "gameId", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		t.Fatal(err)
	}
	game := model.GameDescriptor{
		Association: "MSO", GameId: "1983", Date: "11/20/2026", Time: "6:00 PM",
		Sport: sport, Level: "Varsity", NumOfGames: "1",
		GameFee: "70.00", TravelPay: "0.00", AssignorFee: "0.00", Status: "Pending", TaxYear: "2026",
	}
	collection := &GameCollection{DB: db, Coll: db.Collection("games")}
	if err := collection.AddWithContext(ctx, tenant, game); err != nil {
		t.Fatal(err)
	}
	if err := collection.AddWithContext(ctx, tenant, game); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("duplicate insert: %v", err)
	}
	var failure StructuredLog
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"environment": "development", "tenantId": tenant, "operation": "game.insert", "event": "database.write.terminated",
	}).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.ErrorCategory == nil || *failure.ErrorCategory != "duplicate_key" || failure.DriverCode == nil ||
		*failure.DriverCode != 11000 || failure.Attributes["failureReason"] != "duplicate key constraint" ||
		strings.Contains(failure.Message, tenant) {
		t.Fatalf("unsafe or incorrect duplicate diagnostic: %+v", failure)
	}
	count, err := db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{
		"transactionId": *failure.TransactionID, "event": "database.write.terminated",
	})
	if err != nil || count != 1 {
		t.Fatalf("duplicate terminal count=%d error=%v", count, err)
	}

	expired, expiredCancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer expiredCancel()
	game.GameId = "1984"
	if err := collection.AddWithContext(expired, tenant, game); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired write: %v", err)
	}
	var timeout StructuredLog
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"environment": "development", "tenantId": tenant, "operation": "game.insert", "errorCategory": "timeout_or_canceled", "event": "database.write.terminated",
	}).Decode(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout.Attributes["failureReason"] != "database operation timed out" {
		t.Fatalf("timeout diagnostic: %+v", timeout)
	}

	game.GameId = "1985"
	const workers = 6
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := &GameCollection{DB: db, Coll: db.Collection("games")}
			results <- local.AddWithContext(ctx, tenant, game)
		}()
	}
	wg.Wait()
	close(results)
	successes, duplicates := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if mongo.IsDuplicateKeyError(err) {
			duplicates++
		} else {
			t.Fatalf("concurrent insert: %v", err)
		}
	}
	if successes != 1 || duplicates != workers-1 {
		t.Fatalf("concurrent inserts: successes=%d duplicates=%d", successes, duplicates)
	}
	starts, err := db.Collection("refLedgerLogs").Distinct(ctx, "transactionId", bson.M{
		"tenantId": tenant, "operation": "game.insert", "event": "database.write.started",
	})
	if err != nil || len(starts) != workers+3 {
		t.Fatalf("insert starts=%d error=%v", len(starts), err)
	}
	for _, transactionID := range starts {
		count, err := db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{
			"transactionId": transactionID, "event": bson.M{"$in": bson.A{"database.write.completed", "database.write.terminated"}},
		})
		if err != nil || count != 1 {
			t.Fatalf("insert transaction %v terminal count=%d error=%v", transactionID, count, err)
		}
	}
	t.Logf("duplicate key and timeout diagnostics verified; concurrent writes: %d success, %d duplicates", successes, duplicates)

	game.GameId = "1986"
	duplicate := game
	duplicate.GameId = "1983"
	InsertGameDocs(ctx, []model.GameDescriptor{game, duplicate}, Database, "games", tenant)
	var partial StructuredLog
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"environment": "development", "tenantId": tenant, "operation": "games.import",
		"event": "database.write.terminated", "outcome": "partial",
	}).Decode(&partial); err != nil {
		t.Fatal(err)
	}
	count, err = db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{
		"transactionId": *partial.TransactionID, "event": "database.write.terminated",
	})
	if err != nil || count != 1 || partial.Attributes["partial"] != true {
		t.Fatalf("partial terminal count=%d record=%+v error=%v", count, partial, err)
	}
	t.Log("partial import and one terminal record verified")

	game.GameId = "1987"
	InsertGameDocs(WithStructuredLogContext(ctx, "", "live-session-secret"), []model.GameDescriptor{game}, Database, "games", tenant)
	var saved struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := db.Collection("games").FindOne(ctx, bson.M{"tenantId": tenant, "gameId": int64(1987)}).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	var single StructuredLog
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"tenantId": tenant, "operation": "games.import", "event": "database.write.completed",
		"mongoId": saved.ID.Hex(),
	}).Decode(&single); err != nil {
		t.Fatal(err)
	}
	if single.SessionID == nil || *single.SessionID == "live-session-secret" || single.TransactionID == nil {
		t.Fatalf("single insert lost correlation: %+v", single)
	}
	count, err = db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{
		"transactionId": *single.TransactionID, "event": "database.write.started", "sessionId": *single.SessionID,
	})
	if err != nil || count != 1 {
		t.Fatalf("single insert start count=%d error=%v", count, err)
	}
	t.Log("single-game success contains the returned MongoDB ID and correlated session")

	failpoint := bson.D{
		{Key: "configureFailPoint", Value: "failCommand"},
		{Key: "mode", Value: bson.M{"times": 1}},
		{Key: "data", Value: bson.M{
			"failCommands": bson.A{"commitTransaction"}, "errorCode": 91,
			"errorLabels": bson.A{"UnknownTransactionCommitResult"},
		}},
	}
	if err := db.Client().Database("admin").RunCommand(ctx, failpoint).Err(); err != nil {
		t.Fatalf("enable disposable retry failpoint: %v", err)
	}
	defer func() {
		offCtx, offCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer offCancel()
		if err := db.Client().Database("admin").RunCommand(offCtx, bson.D{
			{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: "off"},
		}).Err(); err != nil {
			t.Errorf("disable retry failpoint: %v", err)
		}
	}()
	if err := SetGamesCompleted(tenant, "MSO", []int64{1986}); err != nil {
		t.Fatalf("transaction retry: %v", err)
	}
	if commitAttempts.Load() < 2 {
		t.Fatalf("expected commit retry, observed %d commit attempts", commitAttempts.Load())
	}
	var retryStart, retryEnd StructuredLog
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"tenantId": tenant, "operation": "games.mark_completed", "event": "database.write.started",
	}).Decode(&retryStart); err != nil {
		t.Fatal(err)
	}
	if err := db.Collection("refLedgerLogs").FindOne(ctx, bson.M{
		"tenantId": tenant, "operation": "games.mark_completed", "event": "database.write.completed",
	}).Decode(&retryEnd); err != nil {
		t.Fatal(err)
	}
	if retryStart.TransactionID == nil || retryEnd.TransactionID == nil || *retryStart.TransactionID != *retryEnd.TransactionID {
		t.Fatal("transaction retry changed its trace ID")
	}
	count, err = db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{
		"transactionId": *retryStart.TransactionID, "event": bson.M{"$in": bson.A{"database.write.completed", "database.write.terminated"}},
	})
	if err != nil || count != 1 {
		t.Fatalf("retry terminal count=%d error=%v", count, err)
	}
	t.Logf("commit retried %d times with one trace ID and one terminal event", commitAttempts.Load())
}

func TestStructuredLoggingLivePartialBulkWrite(t *testing.T) {
	const uri = "mongodb://127.0.0.1:28179/?replicaSet=loggingVerifyRS&directConnection=true"
	if os.Getenv("STRUCTURED_LOG_LIVE_TEST_URI") != uri {
		t.Skip("set STRUCTURED_LOG_LIVE_TEST_URI to the disposable verification replica set")
	}
	t.Setenv("APP_ENV", "development")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil || hello.SetName != "loggingVerifyRS" {
		t.Fatalf("not the disposable verification replica set: setName=%q error=%v", hello.SetName, err)
	}
	oldClient, oldDatabase, oldInitialized := Client, Database, structuredLogsInitialized.Load()
	Client, Database = client, "refledger_logging_verification"
	defer func() {
		Client, Database = oldClient, oldDatabase
		structuredLogsInitialized.Store(oldInitialized)
	}()
	if err := InitializeStructuredLogging(ctx); err != nil {
		t.Fatal(err)
	}

	db := client.Database(Database)
	coll := db.Collection("bulkVerification")
	tenant := "bulk-test-" + primitive.NewObjectID().Hex()
	if _, err := coll.InsertOne(ctx, bson.M{"tenantId": tenant, "key": "duplicate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "key", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		t.Fatal(err)
	}

	// No application path currently invokes BulkWrite; exercise the trace boundary explicitly.
	trace := NewWriteTrace(ctx, "verification.bulk", coll.Name(), tenant, "", "", nil)
	insertedID := primitive.NewObjectID()
	result, writeErr := coll.BulkWrite(ctx, []mongo.WriteModel{
		mongo.NewInsertOneModel().SetDocument(bson.M{"_id": insertedID, "tenantId": tenant, "key": "new"}),
		mongo.NewInsertOneModel().SetDocument(bson.M{"tenantId": tenant, "key": "duplicate"}),
	}, options.BulkWrite().SetOrdered(true))
	if result == nil || result.InsertedCount != 1 || !mongo.IsDuplicateKeyError(writeErr) {
		t.Fatalf("bulk result=%+v error=%v", result, writeErr)
	}
	var bulkErr mongo.BulkWriteException
	if !errors.As(writeErr, &bulkErr) || len(bulkErr.WriteErrors) != 1 ||
		bulkErr.WriteErrors[0].Index != 1 || bulkErr.WriteErrors[0].Code != 11000 {
		t.Fatalf("expected one duplicate-key error for the second item: %v", writeErr)
	}
	trace.Failure(writeErr, map[string]any{"insertedCount": result.InsertedCount, "partial": true}, "partial")

	if err := coll.FindOne(ctx, bson.M{"_id": insertedID, "tenantId": tenant}).Err(); err != nil {
		t.Fatalf("successful bulk item was not persisted: %v", err)
	}
	var start, diagnostic, terminal StructuredLog
	logs := db.Collection("refLedgerLogs")
	for eventName, target := range map[string]*StructuredLog{
		"database.write.started": &start, "database.write.failed": &diagnostic, "database.write.terminated": &terminal,
	} {
		if err := logs.FindOne(ctx, bson.M{"tenantId": tenant, "operation": "verification.bulk", "event": eventName}).Decode(target); err != nil {
			t.Fatalf("%s: %v", eventName, err)
		}
	}
	if start.TransactionID == nil || diagnostic.TransactionID == nil || terminal.TransactionID == nil ||
		*start.TransactionID != *diagnostic.TransactionID || *start.TransactionID != *terminal.TransactionID {
		t.Fatal("bulk trace events do not share one transaction ID")
	}
	if terminal.Outcome == nil || *terminal.Outcome != "partial" || terminal.Attributes["partial"] != true ||
		terminal.Attributes["insertedCount"] != int64(1) ||
		diagnostic.ErrorCategory == nil || *diagnostic.ErrorCategory != "duplicate_key" ||
		diagnostic.DriverCode == nil || *diagnostic.DriverCode != 11000 {
		t.Fatalf("incorrect partial bulk diagnostic or terminal: diagnostic=%+v terminal=%+v", diagnostic, terminal)
	}
	count, err := logs.CountDocuments(ctx, bson.M{
		"transactionId": *start.TransactionID, "event": bson.M{"$in": bson.A{"database.write.completed", "database.write.terminated"}},
	})
	if err != nil || count != 1 {
		t.Fatalf("bulk terminal count=%d error=%v", count, err)
	}
	t.Logf("bulk inserted=1 duplicate-key failures=1 outcome=partial terminal=1 transactionId=%s", *start.TransactionID)
}
