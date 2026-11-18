package main

import (
	"strings"
	"testing"

	"ref-ledger-v2/internal/database"
)

func TestParseHistoricalTypeMap(t *testing.T) {
	input := "\uFEFFtenantId,association,paymentId,expenseType\n" +
		" tenant-a , MSO , payment-1 ,Equipment\n" +
		"tenant-a,MSO,payment-1,Food\n"
	typeMap, err := parseHistoricalTypeMap(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	types := typeMap[database.HistoricalMigrationTypeMapKey("tenant-a", "MSO", "payment-1")]
	if len(types) != 2 || types[0] != "Equipment" || types[1] != "Food" {
		t.Fatalf("unexpected type map values: %#v", types)
	}
}

func TestParseHistoricalTypeMapRejectsBlankFields(t *testing.T) {
	input := "tenantId,association,paymentId,expenseType\n" +
		"tenant-a,MSO,payment-1,\n"
	if _, err := parseHistoricalTypeMap(strings.NewReader(input)); err == nil {
		t.Fatal("blank expense type accepted")
	}
}

func TestRunRequiresExactDatabaseConfirmationForApply(t *testing.T) {
	err := run([]string{
		"-database", "refledger_test",
		"-report", "report.json",
		"-apply",
		"-confirm-database", "refledger_production",
		"-type-map", "types.csv",
	})
	if err == nil || !strings.Contains(err.Error(), "-confirm-database") {
		t.Fatalf("expected database confirmation error, got %v", err)
	}
}

func TestSameFilePath(t *testing.T) {
	if !sameFilePath("report.json", ".\\report.json") {
		t.Fatal("equivalent report paths were not recognized")
	}
	if sameFilePath("types.csv", "report.json") {
		t.Fatal("different paths were treated as the same file")
	}
}
