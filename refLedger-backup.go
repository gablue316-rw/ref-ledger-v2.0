package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMongoURI      = "mongodb://localhost:27017"
	defaultDatabase      = "refLedger_v2"
	defaultBackupDir     = `C:\Personal\MongoDB-Backups`
	defaultBackupHour    = 2
	defaultBackupMinute  = 0
	defaultCheckInterval = time.Minute
	defaultBackupTimeout = 30 * time.Minute
	defaultMongodumpPath = "mongodump"
)

type Config struct {
	MongoURI      string
	Database      string
	BackupDir     string
	MongodumpPath string
	BackupHour    int
	BackupMinute  int
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	if err := validateConfig(config); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	if err := os.MkdirAll(config.BackupDir, 0755); err != nil {
		log.Fatalf(
			"Could not create backup directory %q: %v",
			config.BackupDir,
			err,
		)
	}

	log.Println("RefLedger MongoDB backup service started")
	log.Printf("Database: %s", config.Database)
	log.Printf("MongoDB URI: %s", sanitizeMongoURI(config.MongoURI))
	log.Printf("Backup directory: %s", config.BackupDir)
	log.Printf(
		"Scheduled backup time: %02d:%02d",
		config.BackupHour,
		config.BackupMinute,
	)
	log.Println("Weekly backups run on Monday")
	log.Println("Monthly backups run on the first day of the month")

	/*
		Check immediately when the application starts.

		This allows a missed backup to run if the application starts after
		the scheduled backup time.
	*/
	runScheduledBackups(config, time.Now())

	ticker := time.NewTicker(defaultCheckInterval)
	defer ticker.Stop()

	for currentTime := range ticker.C {
		runScheduledBackups(config, currentTime)
	}
}

func loadConfig() (Config, error) {
	config := Config{
		MongoURI:      getEnvironmentValue("MONGODB_URI", defaultMongoURI),
		Database:      getEnvironmentValue("BACKUP_DATABASE", defaultDatabase),
		BackupDir:     getEnvironmentValue("BACKUP_DIRECTORY", defaultBackupDir),
		MongodumpPath: getEnvironmentValue("MONGODUMP_PATH", defaultMongodumpPath),
		BackupHour:    defaultBackupHour,
		BackupMinute:  defaultBackupMinute,
	}

	var err error

	config.BackupHour, err = getEnvironmentInteger(
		"BACKUP_HOUR",
		defaultBackupHour,
	)
	if err != nil {
		return Config{}, err
	}

	config.BackupMinute, err = getEnvironmentInteger(
		"BACKUP_MINUTE",
		defaultBackupMinute,
	)
	if err != nil {
		return Config{}, err
	}

	return config, nil
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.MongoURI) == "" {
		return errors.New("MONGODB_URI cannot be empty")
	}

	if strings.TrimSpace(config.Database) == "" {
		return errors.New("BACKUP_DATABASE cannot be empty")
	}

	if strings.TrimSpace(config.BackupDir) == "" {
		return errors.New("BACKUP_DIRECTORY cannot be empty")
	}

	if strings.TrimSpace(config.MongodumpPath) == "" {
		return errors.New("MONGODUMP_PATH cannot be empty")
	}

	if config.BackupHour < 0 || config.BackupHour > 23 {
		return fmt.Errorf(
			"BACKUP_HOUR must be between 0 and 23; received %d",
			config.BackupHour,
		)
	}

	if config.BackupMinute < 0 || config.BackupMinute > 59 {
		return fmt.Errorf(
			"BACKUP_MINUTE must be between 0 and 59; received %d",
			config.BackupMinute,
		)
	}

	return nil
}

func runScheduledBackups(config Config, currentTime time.Time) {
	if !scheduledTimeReached(
		currentTime,
		config.BackupHour,
		config.BackupMinute,
	) {
		return
	}

	/*
		Daily backup always runs.
	*/
	runBackupIfMissing(config, "Daily", currentTime)

	/*
		Monday is time.Weekday value time.Monday.
	*/
	if currentTime.Weekday() == time.Monday {
		runBackupIfMissing(config, "Weekly", currentTime)
	}

	/*
		The first day of the month creates the monthly backup.
	*/
	if currentTime.Day() == 1 {
		runBackupIfMissing(config, "Monthly", currentTime)
	}
}

func scheduledTimeReached(
	currentTime time.Time,
	backupHour int,
	backupMinute int,
) bool {
	if currentTime.Hour() > backupHour {
		return true
	}

	if currentTime.Hour() == backupHour &&
		currentTime.Minute() >= backupMinute {
		return true
	}

	return false
}

func runBackupIfMissing(
	config Config,
	backupType string,
	currentTime time.Time,
) {
	exists, err := backupExistsForDate(
		config.BackupDir,
		backupType,
		config.Database,
		currentTime,
	)
	if err != nil {
		log.Printf(
			"Could not check for an existing %s backup: %v",
			backupType,
			err,
		)
		return
	}

	if exists {
		return
	}

	if err := createBackup(config, backupType, currentTime); err != nil {
		log.Printf("%s backup failed: %v", backupType, err)
		return
	}

	log.Printf("%s backup completed successfully", backupType)
}

func backupExistsForDate(
	backupDirectory string,
	backupType string,
	database string,
	currentTime time.Time,
) (bool, error) {
	datePrefix := currentTime.Format("2006-01-02")

	var filenamePattern string

	switch backupType {
	case "Daily":
		filenamePattern = fmt.Sprintf(
			"Daily-%s-%s-*.archive.gz",
			database,
			datePrefix,
		)

	case "Weekly":
		filenamePattern = fmt.Sprintf(
			"Weekly-%s_%s-*.archive.gz",
			database,
			datePrefix,
		)

	case "Monthly":
		filenamePattern = fmt.Sprintf(
			"Monthly-%s-%s-*.archive.gz",
			database,
			datePrefix,
		)

	default:
		return false, fmt.Errorf(
			"unsupported backup type %q",
			backupType,
		)
	}

	fullPattern := filepath.Join(backupDirectory, filenamePattern)

	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return false, fmt.Errorf(
			"could not search for backup files using %q: %w",
			fullPattern,
			err,
		)
	}

	return len(matches) > 0, nil
}

func createBackup(
	config Config,
	backupType string,
	currentTime time.Time,
) error {
	filename, err := buildBackupFilename(
		backupType,
		config.Database,
		currentTime,
	)
	if err != nil {
		return err
	}

	backupPath := filepath.Join(config.BackupDir, filename)
	temporaryPath := backupPath + ".partial"

	/*
		Remove a leftover partial file from a previously interrupted backup.
	*/
	if err := os.Remove(temporaryPath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"could not remove old partial backup %q: %w",
			temporaryPath,
			err,
		)
	}

	log.Printf(
		"Starting %s backup: %s",
		backupType,
		backupPath,
	)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		defaultBackupTimeout,
	)
	defer cancel()

	args := []string{
		"--uri=" + config.MongoURI,
		"--db=" + config.Database,
		"--archive=" + temporaryPath,
		"--gzip",
	}

	command := exec.CommandContext(
		ctx,
		config.MongodumpPath,
		args...,
	)

	output, commandErr := command.CombinedOutput()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = os.Remove(temporaryPath)

		return fmt.Errorf(
			"mongodump exceeded the %s timeout",
			defaultBackupTimeout,
		)
	}

	if commandErr != nil {
		_ = os.Remove(temporaryPath)

		return fmt.Errorf(
			"mongodump returned an error: %w; output: %s",
			commandErr,
			strings.TrimSpace(string(output)),
		)
	}

	fileInfo, err := os.Stat(temporaryPath)
	if err != nil {
		return fmt.Errorf(
			"mongodump completed, but the backup file was not found: %w",
			err,
		)
	}

	if fileInfo.Size() == 0 {
		_ = os.Remove(temporaryPath)

		return errors.New(
			"mongodump created an empty backup file",
		)
	}

	/*
		Rename only after mongodump succeeds.

		This prevents an incomplete backup from appearing to be a valid
		completed backup.
	*/
	if err := os.Rename(temporaryPath, backupPath); err != nil {
		return fmt.Errorf(
			"could not rename completed backup to %q: %w",
			backupPath,
			err,
		)
	}

	log.Printf(
		"Created %s backup: %s (%s)",
		backupType,
		backupPath,
		formatFileSize(fileInfo.Size()),
	)

	return nil
}

func buildBackupFilename(
	backupType string,
	database string,
	currentTime time.Time,
) (string, error) {
	timestamp := currentTime.Format("2006-01-02-15-04-05")

	switch backupType {
	case "Daily":
		return fmt.Sprintf(
			"Daily-%s-%s.archive.gz",
			database,
			timestamp,
		), nil

	case "Weekly":
		/*
			This uses the underscore from your requested weekly pattern:

			Weekly-refLedger_v2_2026-07-20-18-14-19
		*/
		return fmt.Sprintf(
			"Weekly-%s_%s.archive.gz",
			database,
			timestamp,
		), nil

	case "Monthly":
		return fmt.Sprintf(
			"Monthly-%s-%s.archive.gz",
			database,
			timestamp,
		), nil

	default:
		return "", fmt.Errorf(
			"unsupported backup type %q",
			backupType,
		)
	}
}

func getEnvironmentValue(name string, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(name))

	if value == "" {
		return defaultValue
	}

	return value
}

func getEnvironmentInteger(
	name string,
	defaultValue int,
) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))

	if value == "" {
		return defaultValue, nil
	}

	integerValue, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf(
			"%s must be an integer; received %q",
			name,
			value,
		)
	}

	return integerValue, nil
}

func sanitizeMongoURI(uri string) string {
	/*
		Avoid displaying a MongoDB password in the log.

		Example:

		mongodb://username:password@localhost:27017

		becomes:

		mongodb://***:***@localhost:27017
	*/
	schemeIndex := strings.Index(uri, "://")
	atIndex := strings.LastIndex(uri, "@")

	if schemeIndex == -1 || atIndex == -1 || atIndex < schemeIndex {
		return uri
	}

	return uri[:schemeIndex+3] + "***:***" + uri[atIndex:]
}

func formatFileSize(size int64) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * kilobyte
		gigabyte = 1024 * megabyte
	)

	switch {
	case size >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(size)/gigabyte)

	case size >= megabyte:
		return fmt.Sprintf("%.2f MB", float64(size)/megabyte)

	case size >= kilobyte:
		return fmt.Sprintf("%.2f KB", float64(size)/kilobyte)

	default:
		return fmt.Sprintf("%d bytes", size)
	}
}
