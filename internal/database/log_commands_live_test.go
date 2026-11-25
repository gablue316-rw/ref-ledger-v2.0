package database

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestLogCommandsLive(t *testing.T) {
	const uri = "mongodb://127.0.0.1:28179/?replicaSet=loggingVerifyRS&directConnection=true"
	if os.Getenv("STRUCTURED_LOG_LIVE_TEST_URI") != uri {
		t.Skip("set STRUCTURED_LOG_LIVE_TEST_URI to the disposable verification replica set")
	}
	t.Setenv("APP_ENV", "development")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
		t.Fatalf("not the disposable verification replica set: %v", err)
	}
	oldClient, oldDatabase := Client, Database
	Client, Database = client, "refledger_commands_"+uuid.NewString()
	defer func() {
		_ = client.Database(Database).Drop(context.Background())
		Client, Database = oldClient, oldDatabase
	}()
	db := client.Database(Database)
	policy := logCommandPolicy{environment: "development", actors: map[string]bool{"monitor": true}, tenants: map[string]bool{"tenant-a": true}}
	if actual, err := CaptureThreshold(ctx); err != nil || actual != LogLevelInfo {
		t.Fatalf("initial threshold=%d err=%v", actual, err)
	}
	run := func(fields bson.M) string {
		t.Helper()
		id := uuid.NewString()
		fields["_id"], fields["requestId"], fields["version"] = id, id, 1
		fields["workerEnvironment"], fields["environment"], fields["actor"], fields["state"] = "development", "development", "monitor", "pending"
		if _, err := db.Collection("refLedgerLogCommands").InsertOne(ctx, fields); err != nil {
			t.Fatal(err)
		}
		if err := processLogCommand(ctx, id, policy); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, level := range []int{LogLevelDebug, LogLevelInfo} {
		run(bson.M{"type": "set_capture_threshold", "scope": "environment", "threshold": level})
		if actual, err := CaptureThreshold(ctx); err != nil || actual != level {
			t.Fatalf("threshold=%d err=%v", actual, err)
		}
	}
	cutoff := time.Now().UTC().Truncate(time.Millisecond)
	_, err = db.Collection("refLedgerLogs").InsertMany(ctx, []any{
		bson.M{"environment": "development", "tenantId": "tenant-a", "timestamp": cutoff},
		bson.M{"environment": "development", "tenantId": "tenant-a", "timestamp": cutoff.Add(time.Second)},
		bson.M{"environment": "development", "tenantId": "tenant-b", "timestamp": cutoff},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	clear := bson.M{"_id": id, "requestId": id, "version": 1, "workerEnvironment": "development", "environment": "development",
		"actor": "monitor", "state": "pending", "type": "clear_logs", "scope": "tenant",
		"tenantId": "tenant-a", "confirmation": "CLEAR development", "cutoff": cutoff}
	if _, err := db.Collection("refLedgerLogCommands").InsertOne(ctx, clear); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("refLedgerLogCommands").InsertOne(ctx, clear); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("duplicate command accepted: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := processLogCommand(ctx, id, policy); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var result struct {
		Outcome string `bson:"outcome"`
		Deleted int64  `bson:"deletedCount"`
	}
	if err := db.Collection("refLedgerLogCommands").FindOne(ctx, bson.M{"_id": id}).Decode(&result); err != nil || result.Outcome != "success" || result.Deleted != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if count, err := db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{}); err != nil || count != 2 {
		t.Fatalf("remaining logs=%d err=%v", count, err)
	}
	if count, err := db.Collection("refLedgerLogAudit").CountDocuments(ctx, bson.M{"action": "atlas.clear_logs", "outcome": "success"}); err != nil || count != 1 {
		t.Fatalf("clear audits=%d err=%v", count, err)
	}
	if _, err := db.Collection("refLedgerLogs").InsertOne(ctx, bson.M{"environment": "development", "tenantId": "tenant-a", "timestamp": cutoff}); err != nil {
		t.Fatal(err)
	}
	if err := processLogCommand(ctx, id, policy); err != nil {
		t.Fatal(err)
	}
	if count, err := db.Collection("refLedgerLogs").CountDocuments(ctx, bson.M{}); err != nil || count != 3 {
		t.Fatalf("retry removed new logs: count=%d err=%v", count, err)
	}
	rejected := run(bson.M{"type": "clear_logs", "scope": "tenant", "tenantId": "tenant-b", "confirmation": "CLEAR development", "cutoff": cutoff})
	if err := db.Collection("refLedgerLogCommands").FindOne(ctx, bson.M{"_id": rejected}).Decode(&result); err != nil || result.Outcome != "rejected" {
		t.Fatalf("rejected result=%+v err=%v", result, err)
	}
	wrongEnvironment := uuid.NewString()
	if _, err := db.Collection("refLedgerLogCommands").InsertOne(ctx, bson.M{
		"_id": wrongEnvironment, "requestId": wrongEnvironment, "version": 1,
		"workerEnvironment": "development", "environment": "production", "actor": "monitor", "state": "pending",
		"type": "set_capture_threshold", "scope": "environment", "threshold": LogLevelDebug,
	}); err != nil {
		t.Fatal(err)
	}
	if err := processLogCommand(ctx, wrongEnvironment, policy); err != nil {
		t.Fatal(err)
	}
	if err := db.Collection("refLedgerLogCommands").FindOne(ctx, bson.M{"_id": wrongEnvironment}).Decode(&result); err != nil || result.Outcome != "rejected" {
		t.Fatalf("environment result=%+v err=%v", result, err)
	}
	if actual, err := CaptureThreshold(ctx); err != nil || actual != LogLevelInfo {
		t.Fatalf("rejected command changed threshold=%d err=%v", actual, err)
	}
}
