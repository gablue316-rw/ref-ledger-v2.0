package database

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestPayrollDeductionReconciliationAnd1099UseDeposit(t *testing.T) {
	mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock)).Run("reports", func(mt *mtest.T) {
		oldClient, oldDatabase := Client, Database
		Client, Database = mt.Client, mt.DB.Name()
		defer func() { Client, Database = oldClient, oldDatabase }()

		payment := bson.D{
			{Key: "tenantId", Value: "tenant-a"},
			{Key: "association", Value: "MSO"},
			{Key: "paymentId", Value: "payment-1"},
			{Key: "paymentDate", Value: "1/2/2025"},
			{Key: "paymentAmt", Value: int64(48000)},
			{Key: "taxYear", Value: 2025},
			{Key: "gameIds", Value: []int64{42}},
			{Key: "deductionAmount", Value: int64(3000)},
			{Key: "deductionType", Value: "Equipment"},
		}
		game := bson.D{
			{Key: "tenantId", Value: "tenant-a"},
			{Key: "association", Value: "MSO"},
			{Key: "gameId", Value: int64(42)},
			{Key: "taxYear", Value: "2024"},
			{Key: "numOfGames", Value: int64(1)},
			{Key: "gameFee", Value: int64(50000)},
			{Key: "travelPay", Value: int64(2500)},
			{Key: "assignorFee", Value: int64(1500)},
			{Key: "deductions", Value: int64(10000)},
		}

		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".payments", mtest.FirstBatch, payment),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, game),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".payments", mtest.FirstBatch, payment),
		)

		reconciliation, err := GetReconciliationReports("tenant-a", []string{"MSO"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		reconciled := reconciliation["payment-1"]
		if reconciled.PaymentAmt != "$480.00" ||
			reconciled.CalculatedAmt != "$480.00" ||
			reconciled.Status != "Reconciled" {
			mt.Fatalf("unexpected reconciliation result: %+v", reconciled)
		}

		report1099, err := Get1099Reports("tenant-a", []string{"MSO"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		for _, record := range report1099 {
			if record.Amount != "480.00" {
				mt.Fatalf("1099 amount = %q, want deposited payment amount 480.00", record.Amount)
			}
			return
		}
		mt.Fatal("1099 report returned no payment")
	})
}
