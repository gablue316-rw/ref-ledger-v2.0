package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultMongoURI  = "mongodb://localhost:27017/?replicaSet=refLedgerRS"
	defaultBackupDir = `C:\Personal\MongoDB-Backups`

	// The program checks the schedule once per minute.
	checkInterval = time.Minute

	// Backup time in local system time.
	backupHour   = 2
	backupMinute = 0
)

type Config struct {
	MongoURI  string
	BackupDir string
}

type BackupState struct {
	LastOplogTimestamp primitive.Timestamp `bson:"-" json:"-"`
	LastSeconds        uint32              `json:"lastSeconds"`
	LastIncrement      uint32              `json:"lastIncrement"`

	LastDailyDate   string `json:"lastDailyDate"`
	LastWeeklyDate  string `json:"lastWeeklyDate"`
	LastMonthlyDate string `json:"lastMonthlyDate"`
}

type OplogMetadata struct {
	Filename string `json:"filename"`

	StartSeconds   uint32 `json:"startSeconds"`
	StartIncrement uint32 `json:"startIncrement"`

	EndSeconds   uint32 `json:"endSeconds"`
	EndIncrement uint32 `json:"endIncrement"`

	EntryCount int64     `json:"entryCount"`
	CreatedAt  time.Time `json:"createdAt"`
}

func main() {
	cfg := loadConfig()

	if err := os.MkdirAll(cfg.BackupDir, 0755); err != nil {
		log.Fatalf("create backup directory: %v", err)
	}

	ctx := context.Background()

	client, err := mongo.Connect(
		ctx,
		options.Client().ApplyURI(cfg.MongoURI),
	)
	if err != nil {
		log.Fatalf("connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("ping MongoDB: %v", err)
	}

	if err := verifyReplicaSet(ctx, client); err != nil {
		log.Fatalf("MongoDB replica-set check failed: %v", err)
	}

	statePath := filepath.Join(cfg.BackupDir, "backup-state.json")

	state, err := loadState(statePath)
	if err != nil {
		log.Fatalf("load backup state: %v", err)
	}

	/*
		The first time the program runs, initialize the oplog position to
		the newest current entry. This prevents the first daily job from
		copying the entire existing oplog.

		You should create a full backup immediately after this initialization.
	*/
	if state.LastSeconds == 0 && state.LastIncrement == 0 {
		latest, err := getLatestOplogTimestamp(ctx, client)
		if err != nil {
			log.Fatalf("initialize oplog position: %v", err)
		}

		setStateTimestamp(&state, latest)

		if err := saveState(statePath, state); err != nil {
			log.Fatalf("save initial state: %v", err)
		}

		log.Printf(
			"Initialized oplog position at %d:%d",
			latest.T,
			latest.I,
		)

		log.Println("Creating initial full backup...")

		if err := createFullBackup(cfg, "Initial"); err != nil {
			log.Fatalf("initial full backup failed: %v", err)
		}
	}

	log.Println("Ref Ledger backup service started")
	log.Printf("Backup directory: %s", cfg.BackupDir)
	log.Printf("MongoDB URI: %s", redactMongoURI(cfg.MongoURI))

	runScheduler(ctx, client, cfg, statePath, &state)
}

func runScheduler(
	ctx context.Context,
	client *mongo.Client,
	cfg Config,
	statePath string,
	state *BackupState,
) {
	// Check immediately at startup.
	runScheduledBackups(ctx, client, cfg, statePath, state, time.Now())

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for now := range ticker.C {
		runScheduledBackups(ctx, client, cfg, statePath, state, now)
	}
}

func runScheduledBackups(
	ctx context.Context,
	client *mongo.Client,
	cfg Config,
	statePath string,
	state *BackupState,
	now time.Time,
) {
	if now.Hour() != backupHour || now.Minute() != backupMinute {
		return
	}

	today := now.Format("2006-01-02")

	/*
		Run the daily oplog backup first.

		This closes the previous daily oplog segment before any full backup
		starts. Weekly and monthly backups then create new full recovery
		baselines.
	*/
	if state.LastDailyDate != today {
		if err := createDailyOplogBackup(ctx, client, cfg, state); err != nil {
			log.Printf("daily oplog backup failed: %v", err)
		} else {
			state.LastDailyDate = today

			if err := saveState(statePath, *state); err != nil {
				log.Printf("save state after daily backup: %v", err)
			}
		}
	}

	// Monday
	if now.Weekday() == time.Monday && state.LastWeeklyDate != today {
		if err := createFullBackup(cfg, "Weekly"); err != nil {
			log.Printf("weekly full backup failed: %v", err)
		} else {
			state.LastWeeklyDate = today

			if err := saveState(statePath, *state); err != nil {
				log.Printf("save state after weekly backup: %v", err)
			}
		}
	}

	// First day of the month
	if now.Day() == 1 && state.LastMonthlyDate != today {
		if err := createFullBackup(cfg, "Monthly"); err != nil {
			log.Printf("monthly full backup failed: %v", err)
		} else {
			state.LastMonthlyDate = today

			if err := saveState(statePath, *state); err != nil {
				log.Printf("save state after monthly backup: %v", err)
			}
		}
	}
}

func createDailyOplogBackup(
	ctx context.Context,
	client *mongo.Client,
	cfg Config,
	state *BackupState,
) error {
	startTimestamp := stateTimestamp(*state)

	endTimestamp, err := getLatestOplogTimestamp(ctx, client)
	if err != nil {
		return fmt.Errorf("get ending oplog timestamp: %w", err)
	}

	if compareTimestamps(endTimestamp, startTimestamp) <= 0 {
		log.Println("No new oplog entries to back up")
		return nil
	}

	if err := verifyOplogStartStillExists(ctx, client, startTimestamp); err != nil {
		return err
	}

	timestampText := time.Now().Format("2006-01-02-15-04-05")

	filename := fmt.Sprintf(
		"Daily-oplog-%s-%d-%d_to_%d-%d.bson",
		timestampText,
		startTimestamp.T,
		startTimestamp.I,
		endTimestamp.T,
		endTimestamp.I,
	)

	finalPath := filepath.Join(cfg.BackupDir, filename)
	tempPath := finalPath + ".tmp"

	entryCount, err := writeOplogRange(
		ctx,
		client,
		tempPath,
		startTimestamp,
		endTimestamp,
	)
	if err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if entryCount == 0 {
		_ = os.Remove(tempPath)
		log.Println("No new oplog entries were found")
		return nil
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename oplog backup: %w", err)
	}

	metadata := OplogMetadata{
		Filename: filename,

		StartSeconds:   startTimestamp.T,
		StartIncrement: startTimestamp.I,

		EndSeconds:   endTimestamp.T,
		EndIncrement: endTimestamp.I,

		EntryCount: entryCount,
		CreatedAt:  time.Now(),
	}

	if err := saveOplogMetadata(finalPath+".json", metadata); err != nil {
		/*
			Do not advance the state if the metadata could not be written.
			The BSON backup remains intact, allowing the situation to be
			investigated manually.
		*/
		return fmt.Errorf("save oplog metadata: %w", err)
	}

	setStateTimestamp(state, endTimestamp)

	log.Printf(
		"Daily oplog backup completed: %s; entries=%d; range=%d:%d through %d:%d",
		finalPath,
		entryCount,
		startTimestamp.T,
		startTimestamp.I,
		endTimestamp.T,
		endTimestamp.I,
	)

	return nil
}

func writeOplogRange(
	ctx context.Context,
	client *mongo.Client,
	outputPath string,
	start primitive.Timestamp,
	end primitive.Timestamp,
) (int64, error) {
	oplog := client.Database("local").Collection("oplog.rs")

	filter := bson.D{
		{
			Key: "ts",
			Value: bson.D{
				{Key: "$gt", Value: start},
				{Key: "$lte", Value: end},
			},
		},
	}

	findOptions := options.Find().
		SetSort(bson.D{{Key: "$natural", Value: 1}}).
		SetBatchSize(1000)

	cursor, err := oplog.Find(ctx, filter, findOptions)
	if err != nil {
		return 0, fmt.Errorf("query oplog: %w", err)
	}
	defer cursor.Close(ctx)

	file, err := os.OpenFile(
		outputPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0600,
	)
	if err != nil {
		return 0, fmt.Errorf("create oplog file: %w", err)
	}

	shouldDelete := true

	defer func() {
		_ = file.Close()

		if shouldDelete {
			_ = os.Remove(outputPath)
		}
	}()

	var count int64

	for cursor.Next(ctx) {
		var raw bson.Raw

		if err := cursor.Decode(&raw); err != nil {
			return count, fmt.Errorf("decode oplog entry: %w", err)
		}

		/*
			mongorestore expects a BSON stream: one complete BSON document
			immediately followed by the next BSON document.
		*/
		if _, err := file.Write(raw); err != nil {
			return count, fmt.Errorf("write oplog entry: %w", err)
		}

		count++
	}

	if err := cursor.Err(); err != nil {
		return count, fmt.Errorf("read oplog cursor: %w", err)
	}

	if err := file.Sync(); err != nil {
		return count, fmt.Errorf("flush oplog file: %w", err)
	}

	if err := file.Close(); err != nil {
		return count, fmt.Errorf("close oplog file: %w", err)
	}

	shouldDelete = false

	return count, nil
}

func createFullBackup(cfg Config, backupType string) error {
	timestampText := time.Now().Format("2006-01-02-15-04-05")

	var filename string

	switch backupType {
	case "Weekly":
		filename = fmt.Sprintf(
			"Weekly-refLedger_%s",
			timestampText,
		)

	case "Monthly":
		filename = fmt.Sprintf(
			"Monthly-refLedger-%s",
			timestampText,
		)

	case "Initial":
		filename = fmt.Sprintf(
			"Initial-refLedger-%s",
			timestampText,
		)

	default:
		return fmt.Errorf("unsupported backup type %q", backupType)
	}

	finalPath := filepath.Join(cfg.BackupDir, filename)
	tempPath := finalPath + ".tmp"

	/*
		Do not use --db here.

		--oplog requires a full dump of the replica-set member. The output
		is a compressed directory containing all databases plus oplog.bson.gz.
	*/
	args := []string{
		"--uri=" + cfg.MongoURI,
		"--oplog",
		"--gzip",
		"--out=" + tempPath,
	}

	cmd := exec.Command("mongodump", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tempPath)

		return fmt.Errorf(
			"mongodump failed: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.RemoveAll(tempPath)
		return fmt.Errorf("rename full backup directory: %w", err)
	}

	log.Printf(
		"%s full backup completed: %s",
		backupType,
		finalPath,
	)

	return nil
}

func getLatestOplogTimestamp(
	ctx context.Context,
	client *mongo.Client,
) (primitive.Timestamp, error) {
	oplog := client.Database("local").Collection("oplog.rs")

	var entry struct {
		Timestamp primitive.Timestamp `bson:"ts"`
	}

	err := oplog.FindOne(
		ctx,
		bson.D{},
		options.FindOne().SetSort(
			bson.D{{Key: "$natural", Value: -1}},
		),
	).Decode(&entry)

	if err != nil {
		return primitive.Timestamp{}, fmt.Errorf(
			"read latest oplog entry: %w",
			err,
		)
	}

	return entry.Timestamp, nil
}

func getOldestOplogTimestamp(
	ctx context.Context,
	client *mongo.Client,
) (primitive.Timestamp, error) {
	oplog := client.Database("local").Collection("oplog.rs")

	var entry struct {
		Timestamp primitive.Timestamp `bson:"ts"`
	}

	err := oplog.FindOne(
		ctx,
		bson.D{},
		options.FindOne().SetSort(
			bson.D{{Key: "$natural", Value: 1}},
		),
	).Decode(&entry)

	if err != nil {
		return primitive.Timestamp{}, fmt.Errorf(
			"read oldest oplog entry: %w",
			err,
		)
	}

	return entry.Timestamp, nil
}

func verifyOplogStartStillExists(
	ctx context.Context,
	client *mongo.Client,
	start primitive.Timestamp,
) error {
	oldest, err := getOldestOplogTimestamp(ctx, client)
	if err != nil {
		return err
	}

	if compareTimestamps(start, oldest) < 0 {
		return fmt.Errorf(
			"oplog gap detected: last backup ended at %d:%d, "+
				"but the oldest available oplog entry is %d:%d; "+
				"create a new full backup before continuing",
			start.T,
			start.I,
			oldest.T,
			oldest.I,
		)
	}

	return nil
}

func verifyReplicaSet(
	ctx context.Context,
	client *mongo.Client,
) error {
	var result bson.M

	err := client.Database("admin").RunCommand(
		ctx,
		bson.D{{Key: "hello", Value: 1}},
	).Decode(&result)

	if err != nil {
		return err
	}

	setName, ok := result["setName"].(string)
	if !ok || setName == "" {
		return errors.New(
			"MongoDB is not running as a replica-set member",
		)
	}

	log.Printf("Connected to replica set: %s", setName)

	return nil
}

func loadConfig() Config {
	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		mongoURI = defaultMongoURI
	}

	backupDir := os.Getenv("MONGODB_BACKUP_DIR")
	if backupDir == "" {
		backupDir = defaultBackupDir
	}

	return Config{
		MongoURI:  mongoURI,
		BackupDir: backupDir,
	}
}

func loadState(path string) (BackupState, error) {
	var state BackupState

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)

	if err := decoder.Decode(&state); err != nil {
		return state, err
	}

	state.LastOplogTimestamp = primitive.Timestamp{
		T: state.LastSeconds,
		I: state.LastIncrement,
	}

	return state, nil
}

func saveState(path string, state BackupState) error {
	state.LastSeconds = state.LastOplogTimestamp.T
	state.LastIncrement = state.LastOplogTimestamp.I

	tempPath := path + ".tmp"

	file, err := os.OpenFile(
		tempPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0600,
	)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(state); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	return os.Rename(tempPath, path)
}

func saveOplogMetadata(
	path string,
	metadata OplogMetadata,
) error {
	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0600,
	)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	return encoder.Encode(metadata)
}

func stateTimestamp(state BackupState) primitive.Timestamp {
	return primitive.Timestamp{
		T: state.LastSeconds,
		I: state.LastIncrement,
	}
}

func setStateTimestamp(
	state *BackupState,
	timestamp primitive.Timestamp,
) {
	state.LastOplogTimestamp = timestamp
	state.LastSeconds = timestamp.T
	state.LastIncrement = timestamp.I
}

func compareTimestamps(
	left primitive.Timestamp,
	right primitive.Timestamp,
) int {
	if left.T < right.T {
		return -1
	}

	if left.T > right.T {
		return 1
	}

	if left.I < right.I {
		return -1
	}

	if left.I > right.I {
		return 1
	}

	return 0
}

func redactMongoURI(uri string) string {
	at := strings.LastIndex(uri, "@")
	scheme := strings.Index(uri, "://")

	if at == -1 || scheme == -1 || at < scheme {
		return uri
	}

	return uri[:scheme+3] + "***:***" + uri[at:]
}

// Keep io imported if you later add streamed file compression.
var _ io.Writer
