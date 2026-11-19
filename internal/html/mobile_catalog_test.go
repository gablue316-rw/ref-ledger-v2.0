package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMobileCatalogUsesAccountCollections(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("account scope and web dropdown values", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".users", mtest.FirstBatch, bson.D{{Key: "sports", Value: bson.A{"Softball", "Basketball", "Softball"}}, {Key: "passwordHash", Value: "not-for-response"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".sports", mtest.FirstBatch, bson.D{{Key: "name", Value: "Softball"}, {Key: "active", Value: true}}, bson.D{{Key: "name", Value: "Basketball"}, {Key: "active", Value: true}}, bson.D{{Key: "name", Value: "Volleyball"}, {Key: "active", Value: true}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".associations", mtest.FirstBatch, bson.D{{Key: "id", Value: "MSO"}, {Key: "name", Value: "Softball Association"}, {Key: "assignors", Value: " Jane Smith, John Doe,Jane Smith "}, {Key: "phone", Value: "private-contact"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".sites", mtest.FirstBatch, bson.D{{Key: "name", Value: "Central"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".levels", mtest.FirstBatch, bson.D{{Key: "name", Value: "Varsity"}, {Key: "sport", Value: "Softball"}, {Key: "active", Value: true}, {Key: "gameFee", Value: int64(6550)}}),
		)
		w := httptest.NewRecorder()
		MobileCatalog(w, expenseTestRequest("GET", "/api/mobile/catalog?tenantId=other", ""))
		if w.Code != 200 {
			mt.Fatalf("%d: %s", w.Code, w.Body)
		}
		var result struct {
			Sports       []string                  `json:"sports"`
			Available    []string                  `json:"availableSports"`
			Associations []mobileAssociationOption `json:"associations"`
			Sites        []string                  `json:"sites"`
			Levels       []struct {
				Sport   string `json:"sport"`
				GameFee string `json:"gameFee"`
			} `json:"levels"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(result.Sports, []string{"Basketball", "Softball"}) || !reflect.DeepEqual(result.Available, []string{"Volleyball"}) || !reflect.DeepEqual(result.Associations[0].Assignors, []string{"Jane Smith", "John Doe"}) || result.Levels[0].GameFee != "65.50" {
			mt.Fatalf("unexpected catalog: %s", w.Body)
		}
		events := mt.GetAllStartedEvents()
		for i, event := range events {
			if i == 1 {
				if !event.Command.Lookup("filter").Document().Lookup("active").Boolean() {
					mt.Fatalf("inactive global sport query %s", event.Command)
				}
				continue
			}
			if event.Command.Lookup("filter").Document().Lookup("tenantId").StringValue() != "account-a" {
				mt.Fatalf("unscoped query %s", event.Command)
			}
		}
		if !events[4].Command.Lookup("filter").Document().Lookup("active").Boolean() {
			mt.Fatal("inactive levels offered")
		}
	})
}
func TestMobileCatalogRejectsWebCookieAndWrites(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/api/mobile/catalog", nil)
		req.AddCookie(&http.Cookie{Name: "rl_session", Value: "web-cookie"})
		w := httptest.NewRecorder()
		mobileAccountHandler(mobileReadOnly(MobileCatalog))(w, req)
		if w.Code != 401 {
			t.Fatalf("%s: status %d", method, w.Code)
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		mobileReadOnly(MobileCatalog)(w, httptest.NewRequest(method, "/api/mobile/catalog", nil))
		if w.Code != 405 {
			t.Fatalf("write accepted: %d", w.Code)
		}
	}
}
