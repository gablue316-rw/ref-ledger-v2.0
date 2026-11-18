package database

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestPlanHistoricalPayrollDeductionMigration(t *testing.T) {
	tests := []struct {
		name                 string
		expenses             []bson.D
		typeMap              map[string][]string
		wantReady            bool
		wantEntries          int
		wantIssue            string
		wantTaxYearWarning   bool
		wantLegacyTotalYear  string
		wantPaymentTotalYear string
	}{
		{
			name:                 "maps verified type and groups by normalized tax year",
			typeMap:              map[string][]string{HistoricalMigrationTypeMapKey("tenant-a", "MSO", "payment-1"): {"Equipment"}},
			wantReady:            true,
			wantEntries:          1,
			wantTaxYearWarning:   true,
			wantLegacyTotalYear:  "2024",
			wantPaymentTotalYear: "2025",
		},
		{
			name:      "missing verified type blocks migration",
			wantReady: false,
			wantIssue: "unknown_expense_type",
		},
		{
			name: "possible manual duplicate blocks migration",
			expenses: []bson.D{{
				{Key: "expenseId", Value: "manual-1"},
				{Key: "tenantId", Value: "tenant-a"},
				{Key: "association", Value: "MSO"},
				{Key: "date", Value: "2025-01-02"},
				{Key: "amount", Value: int64(3000)},
			}},
			typeMap:     map[string][]string{HistoricalMigrationTypeMapKey("tenant-a", "MSO", "payment-1"): {"Equipment"}},
			wantReady:   false,
			wantEntries: 1,
			wantIssue:   "possible_duplicate_manual_expense",
		},
		{
			name: "orphan payroll expense blocks migration",
			expenses: []bson.D{{
				{Key: "expenseId", Value: "orphan-1"},
				{Key: "tenantId", Value: "tenant-a"},
				{Key: "association", Value: "MSO"},
				{Key: "source", Value: PayrollDeductionSource},
			}},
			typeMap:     map[string][]string{HistoricalMigrationTypeMapKey("tenant-a", "MSO", "payment-1"): {"Equipment"}},
			wantReady:   false,
			wantEntries: 1,
			wantIssue:   "linked_expense_missing_payment_reference",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock)).Run("planner", func(mt *mtest.T) {
				mt.AddMockResponses(
					mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, bson.D{
						{Key: "tenantId", Value: "tenant-a"},
						{Key: "association", Value: "MSO"},
						{Key: "gameId", Value: int64(42)},
						{Key: "status", Value: "Paid"},
						{Key: "deductions", Value: int64(3000)},
						{Key: "taxYear", Value: "02024"},
					}),
					mtest.CreateCursorResponse(0, mt.DB.Name()+".payments", mtest.FirstBatch, bson.D{
						{Key: "tenantId", Value: "tenant-a"},
						{Key: "association", Value: "MSO"},
						{Key: "paymentId", Value: "payment-1"},
						{Key: "paymentDate", Value: "2025-01-02"},
						{Key: "paymentAmt", Value: int64(47000)},
						{Key: "taxYear", Value: 2025},
						{Key: "gameIds", Value: []int64{42}},
					}),
					mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, test.expenses...),
				)

				plan, err := PlanHistoricalPayrollDeductionMigration(context.Background(), mt.DB, test.typeMap)
				if err != nil {
					t.Fatal(err)
				}
				if plan.Report.ReadyToApply != test.wantReady {
					t.Fatalf("ReadyToApply = %v, want %v; issues: %+v", plan.Report.ReadyToApply, test.wantReady, plan.Report.Issues)
				}
				if len(plan.Report.Entries) != test.wantEntries {
					t.Fatalf("entries = %d, want %d", len(plan.Report.Entries), test.wantEntries)
				}
				if test.wantIssue != "" && !hasHistoricalIssue(plan.Report.Issues, test.wantIssue) {
					t.Fatalf("issue %q missing from %+v", test.wantIssue, plan.Report.Issues)
				}
				if test.wantTaxYearWarning && !hasHistoricalIssue(plan.Report.Issues, "game_payment_tax_year_difference") {
					t.Fatalf("tax-year difference warning missing from %+v", plan.Report.Issues)
				}
				if test.wantLegacyTotalYear != "" && !hasHistoricalTotal(plan.Report.Totals, "legacy_game_tax_year", test.wantLegacyTotalYear, 3000) {
					t.Fatalf("legacy total for %s missing from %+v", test.wantLegacyTotalYear, plan.Report.Totals)
				}
				if test.wantPaymentTotalYear != "" && !hasHistoricalTotal(plan.Report.Totals, "proposed_payment_tax_year", test.wantPaymentTotalYear, 3000) {
					t.Fatalf("payment total for %s missing from %+v", test.wantPaymentTotalYear, plan.Report.Totals)
				}
			})
		})
	}
}

func TestHistoricalMigrationTypeMapKeyPreservesBoundaries(t *testing.T) {
	first := HistoricalMigrationTypeMapKey("tenant:a", "MSO", "P1")
	second := HistoricalMigrationTypeMapKey("tenant", "a:MSO", "P1")
	if first == second {
		t.Fatal("type-map key collided when identifiers contained separators")
	}
}

func hasHistoricalIssue(issues []HistoricalMigrationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func hasHistoricalTotal(totals []HistoricalMigrationTotal, source, year string, amount int64) bool {
	for _, total := range totals {
		if total.Source == source && total.TaxYear == year && total.AmountCents == amount &&
			total.TenantID == "tenant-a" && total.Association == "MSO" {
			return true
		}
	}
	return false
}
