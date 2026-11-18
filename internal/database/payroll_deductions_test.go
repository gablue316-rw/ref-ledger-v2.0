package database

import (
	"testing"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
)

func TestNormalizePaymentDeduction(t *testing.T) {
	tests := []struct {
		name        string
		amount      string
		expenseType string
		wantCents   int64
		wantType    string
		wantErr     bool
	}{
		{name: "blank", amount: "", wantCents: 0},
		{name: "zero", amount: "0.00", expenseType: "not a type", wantCents: 0},
		{name: "canonicalizes supported type", amount: "$12.30", expenseType: "equipment", wantCents: 1230, wantType: "Equipment"},
		{name: "positive requires type", amount: "1.00", wantErr: true},
		{name: "rejects negative", amount: "-1.00", wantErr: true},
		{name: "rejects excess precision", amount: "1.001", expenseType: "Food", wantErr: true},
		{name: "rejects unsupported type", amount: "1.00", expenseType: "Other", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCents, gotType, err := NormalizePaymentDeduction(test.amount, test.expenseType)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (gotCents != test.wantCents || gotType != test.wantType) {
				t.Fatalf("got (%d, %q), want (%d, %q)", gotCents, gotType, test.wantCents, test.wantType)
			}
		})
	}
}

func TestPayrollPaymentReferenceIsTenantScopedByCallerAndStable(t *testing.T) {
	first := PayrollPaymentReference("A:B", "payment-1")
	if first != PayrollPaymentReference("A:B", "payment-1") {
		t.Fatal("payment reference changed across retries")
	}
	if first == PayrollPaymentReference("A", "B:payment-1") {
		t.Fatal("association and payment ID were not unambiguously encoded")
	}
	if payrollDeductionExpenseID(first) != payrollDeductionExpenseID(first) {
		t.Fatal("expense ID changed across retries")
	}
}

func TestPayrollDeductionExpenseIsMonetaryEvenForMileageType(t *testing.T) {
	payment := model.PaymentDoc{
		PaymentId:       "P1",
		PaymentDate:     "1/2/2026",
		Association:     "MSO",
		TaxYear:         2025,
		DeductionAmount: 3000,
		DeductionType:   "Mileage",
	}
	expense := paymentDeductionExpense("tenant-a", payment, PayrollPaymentReference("MSO", "P1"))
	if expense.Amount != 3000 || expense.Mileage != 0 || expense.Source != PayrollDeductionSource || expense.TaxYear != "2025" {
		t.Fatalf("unexpected linked expense: %+v", expense)
	}
	if _, err := utils.ParseAmountCents("999999999999999999999999.00"); err == nil {
		t.Fatal("overflowing amount accepted")
	}
}
