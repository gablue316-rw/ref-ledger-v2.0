package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("payroll-deduction-migration", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	databaseName := flags.String("database", "", "MongoDB database to inspect")
	typeMapPath := flags.String("type-map", "", "CSV of verified payment expense types")
	reportPath := flags.String("report", "", "JSON review report output path")
	apply := flags.Bool("apply", false, "apply the migration; default is read-only dry-run")
	confirmDatabase := flags.String("confirm-database", "", "database name confirmation required with -apply")
	approveTaxYearDifferences := flags.Bool("approve-tax-year-differences", false, "allow applying reviewed game/payment tax-year differences")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(*databaseName) == "" {
		return errors.New("-database is required to avoid selecting a database implicitly")
	}
	if strings.TrimSpace(*reportPath) == "" || *reportPath == "-" {
		return errors.New("-report must name a JSON file; standard output is not supported")
	}
	if *apply {
		if *confirmDatabase != *databaseName {
			return errors.New("-apply requires -confirm-database to exactly match -database")
		}
		if strings.TrimSpace(*typeMapPath) == "" {
			return errors.New("-apply requires a verified -type-map CSV")
		}
	} else if *approveTaxYearDifferences {
		return errors.New("-approve-tax-year-differences requires -apply")
	}
	if *typeMapPath != "" && sameFilePath(*typeMapPath, *reportPath) {
		return errors.New("-report must not overwrite the type-map CSV")
	}

	typeMap := make(map[string][]string)
	if *typeMapPath != "" {
		file, err := os.Open(*typeMapPath)
		if err != nil {
			return fmt.Errorf("open type-map CSV: %w", err)
		}
		typeMap, err = parseHistoricalTypeMap(file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close type-map CSV: %w", closeErr)
		}
	}

	uri, _, err := database.BuildMongoURI()
	if err != nil {
		return fmt.Errorf("build MongoDB URI: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect to MongoDB: %w", err)
	}
	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "disconnect MongoDB: %v\n", err)
		}
	}()
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("ping MongoDB: %w", err)
	}

	db := client.Database(*databaseName)
	plan, err := database.PlanHistoricalPayrollDeductionMigration(ctx, db, typeMap)
	if err != nil {
		return fmt.Errorf("plan historical payroll deduction migration: %w", err)
	}
	var applyErr error
	if *apply {
		applyErr = database.ApplyHistoricalPayrollDeductionMigration(
			ctx, db, &plan, *approveTaxYearDifferences,
		)
	}
	if err := writeMigrationReport(*reportPath, plan.Report); err != nil {
		return err
	}
	if applyErr != nil {
		return fmt.Errorf("apply migration: %w; review report: %s", applyErr, *reportPath)
	}
	if *apply {
		fmt.Fprintf(os.Stderr, "Migration completed: %d payment(s) applied. Report: %s\n", plan.Report.AppliedChanges, *reportPath)
	} else {
		fmt.Fprintf(os.Stderr, "Read-only dry-run complete: readyToApply=%t, plannedChanges=%d. Report: %s\n",
			plan.Report.ReadyToApply, plan.Report.PlannedChanges, *reportPath)
	}
	return nil
}

func parseHistoricalTypeMap(input io.Reader) (map[string][]string, error) {
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = 4
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read type-map CSV: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("type-map CSV is empty")
	}
	headers := rows[0]
	if len(headers) != 4 {
		return nil, errors.New("type-map CSV header must be tenantId,association,paymentId,expenseType")
	}
	headers[0] = strings.TrimPrefix(headers[0], "\uFEFF")
	expected := []string{"tenantId", "association", "paymentId", "expenseType"}
	for i := range expected {
		if !strings.EqualFold(strings.TrimSpace(headers[i]), expected[i]) {
			return nil, errors.New("type-map CSV header must be tenantId,association,paymentId,expenseType")
		}
	}

	typeMap := make(map[string][]string)
	for line, row := range rows[1:] {
		tenantID := strings.TrimSpace(row[0])
		association := strings.TrimSpace(row[1])
		paymentID := strings.TrimSpace(row[2])
		expenseType := strings.TrimSpace(row[3])
		if tenantID == "" || association == "" || paymentID == "" || expenseType == "" {
			return nil, fmt.Errorf("type-map CSV row %d has a blank field", line+2)
		}
		key := database.HistoricalMigrationTypeMapKey(tenantID, association, paymentID)
		typeMap[key] = append(typeMap[key], expenseType)
	}
	return typeMap, nil
}

func writeMigrationReport(path string, report database.HistoricalPayrollMigrationReport) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create migration report: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(report)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("write migration report: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close migration report: %w", closeErr)
	}
	return nil
}

func sameFilePath(first, second string) bool {
	firstAbs, firstErr := filepath.Abs(first)
	secondAbs, secondErr := filepath.Abs(second)
	if firstErr != nil || secondErr != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(firstAbs), filepath.Clean(secondAbs))
}
