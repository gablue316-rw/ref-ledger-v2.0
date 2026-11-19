package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMobileExpenseValidationAndStorageUnits(t *testing.T) {
	valid := MobileExpenseInput{Date: "2026-11-15", Type: "Equipment", Value: "7.25", Association: "MSO", TaxYear: "2026", GameID: "42"}
	doc, err := validateMobileExpense(valid)
	if err != nil || doc.Amount != 725 || doc.Mileage != 0 || doc.GameId != 42 || doc.Date != "11/15/2026" {
		t.Fatalf("unexpected conversion: %+v %v", doc, err)
	}
	miles := valid
	miles.Type = "Mileage"
	miles.Value = "83"
	doc, err = validateMobileExpense(miles)
	if err != nil || doc.Amount != 0 || doc.Mileage != 8300 {
		t.Fatalf("unexpected mileage: %+v %v", doc, err)
	}
	for _, change := range []func(*MobileExpenseInput){
		func(v *MobileExpenseInput) { v.Value = "-1.00" }, func(v *MobileExpenseInput) { v.Value = "7.256" },
		func(v *MobileExpenseInput) { v.Date = "1999-01-01" }, func(v *MobileExpenseInput) { v.Date = "2026-02-30" },
		func(v *MobileExpenseInput) { v.TaxYear = "2101" }, func(v *MobileExpenseInput) { v.Association = "" },
		func(v *MobileExpenseInput) { v.GameID = "-3" }, func(v *MobileExpenseInput) { v.Type = "unknown" },
		func(v *MobileExpenseInput) { v.Type = "Mileage"; v.Value = "8.5" }, func(v *MobileExpenseInput) { v.Value = "999999999999999999999.00" },
	} {
		in := valid
		change(&in)
		if _, err := validateMobileExpense(in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}
func TestMobileNewRoutesRequireBearerAndMaintenanceAdmission(t *testing.T) {
	for _, path := range []string{"/api/mobile/game", "/api/mobile/expense", "/api/mobile/expenses", "/api/mobile/access", "/api/mobile/catalog/create/site", "/api/mobile/catalog/activate/level"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			next := mobileAccountHandler(func(http.ResponseWriter, *http.Request) { t.Fatal("cookie authorized mobile action") })
			req := httptest.NewRequest(method, path+"?tenantId=other", nil)
			req.AddCookie(&http.Cookie{Name: "rl_session", Value: "cookie"})
			w := httptest.NewRecorder()
			next(w, req)
			if w.Code != 401 {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
}
func expenseTestRequest(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	ctx := context.WithValue(req.Context(), mobileAccountContextKey{}, "account-a")
	ctx = context.WithValue(ctx, mobileWriteContextKey{}, true)
	return req.WithContext(ctx)
}
func TestMobileExpensesScopeDatesAndConcurrency(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	stored := bson.D{{Key: "_id", Value: "one"}, {Key: "tenantId", Value: "account-a"}, {Key: "expenseId", Value: "EXP-1"}, {Key: "association", Value: "MSO"}, {Key: "date", Value: "11/15/2026"}, {Key: "type", Value: "Equipment"}, {Key: "amount", Value: int64(725)}, {Key: "taxYear", Value: "2026"}}
	raw, _ := bson.Marshal(stored)
	mt.Run("list scope and inclusive date range", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		outside := append(bson.D(nil), stored...)
		outside[4].Value = "2/1/2026"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, stored, outside))
		w := httptest.NewRecorder()
		MobileExpenses(w, expenseTestRequest("GET", "/api/mobile/expenses?tenantId=other&from=2026-11-15&through=2026-11-15&association=MSO", ""))
		if w.Code != 200 {
			mt.Fatalf("list: %d %s", w.Code, w.Body)
		}
		var result struct {
			Items []json.RawMessage `json:"items"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		if len(result.Items) != 1 {
			mt.Fatalf("date boundaries returned %d items", len(result.Items))
		}
		filter := mt.GetStartedEvent().Command.Lookup("filter").Document()
		if filter.Lookup("tenantId").StringValue() != "account-a" || filter.Lookup("association").StringValue() != "MSO" {
			mt.Fatalf("unscoped query %s", filter)
		}
	})
	mt.Run("stale version never deletes", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, stored))
		w := httptest.NewRecorder()
		MobileExpense(w, expenseTestRequest("DELETE", "/api/mobile/expense?expenseId=EXP-1&association=MSO", `{"version":"stale"}`))
		if w.Code != 409 || len(mt.GetAllStartedEvents()) != 1 {
			mt.Fatalf("stale delete %d %s", w.Code, w.Body)
		}
	})
	mt.Run("concurrent change rejects conditional delete", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, stored), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: int32(0)}))
		body, _ := json.Marshal(map[string]string{"version": expenseVersion(raw)})
		w := httptest.NewRecorder()
		MobileExpense(w, expenseTestRequest("DELETE", "/api/mobile/expense?expenseId=EXP-1&association=MSO", string(body)))
		if w.Code != 409 {
			mt.Fatalf("concurrent delete %d %s", w.Code, w.Body)
		}
		events := mt.GetAllStartedEvents()
		values, _ := events[1].Command.Lookup("deletes").Array().Values()
		predicate := values[0].Document().Lookup("q").Document()
		parts, _ := predicate.Lookup("$expr").Document().Lookup("$eq").Array().Values()
		literal := parts[1].Document().Lookup("$literal").Document()
		if !bytes.Equal(literal, raw) {
			mt.Fatalf("conditional delete lost original predicate: %s", predicate)
		}
	})
	mt.Run("type change clears old monetary value", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".expenses", mtest.FirstBatch, stored), mtest.CreateCursorResponse(0, mt.DB.Name()+".associations", mtest.FirstBatch, bson.D{{Key: "id", Value: "MSO"}}), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: int32(1)}, bson.E{Key: "nModified", Value: int32(1)}))
		body, _ := json.Marshal(map[string]interface{}{"version": expenseVersion(raw), "expense": MobileExpenseInput{Date: "2026-11-15", Type: "Mileage", Value: "83", Association: "MSO", TaxYear: "2026"}})
		w := httptest.NewRecorder()
		MobileExpense(w, expenseTestRequest("PUT", "/api/mobile/expense?expenseId=EXP-1&association=MSO", string(body)))
		if w.Code != 204 {
			mt.Fatalf("update: %d %s", w.Code, w.Body)
		}
		events := mt.GetAllStartedEvents()
		values, _ := events[2].Command.Lookup("updates").Array().Values()
		update := values[0].Document().Lookup("u").Document().Lookup("$set").Document()
		if update.Lookup("amount").Int64() != 0 || update.Lookup("mileage").Int64() != 8300 {
			mt.Fatalf("incorrect conversion %s", update)
		}
	})
}

func TestMobileGameDecimalFeesAreStoredAsCents(t *testing.T) {
	game := model.GameDescriptor{GameFee: "65.5", TravelPay: "1", AssignorFee: "$2.05"}
	if err := normalizeMobileGameMoney(&game); err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]int64{game.GameFee: 6550, game.TravelPay: 100, game.AssignorFee: 205} {
		got, err := utils.ConvertAmtStrToInt64(value)
		if err != nil || got != want {
			t.Fatalf("%s: %d != %d (%v)", value, got, want, err)
		}
	}
}
