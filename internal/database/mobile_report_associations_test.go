package database

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMobileReportsAllAssociationsMatchExplicitSelection(t *testing.T) {
	mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock)).Run("reports", func(mt *mtest.T) {
		oldClient, oldDatabase := Client, Database
		Client, Database = mt.Client, mt.DB.Name()
		defer func() { Client, Database = oldClient, oldDatabase }()

		addCursor := func(collection string, records ...bson.D) {
			mt.AddMockResponses(mtest.CreateCursorResponse(
				0,
				mt.DB.Name()+"."+collection,
				mtest.FirstBatch,
				records...,
			))
		}
		associations := []bson.D{
			{{Key: "id", Value: "MSO"}, {Key: "name", Value: "Metro Sports"}, {Key: "tenantId", Value: "tenant-a"}},
			{{Key: "id", Value: "GOLLC"}, {Key: "name", Value: "Greater Officials League"}, {Key: "tenantId", Value: "tenant-a"}},
		}
		paidGames := []bson.D{
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "MSO"}, {Key: "taxYear", Value: "2025"}, {Key: "status", Value: "Paid"}, {Key: "numOfGames", Value: int64(1)}, {Key: "gameFee", Value: int64(10000)}, {Key: "travelPay", Value: int64(1000)}, {Key: "assignorFee", Value: int64(500)}},
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "GOLLC"}, {Key: "taxYear", Value: "2025"}, {Key: "status", Value: "Paid"}, {Key: "numOfGames", Value: int64(2)}, {Key: "gameFee", Value: int64(20000)}, {Key: "travelPay", Value: int64(0)}, {Key: "assignorFee", Value: int64(1000)}},
		}
		payrollExpenses := []bson.D{
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "GOLLC"}, {Key: "taxYear", Value: "2025"}, {Key: "type", Value: "Equipment"}, {Key: "amount", Value: int64(500)}, {Key: "source", Value: PayrollDeductionSource}, {Key: "paymentRef", Value: "payment-1"}},
		}
		financialExpenses := []bson.D{
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "MSO"}, {Key: "taxYear", Value: "2025"}, {Key: "type", Value: "Food"}, {Key: "amount", Value: int64(1000)}},
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "GOLLC"}, {Key: "taxYear", Value: "2025"}, {Key: "type", Value: "Equipment"}, {Key: "amount", Value: int64(2000)}},
			payrollExpenses[0],
		}
		completedGames := []bson.D{
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "MSO"}, {Key: "status", Value: "Completed"}, {Key: "gameId", Value: int64(1)}, {Key: "numOfGames", Value: int64(1)}, {Key: "gameFee", Value: int64(10000)}, {Key: "travelPay", Value: int64(1000)}, {Key: "assignorFee", Value: int64(500)}},
			{{Key: "tenantId", Value: "tenant-a"}, {Key: "association", Value: "GOLLC"}, {Key: "status", Value: "Completed"}, {Key: "gameId", Value: int64(2)}, {Key: "numOfGames", Value: int64(2)}, {Key: "gameFee", Value: int64(20000)}, {Key: "travelPay", Value: int64(0)}, {Key: "assignorFee", Value: int64(1000)}},
		}
		addPaidRevenueResponses := func(includeAssociations bool) {
			if includeAssociations {
				addCursor("associations", associations...)
			}
			addCursor("games", paidGames...)
			addCursor("expenses", payrollExpenses...)
		}
		addFinancialResponses := func(includeAssociations bool) {
			if includeAssociations {
				addCursor("associations", associations...)
			}
			addCursor("games", paidGames...)
			addCursor("expenses", financialExpenses...)
		}
		addExpenseResponses := func(includeAssociations bool) {
			if includeAssociations {
				addCursor("associations", associations...)
			}
			addCursor("expenses", financialExpenses...)
		}
		addReceivableResponses := func(includeAssociations bool) {
			if includeAssociations {
				addCursor("associations", associations...)
			}
			addCursor("games", completedGames...)
		}

		addPaidRevenueResponses(true)
		addPaidRevenueResponses(false)
		allRevenue, err := GetRevenueReports("tenant-a", nil, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		explicitRevenue, err := GetRevenueReports("tenant-a", []string{"MSO", "GOLLC"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(allRevenue, explicitRevenue) || len(allRevenue) != 2 {
			mt.Fatalf("all-association revenue %v differs from explicit-all %v", allRevenue, explicitRevenue)
		}

		addFinancialResponses(true)
		addFinancialResponses(false)
		allFinancial, err := GetFinancialReports("tenant-a", nil, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		explicitFinancial, err := GetFinancialReports("tenant-a", []string{"MSO", "GOLLC"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(allFinancial, explicitFinancial) || len(allFinancial) != 2 {
			mt.Fatalf("all-association financial %v differs from explicit-all %v", allFinancial, explicitFinancial)
		}

		addExpenseResponses(true)
		addExpenseResponses(false)
		allExpenses, err := GetExpenseReports("tenant-a", nil, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		explicitExpenses, err := GetExpenseReports("tenant-a", []string{"MSO", "GOLLC"}, "2025")
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(allExpenses, explicitExpenses) || len(allExpenses) != 2 {
			mt.Fatalf("all-association expenses %v differs from explicit-all %v", allExpenses, explicitExpenses)
		}

		addReceivableResponses(true)
		addReceivableResponses(false)
		allReceivables, err := GetAccountsReceivableReport("tenant-a", nil)
		if err != nil {
			mt.Fatal(err)
		}
		explicitReceivables, err := GetAccountsReceivableReport("tenant-a", []string{"MSO", "GOLLC"})
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(allReceivables, explicitReceivables) || len(allReceivables) != 2 {
			mt.Fatalf("all-association receivables %v differs from explicit-all %v", allReceivables, explicitReceivables)
		}

		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName != "find" {
				continue
			}
			filter := event.Command.Lookup("filter").Document()
			if filter.Lookup("tenantId").StringValue() != "tenant-a" {
				mt.Fatalf("%s query is not tenant-scoped: %s", event.Command.Lookup("find").StringValue(), filter)
			}
		}
	})
}
