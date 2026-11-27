package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ref-ledger-v2/internal/database"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestAnnouncementCommandValidation(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.NewString()
	p := announcementCommandPolicy{environment: "dev", actors: map[string]bool{"monitor": true}}
	c := announcementCommand{ID: id, RequestID: id, Version: 1, Environment: "dev", WorkerEnvironment: "dev", Actor: "monitor", Type: "edit", AnnouncementID: id, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Request: announcementRequest{Version: 1, Audience: "system"}}
	if reason := validateAnnouncementCommand(c, p, now); reason != "" {
		t.Fatal(reason)
	}
	for _, change := range []func(*announcementCommand){
		func(c *announcementCommand) { c.Request.Version = 0 },
		func(c *announcementCommand) { c.Actor = "intruder" },
		func(c *announcementCommand) { c.Environment = "prod" },
		func(c *announcementCommand) { c.ExpiresAt = now },
		func(c *announcementCommand) { c.ExpiresAt = now.Add(16 * time.Minute) },
		func(c *announcementCommand) { c.RequestID = uuid.NewString() },
	} {
		bad := c
		change(&bad)
		if validateAnnouncementCommand(bad, p, now) == "" {
			t.Fatal("accepted invalid command", bad)
		}
	}
	raw, _ := bson.Marshal(bson.M{"type": "publish", "request": bson.M{"message": "hello", "password": "secret"}})
	if validAnnouncementCommandFields(raw, "publish") {
		t.Fatal("accepted unknown request field")
	}
	raw, _ = bson.Marshal(bson.M{"type": "list", "token": "secret"})
	if validAnnouncementCommandFields(raw, "list") {
		t.Fatal("accepted unknown command field")
	}
}

func TestAnnouncementCommandPolicy(t *testing.T) {
	t.Setenv("REF_LEDGER_ANNOUNCEMENT_COMMANDS_ENABLED", "")
	if _, enabled, err := configuredAnnouncementCommandPolicy(); enabled || err != nil {
		t.Fatal(enabled, err)
	}
	t.Setenv("REF_LEDGER_ANNOUNCEMENT_COMMANDS_ENABLED", "true")
	t.Setenv("REF_LEDGER_ANNOUNCEMENT_COMMAND_ACTORS", "")
	if _, enabled, err := configuredAnnouncementCommandPolicy(); enabled || err == nil {
		t.Fatal(enabled, err)
	}
	t.Setenv("REF_LEDGER_ANNOUNCEMENT_COMMAND_ACTORS", "monitor")
	if _, enabled, err := configuredAnnouncementCommandPolicy(); !enabled || err != nil {
		t.Fatal(enabled, err)
	}
}

func TestAnnouncementCommandsLive(t *testing.T) {
	const uri = "mongodb://127.0.0.1:28179/?replicaSet=loggingVerifyRS&directConnection=true"
	if os.Getenv("ANNOUNCEMENT_LIVE_TEST_URI") != uri {
		t.Skip("set ANNOUNCEMENT_LIVE_TEST_URI to the disposable verification replica set")
	}
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
		t.Fatalf("unexpected replica set: %v", err)
	}
	name := "announcement_test_" + uuid.NewString()
	t.Setenv("MONGODB_NAME", name)
	oldClient := database.Client
	database.Client = client
	defer func() { _ = client.Database(name).Drop(context.Background()); database.Client = oldClient }()
	db := client.Database(name)
	cancelled, stop := context.WithCancel(ctx)
	stop()
	_, lookupErr := (mongoAnnouncementDirectory{}).toSession(cancelled, announcementSessionDoc{TenantID: "b", Username: "same"})
	if lookupErr == nil || errors.Is(lookupErr, errAnnouncementSessionBad) {
		t.Fatalf("database error misclassified: %v", lookupErr)
	}
	p := announcementCommandPolicy{environment: "test", actors: map[string]bool{"monitor": true}}
	uidA, uidB := primitive.NewObjectID(), primitive.NewObjectID()
	_, err = db.Collection("users").InsertMany(ctx, []any{
		bson.M{"_id": uidA, "tenantId": "a", "username": "same", "password": "user-secret"},
		bson.M{"_id": uidB, "tenantId": "b", "username": "same", "password": "user-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	web, mobile, old, revoked, marker := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Collection("sessions").InsertMany(ctx, []any{
		bson.M{"tenantId": "b", "username": "same", "targetId": web, "expiresAt": now.Add(time.Hour), "sessionId": "web-secret"},
		bson.M{"tenantId": "b", "username": "same", "expiresAt": now.Add(time.Hour), "sessionId": "legacy-web-secret"},
		bson.M{"_id": bson.D{{Key: "type", Value: "mobileActivity"}, {Key: "tenantId", Value: "b"}, {Key: "username", Value: "same"}}, "tenantId": "b", "username": "same", "targetId": marker, "expiresAt": now.Add(time.Hour)},
		bson.M{"tenantId": "b", "username": "same", "type": "mobile", "targetId": mobile, "createdAt": now, "expiresAt": now.Add(time.Hour), "mobileTokenHash": "mobile-secret"},
		bson.M{"tenantId": "b", "username": "same", "type": "mobile", "targetId": old, "createdAt": now.Add(-time.Hour), "expiresAt": now.Add(time.Hour)},
		bson.M{"tenantId": "b", "username": "same", "targetId": revoked, "expiresAt": now.Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("sessions").DeleteOne(ctx, bson.M{"targetId": revoked}); err != nil {
		t.Fatal(err)
	}
	commands := db.Collection(announcementCommandCollection)
	insert := func(typ string, extra bson.M) string {
		t.Helper()
		id := uuid.NewString()
		doc := bson.M{"_id": id, "requestId": id, "version": 1, "workerEnvironment": "test", "environment": "test", "actor": "monitor", "state": "pending", "type": typ, "createdAt": time.Now(), "expiresAt": time.Now().Add(time.Minute)}
		for k, v := range extra {
			doc[k] = v
		}
		if _, err := commands.InsertOne(ctx, doc); err != nil {
			t.Fatal(err)
		}
		return id
	}
	result := func(id string) bson.M {
		t.Helper()
		var doc bson.M
		if err := commands.FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	run := func(id string) bson.M {
		t.Helper()
		if err := processAnnouncementCommand(ctx, id, p); err != nil {
			t.Fatal(err)
		}
		return result(id)
	}
	accounts := run(insert("accounts", bson.M{"username": "same"}))
	if len(accounts["result"].(bson.M)["accounts"].(bson.A)) != 2 {
		t.Fatal(accounts)
	}
	sessions := run(insert("sessions", bson.M{"tenantId": "b", "userId": uidB.Hex()}))
	if sessions["outcome"] != "success" || len(sessions["result"].(bson.M)["sessions"].(bson.A)) != 3 {
		t.Fatal(sessions)
	}
	directorySessions, err := (mongoAnnouncementDirectory{}).sessionsForUser(ctx, "b", "same")
	if err != nil || len(directorySessions) != 3 {
		t.Fatalf("legacy session discovery: %v %v", directorySessions, err)
	}
	encoded, _ := bson.MarshalExtJSON(sessions["result"], false, false)
	for _, secret := range []string{"web-secret", "legacy-web-secret", "mobile-secret", "user-secret"} {
		if containsBytes(encoded, secret) {
			t.Fatalf("result leaked secret")
		}
	}
	for _, target := range []string{old, revoked, marker, uuid.NewString()} {
		doc := run(insert("publish", bson.M{"request": bson.M{"message": "hello", "audience": "session", "tenantId": "b", "userId": uidB.Hex(), "sessionTargetId": target}}))
		if doc["reason"] != "invalid_session" {
			t.Fatal(doc)
		}
	}
	webID := insert("publish", bson.M{"request": bson.M{"message": "web", "audience": "session", "tenantId": "b", "userId": uidB.Hex(), "sessionTargetId": web}})
	if doc := run(webID); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	svc := &announcementService{store: mongoAnnouncementStore{}, dir: mongoAnnouncementDirectory{}}
	view, err := svc.current(ctx, announcementRecipient{TenantID: "b", UserID: uidB.Hex(), TargetID: web})
	if err != nil || view == nil || view.ID != webID {
		t.Fatalf("web delivery: %v %v", view, err)
	}
	view, err = svc.current(ctx, announcementRecipient{TenantID: "a", UserID: uidA.Hex(), TargetID: web})
	if err != nil || view != nil {
		t.Fatalf("cross-tenant delivery: %v %v", view, err)
	}
	run(insert("clear", bson.M{"announcementId": webID}))
	scheduled := insert("publish", bson.M{"request": bson.M{"message": "later", "audience": "system", "startsAt": now.Add(time.Hour).Format(time.RFC3339), "timezone": "UTC"}})
	if doc := run(scheduled); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	if view, err := svc.current(ctx, announcementRecipient{}); err != nil || view != nil {
		t.Fatalf("scheduled delivery: %v %v", view, err)
	}
	run(insert("clear", bson.M{"announcementId": scheduled}))
	id := insert("publish", bson.M{"request": bson.M{"message": "hello", "audience": "session", "tenantId": "b", "userId": uidB.Hex(), "sessionTargetId": mobile}})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- processAnnouncementCommand(ctx, id, p) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if doc := result(id); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	view, err = svc.current(ctx, announcementRecipient{TenantID: "b", UserID: uidB.Hex(), TargetID: mobile})
	if err != nil || view == nil || view.ID != id {
		t.Fatalf("mobile delivery: %v %v", view, err)
	}
	if err := processAnnouncementCommand(ctx, id, p); err != nil {
		t.Fatal(err)
	}
	count, err := db.Collection("announcements").CountDocuments(ctx, bson.M{"_id": id})
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	edit := func(version int) bson.M {
		return bson.M{"announcementId": id, "request": bson.M{"message": "changed", "audience": "user", "tenantId": "b", "userId": uidB.Hex(), "version": version}}
	}
	if doc := run(insert("edit", edit(1))); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	if doc := run(insert("edit", edit(1))); doc["reason"] != "conflict" {
		t.Fatal(doc)
	}
	if doc := run(insert("detail", bson.M{"announcementId": id})); doc["result"].(bson.M)["version"] != int64(2) {
		t.Fatal(doc)
	}
	if doc := run(insert("clear", bson.M{"announcementId": id})); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	if doc := run(insert("list", bson.M{})); len(doc["result"].(bson.M)["announcements"].(bson.A)) != 0 {
		t.Fatal(doc)
	}
	if doc := run(insert("list", bson.M{"password": "sensitive"})); doc["reason"] != "malformed" || doc["result"] != nil {
		t.Fatal(doc)
	}
	raceTarget := uuid.NewString()
	if _, err := db.Collection("sessions").InsertOne(ctx, bson.M{"tenantId": "b", "username": "same", "targetId": raceTarget, "expiresAt": now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	session, err := client.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(ctx)
	if err := session.StartTransaction(); err != nil {
		t.Fatal(err)
	}
	sc := mongo.NewSessionContext(ctx, session)
	target, err := (mongoAnnouncementDirectory{}).sessionByTarget(sc, raceTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("sessions").DeleteOne(ctx, bson.M{"targetId": raceTarget}); err != nil {
		t.Fatal(err)
	}
	if err := fenceAnnouncementSession(sc, db, announcement{Audience: "session", TenantID: target.TenantID, Username: target.Username, SessionTargetID: target.TargetID}); err == nil {
		t.Fatal("snapshot accepted a concurrently revoked session")
	}
	if err := session.AbortTransaction(ctx); err != nil {
		t.Fatal(err)
	}
	expired := insert("list", bson.M{"createdAt": now.Add(-time.Hour), "expiresAt": now.Add(-time.Minute)})
	if doc := run(expired); doc["reason"] != "expired_or_invalid_lifetime" {
		t.Fatal(doc)
	}
	// A duplicate announcement ID forces transaction aborts and exercises durable retry exhaustion.
	retryID := insert("publish", bson.M{"request": bson.M{"message": "retry", "audience": "system"}})
	if _, err := db.Collection("announcements").InsertOne(ctx, bson.M{"_id": retryID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := processAnnouncementCommand(ctx, retryID, p); err == nil {
			t.Fatal("expected transaction failure")
		}
		if doc := result(retryID); doc["state"] != "pending" || doc["result"] != nil {
			t.Fatal(doc)
		}
	}
	if doc := run(retryID); doc["outcome"] != "failed" || doc["reason"] != "retry_exhausted" {
		t.Fatal(doc)
	}
	// Verify stable cursors expose the rest of a result set.
	var many []any
	for i := 0; i < 105; i++ {
		many = append(many, bson.M{"tenantId": "page", "username": "page"})
	}
	if _, err := db.Collection("users").InsertMany(ctx, many); err != nil {
		t.Fatal(err)
	}
	first := run(insert("accounts", bson.M{"username": "page"}))["result"].(bson.M)
	cursor := first["nextCursor"].(string)
	second := run(insert("accounts", bson.M{"username": "page", "after": cursor}))["result"].(bson.M)
	if len(first["accounts"].(bson.A)) != 100 || len(second["accounts"].(bson.A)) != 5 || second["nextCursor"] != "" {
		t.Fatalf("pagination failed: %v %v", first, second)
	}
	// Malformed worker metadata must not pin the first 25 queue entries.
	batchPolicy := announcementCommandPolicy{environment: "batch-test", actors: map[string]bool{"monitor": true}}
	var batch []any
	for i := 0; i < 26; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		badAttempts := []any{"invalid", nil, bson.A{1}, -1, 0.5}[i%5]
		batch = append(batch, bson.M{"_id": id, "requestId": id, "version": 1, "workerEnvironment": "batch-test",
			"environment": "batch-test", "actor": "monitor", "state": "pending", "type": "list",
			"createdAt": now, "expiresAt": now.Add(time.Minute), "attempts": badAttempts})
	}
	for i := 0; i < 26; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8002-%012x", i)
		batch = append(batch, bson.M{"_id": id, "requestId": id, "version": 1, "workerEnvironment": "batch-test",
			"environment": "batch-test", "actor": "monitor", "state": bson.A{"pending"}, "type": "publish",
			"request": bson.M{"message": "must not publish", "audience": "system"}, "createdAt": now,
			"expiresAt": now.Add(time.Minute), "attempts": "invalid", "leaseId": "malicious", "leaseUntil": now.Add(time.Hour)})
	}
	goodID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	batch = append(batch, bson.M{"_id": goodID, "requestId": goodID, "version": 1, "workerEnvironment": "batch-test",
		"environment": "batch-test", "actor": "monitor", "state": "pending", "type": "list",
		"createdAt": now, "expiresAt": now.Add(time.Minute)})
	activeID := "00000000-0000-4000-8001-000000000001"
	batch = append(batch, bson.M{"_id": activeID, "workerEnvironment": "batch-test", "state": "processing",
		"leaseId": "other-worker", "leaseUntil": now.Add(time.Hour), "attempts": "invalid"})
	expiredID := "00000000-0000-4000-8001-000000000002"
	batch = append(batch, bson.M{"_id": expiredID, "workerEnvironment": "batch-test", "state": "processing",
		"leaseId": "old-worker", "leaseUntil": now.Add(-time.Minute), "attempts": "invalid"})
	pendingLeaseID := "00000000-0000-4000-8001-000000000003"
	batch = append(batch, bson.M{"_id": pendingLeaseID, "workerEnvironment": "batch-test", "state": "pending", "leaseId": "invalid"})
	expiredMissingLeaseID := "00000000-0000-4000-8001-000000000004"
	batch = append(batch, bson.M{"_id": expiredMissingLeaseID, "workerEnvironment": "batch-test", "state": "processing",
		"leaseUntil": now.Add(-time.Minute), "attempts": 1})
	unknownLeaseID := "00000000-0000-4000-8001-000000000005"
	batch = append(batch, bson.M{"_id": unknownLeaseID, "workerEnvironment": "batch-test", "state": "processing",
		"leaseId": "unknown", "leaseUntil": bson.A{now.Add(-time.Minute)}, "attempts": 1})
	activeArrayID := "00000000-0000-4000-8001-000000000006"
	batch = append(batch, bson.M{"_id": activeArrayID, "workerEnvironment": "batch-test", "state": bson.A{"pending", "processing"},
		"leaseId": "other-worker", "leaseUntil": now.Add(time.Hour), "attempts": 1})
	expiredArrayID := "00000000-0000-4000-8001-000000000007"
	batch = append(batch, bson.M{"_id": expiredArrayID, "workerEnvironment": "batch-test", "state": bson.A{"pending", "processing"},
		"leaseId": "old-worker", "leaseUntil": now.Add(-time.Minute), "attempts": 1})
	if _, err := commands.InsertMany(ctx, batch); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := processPendingAnnouncementCommands(ctx, batchPolicy); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 26; i++ {
		doc := result(fmt.Sprintf("00000000-0000-4000-8000-%012x", i))
		if doc["state"] != "completed" || doc["outcome"] != "rejected" || doc["reason"] != "malformed" {
			t.Fatal(doc)
		}
	}
	for i := 0; i < 26; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8002-%012x", i)
		doc := result(id)
		if doc["state"] != "completed" || doc["outcome"] != "rejected" || doc["reason"] != "malformed" {
			t.Fatal(doc)
		}
		count, err := db.Collection("announcements").CountDocuments(ctx, bson.M{"_id": id})
		if err != nil || count != 0 {
			t.Fatalf("malformed state caused publish: count=%d err=%v", count, err)
		}
	}
	if doc := result(goodID); doc["outcome"] != "success" {
		t.Fatal(doc)
	}
	if doc := result(expiredID); doc["outcome"] != "rejected" || doc["reason"] != "malformed" {
		t.Fatal(doc)
	}
	for _, id := range []string{pendingLeaseID, expiredMissingLeaseID, expiredArrayID} {
		if doc := result(id); doc["outcome"] != "rejected" || doc["reason"] != "malformed" {
			t.Fatal(doc)
		}
	}
	if doc := result(activeID); doc["state"] != "processing" || doc["leaseId"] != "other-worker" {
		t.Fatal(doc)
	}
	if doc := result(unknownLeaseID); doc["state"] != "processing" || doc["leaseId"] != "unknown" {
		t.Fatal(doc)
	}
	if doc := result(activeArrayID); doc["leaseId"] != "other-worker" || len(doc["state"].(bson.A)) != 2 {
		t.Fatal(doc)
	}
}

func containsBytes(b []byte, s string) bool { return strings.Contains(string(b), s) }
