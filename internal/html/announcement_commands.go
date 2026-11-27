package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const announcementCommandCollection = "refLedgerAnnouncementCommands"

type announcementCommand struct {
	Status            string              `bson:"status"`
	AnnouncementIDs   []string            `bson:"announcementIds"`
	ID                string              `bson:"_id"`
	RequestID         string              `bson:"requestId"`
	Version           int                 `bson:"version"`
	WorkerEnvironment string              `bson:"workerEnvironment"`
	Environment       string              `bson:"environment"`
	Actor             string              `bson:"actor"`
	Type              string              `bson:"type"`
	CreatedAt         time.Time           `bson:"createdAt"`
	ExpiresAt         time.Time           `bson:"expiresAt"`
	AnnouncementID    string              `bson:"announcementId"`
	TenantID          string              `bson:"tenantId"`
	UserID            string              `bson:"userId"`
	Username          string              `bson:"username"`
	After             string              `bson:"after"`
	Request           announcementRequest `bson:"request"`
}

type announcementSearchRow struct {
	Announcement announcement `bson:",inline"`
	Status       string       `bson:"status"`
}

type announcementCommandPolicy struct {
	environment string
	actors      map[string]bool
}

var announcementActorPattern = regexp.MustCompile(`^[A-Za-z0-9._@:-]{1,128}$`)

func configuredAnnouncementCommandPolicy() (announcementCommandPolicy, bool, error) {
	var p announcementCommandPolicy
	switch os.Getenv("REF_LEDGER_ANNOUNCEMENT_COMMANDS_ENABLED") {
	case "", "false":
		return p, false, nil
	case "true":
	default:
		return p, false, errors.New("invalid enabled setting")
	}
	p.environment = database.LogEnvironment()
	if p.environment == "invalid" {
		return p, false, errors.New("invalid environment")
	}
	p.actors = map[string]bool{}
	for _, actor := range strings.Split(os.Getenv("REF_LEDGER_ANNOUNCEMENT_COMMAND_ACTORS"), ",") {
		actor = strings.TrimSpace(actor)
		if !announcementActorPattern.MatchString(actor) {
			return p, false, errors.New("invalid actor allowlist")
		}
		p.actors[actor] = true
	}
	return p, true, nil
}

func validateAnnouncementCommand(c announcementCommand, p announcementCommandPolicy, now time.Time) string {
	id, err := uuid.Parse(c.ID)
	if err != nil || id.String() != c.ID || c.RequestID != c.ID {
		return "invalid_request_id"
	}
	if c.Version != 1 {
		return "invalid_version"
	}
	if c.WorkerEnvironment != p.environment || c.Environment != p.environment {
		return "invalid_environment"
	}
	if !p.actors[c.Actor] {
		return "invalid_actor"
	}
	if c.CreatedAt.IsZero() || c.ExpiresAt.IsZero() || c.CreatedAt.After(now.Add(30*time.Second)) ||
		!c.ExpiresAt.After(now) || !c.ExpiresAt.After(c.CreatedAt) || c.ExpiresAt.After(c.CreatedAt.Add(15*time.Minute)) {
		return "expired_or_invalid_lifetime"
	}
	switch c.Type {
	case "list", "history", "search", "accounts", "sessions", "detail", "publish", "edit", "clear", "bulk-clear", "delete":
	default:
		return "invalid_type"
	}
	if c.Type == "detail" || c.Type == "edit" || c.Type == "clear" || c.Type == "delete" {
		id, err := uuid.Parse(c.AnnouncementID)
		if err != nil || id.String() != c.AnnouncementID {
			return "invalid_announcement_id"
		}
	}
	if c.Type == "search" {
		switch c.Status {
		case "all", "active", "scheduled", "cleared", "expired":
		default:
			return "invalid_status"
		}
	}
	if c.Type == "bulk-clear" {
		if len(c.AnnouncementIDs) == 0 || len(c.AnnouncementIDs) > 100 {
			return "invalid_announcement_id"
		}
		seen := make(map[string]bool, len(c.AnnouncementIDs))
		for _, id := range c.AnnouncementIDs {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.String() != id || seen[id] {
				return "invalid_announcement_id"
			}
			seen[id] = true
		}
	}
	if c.Type == "sessions" && (c.TenantID == "" || c.UserID == "") {
		return "invalid_account"
	}
	if c.Type == "edit" && c.Request.Version < 1 {
		return "version_required"
	}
	if (c.Type == "publish" || c.Type == "edit") && strings.EqualFold(strings.TrimSpace(c.Request.Audience), announcementAudienceSession) &&
		(c.Request.TenantID == "" || c.Request.UserID == "") {
		return "invalid_account"
	}
	if c.Type == "publish" || c.Type == "edit" {
		switch c.Request.Audience {
		case announcementAudienceSystem:
			if c.Request.TenantID != "" || c.Request.UserID != "" || c.Request.Username != "" || c.Request.SessionTargetID != "" {
				return "invalid_request"
			}
		case announcementAudienceUser:
			if c.Request.TenantID == "" || c.Request.UserID == "" || c.Request.SessionTargetID != "" {
				return "invalid_request"
			}
		case announcementAudienceSession:
			target, err := uuid.Parse(c.Request.SessionTargetID)
			if err != nil || target.String() != c.Request.SessionTargetID {
				return "invalid_session"
			}
		default:
			return "invalid_request"
		}
	}
	if len(c.Username) > 256 || len(c.TenantID) > 256 || len(c.UserID) > 24 ||
		len(c.Request.TenantID) > 256 || len(c.Request.UserID) > 24 || len(c.Request.Username) > 256 ||
		len(c.Request.SessionTargetID) > 64 || len(c.Request.StartsAt) > 64 || len(c.Request.EndsAt) > 64 ||
		len(c.Request.Timezone) > 128 || len(c.Request.Audience) > 16 {
		return "invalid_request"
	}
	if c.After != "" {
		if c.Type == "list" || c.Type == "history" || c.Type == "search" {
			id, err := uuid.Parse(c.After)
			if err != nil || id.String() != c.After {
				return "invalid_cursor"
			}
		} else if c.Type == "accounts" || c.Type == "sessions" {
			if _, err := primitive.ObjectIDFromHex(c.After); err != nil {
				return "invalid_cursor"
			}
		} else {
			return "invalid_cursor"
		}
	}
	return ""
}

// Inspect the raw BSON before decoding: the decoder normally ignores unknown fields.
func validAnnouncementCommandFields(raw bson.Raw, typ string) bool {
	allowed := map[string]bool{"_id": true, "requestId": true, "version": true, "workerEnvironment": true,
		"environment": true, "actor": true, "type": true, "createdAt": true, "expiresAt": true,
		"state": true, "attempts": true, "leaseId": true, "leaseUntil": true}
	switch typ {
	case "accounts":
		allowed["username"], allowed["after"] = true, true
	case "sessions":
		allowed["tenantId"], allowed["userId"], allowed["after"] = true, true, true
	case "list", "history":
		allowed["after"] = true
	case "search":
		allowed["after"], allowed["status"] = true, true
	case "bulk-clear":
		allowed["announcementIds"] = true
	case "detail", "clear", "delete":
		allowed["announcementId"] = true
	case "publish":
		allowed["request"] = true
	case "edit":
		allowed["announcementId"], allowed["request"] = true, true
	}
	if !onlyAnnouncementFields(raw, allowed) {
		return false
	}
	if typ == "publish" || typ == "edit" {
		v := raw.Lookup("request")
		doc, ok := v.DocumentOK()
		if !ok {
			return false
		}
		fields := map[string]bool{"message": true, "audience": true, "tenantId": true, "userId": true,
			"username": true, "sessionTargetId": true, "startsAt": true, "endsAt": true, "timezone": true}
		if typ == "edit" {
			fields["version"] = true
		}
		return onlyAnnouncementFields(doc, fields)
	}
	return true
}

func onlyAnnouncementFields(raw bson.Raw, allowed map[string]bool) bool {
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, e := range elements {
		if !allowed[e.Key()] || seen[e.Key()] {
			return false
		}
		seen[e.Key()] = true
	}
	return true
}

func runAnnouncementCommandWorker(ctx context.Context) {
	policy, enabled, err := configuredAnnouncementCommandPolicy()
	if err != nil {
		log.Printf("announcement command worker disabled: %v", err)
		return
	}
	if !enabled {
		return
	}
	if database.Client == nil {
		log.Print("announcement command worker disabled: database unavailable")
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := processPendingAnnouncementCommands(ctx, policy); err != nil && ctx.Err() == nil {
			log.Printf("announcement command worker failed: category=%s", database.SafeErrorCategory(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func announcementScalarState(state string) bson.M {
	return bson.M{"state": state, "$expr": bson.M{"$eq": bson.A{bson.M{"$type": "$state"}, "string"}}}
}

func announcementExpiredLease(now time.Time) bson.M {
	return bson.M{"leaseUntil": bson.M{"$lte": now}, "$expr": bson.M{"$eq": bson.A{bson.M{"$type": "$leaseUntil"}, "date"}}}
}

func announcementClaimable(now time.Time) bson.M {
	return bson.M{"$or": bson.A{
		announcementScalarState("pending"),
		bson.M{"$and": bson.A{announcementScalarState("processing"), announcementExpiredLease(now)}},
	}}
}

// An array containing "processing" might belong to a worker whose state was
// corrupted externally. Do not disturb it unless its lease is known to expire.
func announcementMalformedPendingState(now time.Time) bson.M {
	return bson.M{"$and": bson.A{
		bson.M{"state": "pending"},
		bson.M{"$expr": bson.M{"$ne": bson.A{bson.M{"$type": "$state"}, "string"}}},
		bson.M{"$or": bson.A{bson.M{"state": bson.M{"$nin": bson.A{"processing"}}}, announcementExpiredLease(now)}},
	}}
}

func processPendingAnnouncementCommands(parent context.Context, policy announcementCommandPolicy) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	collection := database.Client.Database(database.GetMongoDbName()).Collection(announcementCommandCollection)
	now := time.Now().UTC()
	cur, err := collection.Find(ctx, bson.M{"workerEnvironment": policy.environment, "$or": bson.A{
		announcementClaimable(now), announcementMalformedPendingState(now),
	}},
		options.Find().SetProjection(bson.M{"_id": 1}).SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(25))
	if err != nil {
		return err
	}
	var ids []bson.M
	if err = cur.All(ctx, &ids); err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		commandCtx, done := context.WithTimeout(ctx, 3*time.Second)
		err := processAnnouncementCommand(commandCtx, id["_id"], policy)
		done()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func processAnnouncementCommand(ctx context.Context, id any, policy announcementCommandPolicy) error {
	client := database.Client
	session, err := client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	db := client.Database(database.GetMongoDbName())
	commands := db.Collection(announcementCommandCollection, options.Collection().SetWriteConcern(writeconcern.Majority()))
	lease := uuid.NewString()
	now := time.Now().UTC()
	identity := bson.M{"_id": id, "workerEnvironment": policy.environment}
	badState := bson.M{"$and": bson.A{identity, announcementMalformedPendingState(now)}}
	if _, err := commands.UpdateOne(ctx, badState, bson.M{"$set": bson.M{
		"state": "completed", "outcome": "rejected", "reason": "malformed", "result": nil, "completedAt": now,
	}, "$unset": bson.M{"leaseId": "", "leaseUntil": ""}}); err != nil {
		return err
	}
	eligible := bson.M{"$and": bson.A{identity, announcementClaimable(now)}}
	// Aggregation $type checks the field itself (query $type also matches array
	// elements). Only pending or definitively expired leases may be rejected.
	attemptType := bson.M{"$type": "$attempts"}
	malformed := bson.M{"$and": bson.A{eligible, bson.M{"$expr": bson.M{"$or": bson.A{
		bson.M{"$not": bson.A{bson.M{"$in": bson.A{attemptType, bson.A{"missing", "int", "long"}}}}},
		bson.M{"$cond": bson.A{bson.M{"$in": bson.A{attemptType, bson.A{"int", "long"}}}, bson.M{"$lt": bson.A{"$attempts", 0}}, false}},
		bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$state", "pending"}}, bson.M{"$or": bson.A{
			bson.M{"$ne": bson.A{bson.M{"$type": "$leaseId"}, "missing"}},
			bson.M{"$ne": bson.A{bson.M{"$type": "$leaseUntil"}, "missing"}},
		}}}},
		bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$state", "processing"}}, bson.M{"$or": bson.A{
			bson.M{"$ne": bson.A{bson.M{"$type": "$leaseId"}, "string"}},
			bson.M{"$eq": bson.A{"$leaseId", ""}},
			bson.M{"$eq": bson.A{attemptType, "missing"}},
		}}}},
	}}}}}
	if _, err := commands.UpdateOne(ctx, malformed, bson.M{"$set": bson.M{
		"state": "completed", "outcome": "rejected", "reason": "malformed", "result": nil, "completedAt": now,
	}, "$unset": bson.M{"leaseId": "", "leaseUntil": ""}}); err != nil {
		return err
	}
	// Exhausted leases are terminal even when the previous worker died.
	exhausted := bson.M{"$and": bson.A{eligible, bson.M{"attempts": bson.M{"$gte": 3}}}}
	if _, err := commands.UpdateOne(ctx, exhausted, bson.M{"$set": bson.M{"state": "completed", "outcome": "failed", "reason": "retry_exhausted", "completedAt": now}, "$unset": bson.M{"leaseId": "", "leaseUntil": ""}}); err != nil {
		return err
	}
	filter := bson.M{"$and": bson.A{eligible, bson.M{"$or": bson.A{bson.M{"attempts": bson.M{"$exists": false}}, bson.M{"attempts": bson.M{"$lt": 3}}}}}}
	res, err := commands.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"state": "processing", "leaseId": lease, "leaseUntil": now.Add(30 * time.Second)}, "$inc": bson.M{"attempts": 1}})
	if err != nil || res.MatchedCount == 0 {
		return err
	}
	changed := false
	_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		// WithTransaction may invoke this callback again after an abort.
		changed = false
		filter := bson.M{"_id": id, "workerEnvironment": policy.environment, "state": "processing", "leaseId": lease,
			"$expr": bson.M{"$eq": bson.A{bson.M{"$type": "$state"}, "string"}}}
		raw, err := commands.FindOne(sc, filter).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var command announcementCommand
		reason := "malformed"
		if len(raw) <= 64<<10 && bson.Unmarshal(raw, &command) == nil && validAnnouncementCommandFields(raw, command.Type) {
			reason = validateAnnouncementCommand(command, policy, time.Now().UTC())
		}
		var result any
		if reason == "" {
			result, changed, err = executeAnnouncementCommand(sc, db, command)
			if err != nil {
				reason = announcementCommandRejection(err)
				if reason == "" {
					return nil, err
				}
				changed = false
			}
		}
		outcome := "success"
		if reason != "" {
			outcome = "rejected"
			result = nil
		}
		res, err := commands.UpdateOne(sc, filter,
			bson.M{"$set": bson.M{"state": "completed", "outcome": outcome, "reason": reason, "result": result, "completedAt": time.Now().UTC()}, "$unset": bson.M{"leaseId": "", "leaseUntil": ""}})
		if err != nil {
			return nil, err
		}
		if res.MatchedCount != 1 {
			return nil, errors.New("command claim lost")
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		// If commit succeeded despite a lost response this predicate cannot match.
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = commands.UpdateOne(releaseCtx, bson.M{"_id": id, "state": "processing", "leaseId": lease,
			"$expr": bson.M{"$eq": bson.A{bson.M{"$type": "$state"}, "string"}}}, bson.M{"$set": bson.M{"state": "pending"}, "$unset": bson.M{"leaseId": "", "leaseUntil": ""}})
	}
	if err == nil && changed {
		announcements().hub.notify()
	}
	return err
}

func announcementCommandRejection(err error) string {
	var input announcementInputError
	switch {
	case errors.As(err, &input):
		return "invalid_request"
	case errors.Is(err, errAnnouncementNotFound), errors.Is(err, errAnnouncementUserUnknown):
		return "not_found"
	case errors.Is(err, errAnnouncementSessionBad):
		return "invalid_session"
	case errors.Is(err, errAnnouncementConflict):
		return "conflict"
	default:
		return ""
	}
}

// A write to the selected session makes a concurrent revoke conflict with the
// transaction instead of allowing an old snapshot to authorize a new target.
func fenceAnnouncementSession(ctx context.Context, db *mongo.Database, a announcement) error {
	if a.Audience != announcementAudienceSession {
		return nil
	}
	filter := bson.M{"$and": bson.A{announcementSessionFilter(), bson.M{
		"targetId": a.SessionTargetID, "tenantId": a.TenantID, "username": a.Username,
		"expiresAt": bson.M{"$gt": time.Now().UTC()},
	}}}
	res, err := db.Collection("sessions").UpdateOne(ctx, filter, bson.M{"$inc": bson.M{"announcementValidationVersion": 1}})
	if err != nil {
		return err
	}
	if res.MatchedCount != 1 {
		return errAnnouncementSessionBad
	}
	return nil
}

func executeAnnouncementCommand(ctx context.Context, db *mongo.Database, c announcementCommand) (any, bool, error) {
	s := &announcementService{store: mongoAnnouncementStore{}, dir: mongoAnnouncementDirectory{}}
	switch c.Type {
	case "accounts":
		filter := bson.M{}
		if c.Username != "" {
			filter["username"] = c.Username
		}
		if c.After != "" {
			id, _ := primitive.ObjectIDFromHex(c.After)
			filter["_id"] = bson.M{"$gt": id}
		}
		cur, err := db.Collection("users").Find(ctx, filter, options.Find().
			SetProjection(bson.M{"_id": 1, "tenantId": 1, "username": 1}).
			SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(101))
		if err != nil {
			return nil, false, err
		}
		var docs []struct {
			ID       primitive.ObjectID `bson:"_id"`
			TenantID string             `bson:"tenantId"`
			Username string             `bson:"username"`
		}
		if err := cur.All(ctx, &docs); err != nil {
			return nil, false, err
		}
		truncated := len(docs) > 100
		if truncated {
			docs = docs[:100]
		}
		items := make([]bson.M, 0, len(docs))
		for _, d := range docs {
			if d.TenantID != "" && d.Username != "" {
				items = append(items, bson.M{"tenantId": d.TenantID, "userId": d.ID.Hex(), "username": d.Username})
			}
		}
		next := ""
		if truncated {
			next = docs[len(docs)-1].ID.Hex()
		}
		return bson.M{"accounts": items, "nextCursor": next}, false, nil
	case "sessions":
		u, err := s.dir.userByID(ctx, c.TenantID, c.UserID)
		if err != nil {
			return nil, false, err
		}
		resolved, err := (mongoAnnouncementDirectory{}).userID(ctx, u.TenantID, u.Username)
		if err != nil {
			return nil, false, err
		}
		if resolved != u.UserID {
			return nil, false, errAnnouncementUserUnknown
		}
		filter := bson.M{"$and": bson.A{announcementSessionFilter(), bson.M{"tenantId": u.TenantID, "username": u.Username, "expiresAt": bson.M{"$gt": time.Now().UTC()}}}}
		if c.After != "" {
			id, _ := primitive.ObjectIDFromHex(c.After)
			filter["_id"] = bson.M{"$gt": id}
		}
		cur, err := db.Collection("sessions").Find(ctx, filter, options.Find().
			SetProjection(bson.M{"_id": 1, "targetId": 1, "platform": 1, "createdAt": 1, "expiresAt": 1}).
			SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(101))
		if err != nil {
			return nil, false, err
		}
		var docs []struct {
			ID        primitive.ObjectID `bson:"_id"`
			TargetID  string             `bson:"targetId"`
			Platform  string             `bson:"platform"`
			CreatedAt time.Time          `bson:"createdAt"`
			ExpiresAt time.Time          `bson:"expiresAt"`
		}
		if err := cur.All(ctx, &docs); err != nil {
			return nil, false, err
		}
		truncated := len(docs) > 100
		if truncated {
			docs = docs[:100]
		}
		items := make([]bson.M, 0, len(docs))
		for _, d := range docs {
			if d.TargetID == "" {
				d.TargetID = uuid.NewString()
				res, err := db.Collection("sessions").UpdateOne(ctx, bson.M{"_id": d.ID, "$or": bson.A{bson.M{"targetId": bson.M{"$exists": false}}, bson.M{"targetId": ""}}}, bson.M{"$set": bson.M{"targetId": d.TargetID}})
				if err != nil {
					return nil, false, err
				}
				if res.MatchedCount != 1 {
					return nil, false, errors.New("session changed concurrently")
				}
			}
			platform := d.Platform
			if platform != "android" && platform != "web" {
				platform = "web"
			}
			items = append(items, bson.M{"sessionTargetId": d.TargetID, "tenantId": u.TenantID, "userId": u.UserID, "username": u.Username, "platform": platform, "createdAt": d.CreatedAt, "expiresAt": d.ExpiresAt})
		}
		next := ""
		if truncated {
			next = docs[len(docs)-1].ID.Hex()
		}
		return bson.M{"sessions": items, "nextCursor": next}, false, nil
	case "list", "history", "search":
		now := s.clock()
		filter := bson.M{"cleared": false, "$or": bson.A{bson.M{"endsAt": bson.M{"$exists": false}}, bson.M{"endsAt": nil}, bson.M{"endsAt": bson.M{"$gt": now}}}}
		if c.Type == "history" {
			filter = inactiveAnnouncementFilter(now)
		} else if c.Type == "search" {
			filter = announcementSearchFilter(c.Status, now)
		}
		if c.After != "" {
			filter["_id"] = bson.M{"$gt": c.After}
		}
		cur, err := db.Collection("announcements").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(101))
		if err != nil {
			return nil, false, err
		}
		var items []announcement
		if err := cur.All(ctx, &items); err != nil {
			return nil, false, err
		}
		truncated := len(items) > 100
		if truncated {
			items = items[:100]
		}
		if items == nil {
			items = []announcement{}
		}
		next := ""
		if truncated {
			next = items[len(items)-1].ID
		}
		if c.Type == "search" {
			rows := make([]announcementSearchRow, 0, len(items))
			for _, item := range items {
				rows = append(rows, announcementSearchRow{Announcement: item, Status: announcementStatus(item, now)})
			}
			return bson.M{"announcements": rows, "nextCursor": next}, false, nil
		}
		return bson.M{"announcements": items, "nextCursor": next}, false, nil
	case "detail":
		a, err := s.store.get(ctx, c.AnnouncementID)
		return a, false, err
	case "publish":
		now := s.clock()
		a, err := s.build(ctx, c.Request, announcement{ID: c.ID, CreatedBy: c.Actor, CreatedAt: now})
		if err != nil {
			return nil, false, err
		}
		if err := fenceAnnouncementSession(ctx, db, a); err != nil {
			return nil, false, err
		}
		a.Version, a.UpdatedAt = 1, now
		if err := s.store.save(ctx, a); err != nil {
			return nil, false, err
		}
		return a, true, nil
	case "bulk-clear":
		now := s.clock()
		results := make([]bson.M, 0, len(c.AnnouncementIDs))
		changed := false
		for _, id := range c.AnnouncementIDs {
			filter := announcementSearchFilter("open", now)
			filter["_id"] = id
			result, err := db.Collection("announcements").UpdateOne(ctx, filter, bson.M{
				"$set": bson.M{"cleared": true, "clearedAt": now, "updatedAt": now},
				"$inc": bson.M{"version": 1},
			})
			if err != nil {
				return nil, false, err
			}
			outcome := "skipped"
			if result.ModifiedCount == 1 {
				outcome = "cleared"
				changed = true
			}
			results = append(results, bson.M{"id": id, "outcome": outcome})
		}
		return bson.M{"results": results}, changed, nil
	case "delete":
		filter := inactiveAnnouncementFilter(s.clock())
		filter["_id"] = c.AnnouncementID
		result, err := db.Collection("announcements").DeleteOne(ctx, filter)
		if err != nil {
			return nil, false, err
		}
		if result.DeletedCount == 0 {
			return nil, false, errAnnouncementNotFound
		}
		return bson.M{"_id": c.AnnouncementID}, true, nil
	case "edit", "clear":
		a, err := s.store.get(ctx, c.AnnouncementID)
		if err != nil {
			return nil, false, err
		}
		if c.Type == "clear" && a.Cleared {
			return a, false, nil
		}
		now := s.clock()
		if c.Type == "edit" {
			if a.Cleared || (a.EndsAt != nil && !now.Before(*a.EndsAt)) {
				return nil, false, errAnnouncementNotFound
			}
			if c.Request.Version != a.Version {
				return nil, false, errAnnouncementConflict
			}
			a, err = s.build(ctx, c.Request, a)
			if err != nil {
				return nil, false, err
			}
			if err := fenceAnnouncementSession(ctx, db, a); err != nil {
				return nil, false, err
			}
		} else {
			a.Cleared, a.ClearedAt = true, &now
		}
		a.Version++
		a.UpdatedAt = now
		if err := s.store.save(ctx, a); err != nil {
			return nil, false, err
		}
		return a, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported command")
	}
}

func inactiveAnnouncementFilter(now time.Time) bson.M {
	return bson.M{"$or": bson.A{bson.M{"cleared": true}, bson.M{"endsAt": bson.M{"$lte": now}}}}
}

func announcementStatus(a announcement, now time.Time) string {
	switch {
	case a.Cleared:
		return "cleared"
	case a.EndsAt != nil && !now.Before(*a.EndsAt):
		return "expired"
	case now.Before(a.StartsAt):
		return "scheduled"
	default:
		return "active"
	}
}

func announcementSearchFilter(status string, now time.Time) bson.M {
	switch status {
	case "all":
		return bson.M{}
	case "cleared":
		return bson.M{"cleared": true}
	case "expired":
		return bson.M{"cleared": false, "endsAt": bson.M{"$lte": now}}
	}
	filter := bson.M{"cleared": false, "$or": bson.A{bson.M{"endsAt": nil}, bson.M{"endsAt": bson.M{"$gt": now}}}}
	switch status {
	case "scheduled":
		filter["startsAt"] = bson.M{"$gt": now}
	case "active":
		filter["startsAt"] = bson.M{"$lte": now}
	}
	return filter
}
