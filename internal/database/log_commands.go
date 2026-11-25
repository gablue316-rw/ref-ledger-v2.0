package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type logCommand struct {
	ID                string    `bson:"_id"`
	RequestID         string    `bson:"requestId"`
	Version           int       `bson:"version"`
	WorkerEnvironment string    `bson:"workerEnvironment"`
	Environment       string    `bson:"environment"`
	Type              string    `bson:"type"`
	Actor             string    `bson:"actor"`
	Scope             string    `bson:"scope"`
	TenantID          string    `bson:"tenantId"`
	Threshold         int       `bson:"threshold"`
	Confirmation      string    `bson:"confirmation"`
	Cutoff            time.Time `bson:"cutoff"`
}

type logCommandPolicy struct {
	environment     string
	actors          map[string]bool
	tenants         map[string]bool
	allowAllTenants bool
}

func commandAllowlist(value string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" || safeLogText(entry) != entry {
			return nil, errors.New("invalid or empty command allowlist")
		}
		result[entry] = true
	}
	return result, nil
}

func configuredLogCommandPolicy() (logCommandPolicy, bool, error) {
	var policy logCommandPolicy
	switch os.Getenv("REF_LEDGER_LOG_COMMANDS_ENABLED") {
	case "", "false":
		return policy, false, nil
	case "true":
	default:
		return policy, false, errors.New("invalid command worker setting")
	}
	policy.environment = LogEnvironment()
	if policy.environment == "invalid" {
		return policy, false, errors.New("invalid environment")
	}
	var err error
	if policy.actors, err = commandAllowlist(os.Getenv("REF_LEDGER_LOG_COMMAND_ACTORS")); err != nil {
		return policy, false, err
	}
	if policy.tenants, err = commandAllowlist(os.Getenv("REF_LEDGER_LOG_COMMAND_TENANTS")); err != nil {
		return policy, false, err
	}
	switch os.Getenv("REF_LEDGER_LOG_COMMAND_ALLOW_ALL_TENANTS") {
	case "", "false":
	case "true":
		policy.allowAllTenants = true
	default:
		return policy, false, errors.New("invalid all-tenant command authorization")
	}
	return policy, true, nil
}

func validateLogCommand(command logCommand, policy logCommandPolicy, now time.Time) string {
	id, err := uuid.Parse(command.ID)
	if err != nil || id.String() != command.ID || command.RequestID != command.ID {
		return "invalid_request_id"
	}
	if command.Version != 1 {
		return "invalid_version"
	}
	if command.WorkerEnvironment != policy.environment || command.Environment != policy.environment {
		return "invalid_environment"
	}
	if !policy.actors[command.Actor] {
		return "invalid_actor"
	}
	switch command.Type {
	case "set_capture_threshold":
		if command.Scope != "environment" || command.TenantID != "" || command.Threshold < LogLevelError || command.Threshold > LogLevelDebug {
			return "invalid_scope_or_threshold"
		}
	case "clear_logs":
		switch command.Scope {
		case "environment":
			if command.TenantID != "" || !policy.allowAllTenants {
				return "invalid_scope"
			}
		case "tenant":
			if command.TenantID == "" || !policy.tenants[command.TenantID] {
				return "invalid_scope"
			}
		default:
			return "invalid_scope"
		}
		if command.Confirmation != "CLEAR "+policy.environment || command.Cutoff.IsZero() || command.Cutoff.After(now) {
			return "invalid_confirmation_or_cutoff"
		}
	default:
		return "invalid_type"
	}
	return ""
}

// RunLogCommandWorker is opt-in. Command effects, result, and audit commit together.
func RunLogCommandWorker(ctx context.Context) {
	policy, enabled, err := configuredLogCommandPolicy()
	if err != nil {
		log.Printf("log command worker disabled: %v", err)
		return
	}
	if !enabled {
		return
	}
	if !structuredLogsInitialized.Load() {
		log.Printf("log command worker disabled: structured logging is not initialized")
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := processPendingLogCommands(ctx, policy); err != nil && ctx.Err() == nil {
			log.Printf("log command worker failed: category=%s", SafeErrorCategory(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func processPendingLogCommands(parent context.Context, policy logCommandPolicy) error {
	if Client == nil || Database == "" {
		return errors.New("structured log database is not initialized")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	collection := Client.Database(Database).Collection("refLedgerLogCommands")
	cursor, err := collection.Find(ctx, bson.M{"workerEnvironment": policy.environment, "state": "pending"},
		options.Find().SetProjection(bson.M{"_id": 1}).SetLimit(25).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	var ids []bson.M
	err = cursor.All(ctx, &ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := processLogCommand(ctx, id["_id"], policy); err != nil {
			return err
		}
	}
	return nil
}

func processLogCommand(ctx context.Context, id any, policy logCommandPolicy) error {
	session, err := Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	db := Client.Database(Database)
	commands := db.Collection("refLedgerLogCommands")
	_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		filter := bson.M{"_id": id, "workerEnvironment": policy.environment, "state": "pending"}
		raw, err := commands.FindOne(sc, filter).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var command logCommand
		rejection := "malformed"
		if bson.Unmarshal(raw, &command) == nil {
			rejection = validateLogCommand(command, policy, time.Now().UTC())
		}
		outcome := "success"
		var deleted int64
		if rejection != "" {
			outcome = "rejected"
		} else if command.Type == "set_capture_threshold" {
			if err := SetCaptureThreshold(sc, command.Threshold); err != nil {
				return nil, err
			}
		} else {
			deleted, err = ClearStructuredLogs(sc, command.Cutoff, command.TenantID)
			if err != nil {
				return nil, err
			}
		}
		scope := "environment:" + policy.environment
		if rejection == "" && command.Type == "clear_logs" && command.Scope == "tenant" {
			scope += ";tenant:" + command.TenantID
		}
		if err := addLogAudit(sc, command.Actor, "atlas."+command.Type, scope, outcome, command.ID); err != nil {
			return nil, err
		}
		result, err := commands.UpdateOne(sc, filter, bson.M{"$set": bson.M{
			"state": "completed", "outcome": outcome, "reason": rejection,
			"deletedCount": deleted, "completedAt": time.Now().UTC(),
		}})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != 1 {
			return nil, fmt.Errorf("command changed concurrently")
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}
