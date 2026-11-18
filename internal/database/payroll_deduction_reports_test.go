package database

import (
	"testing"

	"ref-ledger-v2/internal/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestPayrollDeductionReportAcceptanceTotals(t *testing.T) {
	mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock)).Run("reports", func(mt *mtest.T) {
		oldClient, oldDatabase := Client, Database
		Client, Database = mt.Client, mt.DB.Name()
		defer func() { Client, Database = oldClient, oldDatabase }()

		gameEarnings := gameEarningsCents(model.GameDoc{
			NumOfGames: 1, GameFee: 50000, TravelPay: 2500, AssignorFee: 1500,
		})
		if gameEarnings != 51000 || gameEarnings-3000 != 48000 {
			mt.Fatalf("earnings/deposit = %d/%d cents, want 51000/48000", gameEarnings, gameEarnings-3000)
		}

		game := bson.D{
			{Key: "association", Value: "MSO"},
			{Key: "taxYear", Value: "2025"},
			{Key: "status", Value: "Paid"},
			{Key: "numOfGames", Value: int64(1)},
			{Key: "gameFee", Value: int64(50000)},
			{Key: "travelPay", Value: int64(2500)},
			{Key: "assignorFee", Value: int64(1500)},
		}
		payrollDeduction := bson.D{
			{Key: "association", Value: "MSO"},
			{Key: "taxYear", Value: "2025"},
			{Key: "type", Value: "Mileage"},
			{Key: "amount", Value: int64(3000)},
			{Key: "mileage", Value: int64(0)},
			{Key: "source", Value: PayrollDeductionSource},
			{Key: "paymentRef", Value: "3:MSO:payment-1"},
		}
		otherExpense := bson.D{
			{Key: "association", Value: "MSO"},
			{Key: "taxYear", Value: "2025"},
			{Key: "type", Value: "Food"},
			{Key: "amount", Value: int64(2000)},
		}

		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, game),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, payrollDeduction),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, game),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, payrollDeduction, otherExpense),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, payrollDeduction, otherExpense),
		)

		revenue, err := GetRevenueReports("tenant-a", []string{"MSO"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		revenueRecord := revenue["3:MSO:2025"]
		if revenueRecord.GrossRevenue != "$525.00" ||
			revenueRecord.TotAssignorFees != "$15.00" ||
			revenueRecord.PayrollDeductions != "$30.00" ||
			revenueRecord.NetRevenue != "$480.00" {
			mt.Fatalf("unexpected revenue report totals: %+v", revenueRecord)
		}

		financial, err := GetFinancialReports("tenant-a", []string{"MSO"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		financialRecord := financial["3:MSO:2025"]
		if financialRecord.GrossIncome != "$510.00" ||
			financialRecord.TotalExpenses != "$50.00" ||
			financialRecord.NetIncome != "$460.00" ||
			financialRecord.TotalMileage != "0.00" {
			mt.Fatalf("unexpected financial report totals: %+v", financialRecord)
		}

		expenses, err := GetExpenseReports("tenant-a", []string{"MSO"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		expenseRecord := expenses["3:MSO:2025"]
		if expenseRecord.PayrollDeductions != "$30.00" || expenseRecord.TotalExpenses != "$50.00" {
			mt.Fatalf("unexpected expense report totals: %+v", expenseRecord)
		}

		financialPDF, err := emailReportTable(EmailReportRequest{ReportType: "financial"}, financial)
		if err != nil {
			mt.Fatal(err)
		}
		if financialPDF.note != "Payroll deductions are recorded when payments are entered and are included in Total Expenses. Net Income Before Taxes reflects those deductions." ||
			financialPDF.rows[0][1] != "$510.00" ||
			financialPDF.rows[0][2] != "$50.00" ||
			financialPDF.rows[0][3] != "$460.00" {
			mt.Fatalf("unexpected financial PDF output: %+v", financialPDF)
		}
		revenuePDF, err := emailReportTable(EmailReportRequest{ReportType: "revenue"}, revenue)
		if err != nil {
			mt.Fatal(err)
		}
		if revenuePDF.rows[0][6] != "$30.00" || revenuePDF.rows[0][7] != "$480.00" {
			mt.Fatalf("unexpected revenue PDF output: %+v", revenuePDF)
		}
	})
}
