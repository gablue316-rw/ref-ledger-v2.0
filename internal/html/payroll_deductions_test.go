package main

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestGameCSVSupportsCurrentAndLegacyDeductionHeaders(t *testing.T) {
	current := []string{
		"gameId", "date", "taxyear", "time", "sport", "site", "field",
		"numOfGames", "level", "gameFee", "travelPay", "assignorFee",
		"association", "status", "official1", "official2", "official3",
		"official4", "official5", "official6", "official7", "official8",
		"official9", "official10", "assignor", "home", "visitor", "notes",
	}
	legacy := append([]string(nil), current[:12]...)
	legacy = append(legacy, "deductions")
	legacy = append(legacy, current[12:]...)
	if err := validateGamesCSVHeader(current); err != nil {
		t.Fatalf("current header rejected: %v", err)
	}
	if err := validateGamesCSVHeader(legacy); err != nil {
		t.Fatalf("legacy header rejected: %v", err)
	}
	unknown := append([]string(nil), legacy...)
	unknown[12] = "unexpected"
	if err := validateGamesCSVHeader(unknown); err == nil {
		t.Fatal("unknown legacy column accepted")
	}

	row := make([]string, len(legacy))
	for i := range row {
		row[i] = string(rune('A' + i))
	}
	normalized := normalizeGameCSVRecord(row)
	expected := append(append([]string(nil), row[:12]...), row[13:]...)
	if !reflect.DeepEqual(normalized, expected) {
		t.Fatalf("legacy row was not safely normalized: %v", normalized)
	}
	if normalized[12] != row[13] || normalized[27] != row[28] {
		t.Fatalf("legacy columns shifted incorrectly: %v", normalized)
	}
}

func TestPaymentImportSupportsLegacyAndDeductionRows(t *testing.T) {
	legacy := validatePaymentImportRow(2, []string{
		"P1", "1/2/2026", "2026", "$480.00", "MSO", "11,12",
	})
	if !legacy.Valid {
		t.Fatalf("legacy payment row rejected: %v", legacy.Errors)
	}
	if legacy.Data.DeductionAmount != "" || legacy.Data.DeductionType != "" {
		t.Fatalf("legacy row acquired a deduction: %+v", legacy.Data)
	}

	withDeduction := validatePaymentImportRow(2, []string{
		"P2", "1/2/2026", "2026", "$480.00", "MSO", "11,12", "$30.00", "Equipment",
	})
	if !withDeduction.Valid {
		t.Fatalf("deduction payment row rejected: %v", withDeduction.Errors)
	}
	if withDeduction.Data.DeductionAmount != "30.00" || withDeduction.Data.DeductionType != "Equipment" {
		t.Fatalf("deduction row was not normalized: %+v", withDeduction.Data)
	}

	invalid := validatePaymentImportRow(2, []string{
		"P3", "1/2/2026", "2026", "$480.00", "MSO", "11,12", "30.00", "",
	})
	if invalid.Valid {
		t.Fatal("positive deduction without a type was accepted")
	}
}

func TestPaymentCSVExportRoundTripsDeductionColumns(t *testing.T) {
	expectedHeader := []string{
		"paymentId", "paymentDate", "taxYear", "paymentAmt", "association",
		"gameIds(comma-separated)", "deductionAmount", "deductionType",
	}
	if header := dataExportHeader("payments"); !reflect.DeepEqual(header, expectedHeader) {
		t.Fatalf("payment export header = %v, want %v", header, expectedHeader)
	}
	exported, err := dataExportRow("payments", bson.M{
		"paymentId":       "P1",
		"paymentDate":     "1/2/2025",
		"taxYear":         int32(2025),
		"paymentAmt":      int64(48000),
		"association":     "MSO",
		"gameIds":         primitive.A{int64(11), int64(12)},
		"deductionAmount": int64(3000),
		"deductionType":   "Equipment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 8 || exported[3] != "$480.00" || exported[6] != "$30.00" {
		t.Fatalf("unexpected exported payment row: %v", exported)
	}
	preview := validatePaymentImportRow(2, exported)
	if !preview.Valid || preview.Data.DeductionAmount != "30.00" || preview.Data.DeductionType != "Equipment" {
		t.Fatalf("exported payment did not round-trip: %+v", preview)
	}
}

func TestGameCSVExportHeaderOmitsDeductions(t *testing.T) {
	header := dataExportHeader("games")
	if len(header) != 28 {
		t.Fatalf("game export header has %d columns, want 28", len(header))
	}
	for _, column := range header {
		if column == "deductions" {
			t.Fatal("game export header still includes deductions")
		}
	}
}
