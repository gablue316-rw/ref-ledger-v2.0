package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func catalogRequest(method, kind, id, body string) *http.Request {
	r := expenseTestRequest(method, "/api/mobile/catalog/records/"+kind+"?id="+id+"&tenantId=other", body)
	r.SetPathValue("type", kind)
	return r
}

func TestMobileCatalogRecordScopeAndCursor(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, kind := range []string{"official", "association", "site"} {
		mt.Run(kind, func(mt *mtest.T) {
			old, db := database.Client, database.Database
			database.Client, database.Database = mt.Client, mt.DB.Name()
			defer func() { database.Client, database.Database = old, db }()
			collection := map[string]string{"official": "officials", "association": "associations", "site": "sites"}[kind]
			first := bson.D{{Key: "_id", Value: "one"}, {Key: "tenantId", Value: "account-a"}, {Key: "id", Value: int64(1)}, {Key: "firstName", Value: "Ann"}, {Key: "lastName", Value: "Smith"}, {Key: "name", Value: "One"}}
			second := bson.D{{Key: "_id", Value: "two"}, {Key: "tenantId", Value: "account-a"}, {Key: "id", Value: int64(2)}, {Key: "firstName", Value: "Bob"}, {Key: "lastName", Value: "Smith"}, {Key: "name", Value: "Two"}}
			if kind == "association" {
				first[2].Value, second[2].Value = "A", "B"
			}
			mt.AddMockResponses(mtest.CreateCursorResponse(123, mt.DB.Name()+"."+collection, mtest.FirstBatch, first), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.NextBatch, second))
			w := httptest.NewRecorder()
			MobileCatalogRecords(w, catalogRequest("GET", kind, "", ""))
			var items []map[string]interface{}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || len(items) != 2 || items[0]["version"] == "" {
				mt.Fatalf("list: %d %s", w.Code, w.Body)
			}
			if _, leaked := items[0]["TenantId"]; leaked {
				mt.Fatal("tenant ID exposed")
			}
			events := mt.GetAllStartedEvents()
			if events[0].Command.Lookup("filter").Document().Lookup("tenantId").StringValue() != "account-a" || events[1].CommandName != "getMore" {
				mt.Fatalf("unscoped or incomplete cursor: %v", events)
			}
		})
	}
}

func TestMobileCatalogForeignIDsAndStaleVersions(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, kind := range []string{"official", "association", "site"} {
		for _, method := range []string{"PUT", "DELETE"} {
			mt.Run(kind+method, func(mt *mtest.T) {
				old, db := database.Client, database.Database
				database.Client, database.Database = mt.Client, mt.DB.Name()
				defer func() { database.Client, database.Database = old, db }()
				collection := map[string]string{"official": "officials", "association": "associations", "site": "sites"}[kind]
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch))
				w := httptest.NewRecorder()
				MobileCatalogRecords(w, catalogRequest(method, kind, "123", `{"version":"foreign"}`))
				events := mt.GetAllStartedEvents()
				if w.Code != 404 || len(events) != 1 || events[0].Command.Lookup("filter").Document().Lookup("tenantId").StringValue() != "account-a" {
					mt.Fatalf("foreign record: %d %s", w.Code, w.Body)
				}
			})
		}
	}
	mt.Run("stale site edit", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".sites", mtest.FirstBatch, bson.D{{Key: "_id", Value: "one"}, {Key: "tenantId", Value: "account-a"}, {Key: "name", Value: "Site"}}))
		w := httptest.NewRecorder()
		MobileCatalogRecords(w, catalogRequest("PUT", "site", "Site", `{"version":"stale","name":"Site"}`))
		if w.Code != 409 || len(mt.GetAllStartedEvents()) != 1 {
			mt.Fatalf("stale edit: %d %s", w.Code, w.Body)
		}
	})
}

func TestMobileCatalogOfficialDeletionBlockedForLegacyID(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("assigned official", func(mt *mtest.T) {
		old, db, oldOC := database.Client, database.Database, oc
		database.Client, database.Database = mt.Client, mt.DB.Name()
		oc = database.OfficialCollection{Coll: mt.DB.Collection("officials")}
		defer func() { database.Client, database.Database, oc = old, db, oldOC }()
		doc := bson.D{{Key: "_id", Value: "one"}, {Key: "tenantId", Value: "account-a"}, {Key: "officialId", Value: int64(42)}, {Key: "firstName", Value: "Ann"}, {Key: "lastName", Value: "Smith"}}
		raw, _ := bson.Marshal(doc)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".officials", mtest.FirstBatch, doc), mtest.CreateCursorResponse(0, mt.DB.Name()+".officials", mtest.FirstBatch, doc), mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, bson.D{{Key: "_id", Value: "game"}}))
		body, _ := json.Marshal(map[string]string{"version": database.RecordVersion(raw)})
		w := httptest.NewRecorder()
		MobileCatalogRecords(w, catalogRequest("DELETE", "official", "42", string(body)))
		events := mt.GetAllStartedEvents()
		if w.Code != 400 || !strings.Contains(w.Body.String(), "assigned to a game") || len(events) != 3 || events[2].Command.Lookup("filter").Document().Lookup("officials.officialId").StringValue() != "42" {
			mt.Fatalf("blocked deletion: %d %s", w.Code, w.Body)
		}
	})
}

func TestMobileCatalogPermissionsAndDuplicate(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if mobileMethodAllowed("readonly", method) {
			t.Fatalf("read-only allowed %s", method)
		}
	}
	if !mobileMethodAllowed("readonly", "GET") {
		t.Fatal("read-only list rejected")
	}
	w := httptest.NewRecorder()
	mobileCatalogWriteError(w, errors.New("site already exists"))
	if w.Code != 409 {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
}

func TestMobileCatalogUpdatePreservesIdentityAndCreateDuplicate(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("official edit preserves legacy id", func(mt *mtest.T) {
		old, db := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = old, db }()
		doc := bson.D{{Key: "_id", Value: "one"}, {Key: "tenantId", Value: "account-a"}, {Key: "officialId", Value: int64(42)}, {Key: "firstName", Value: "Ann"}, {Key: "lastName", Value: "Smith"}}
		raw, _ := bson.Marshal(doc)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".officials", mtest.FirstBatch, doc), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: int32(1)}, bson.E{Key: "nModified", Value: int32(1)}))
		body, _ := json.Marshal(map[string]string{"version": database.RecordVersion(raw), "firstName": "Ann", "lastName": "Smith", "phone": "555"})
		w := httptest.NewRecorder()
		MobileCatalogRecords(w, catalogRequest("PUT", "official", "42", string(body)))
		if w.Code != 204 {
			mt.Fatalf("edit: %d %s", w.Code, w.Body)
		}
		events := mt.GetAllStartedEvents()
		updates, _ := events[1].Command.Lookup("updates").Array().Values()
		update := updates[0].Document().Lookup("u").Document().Lookup("$set").Document()
		if update.Lookup("officialId").Type != 0 || update.Lookup("id").Type != 0 || update.Lookup("phone").StringValue() != "555" {
			mt.Fatalf("edit replaced official identity: %s", update)
		}
	})
	mt.Run("duplicate association create", func(mt *mtest.T) {
		old := ac
		ac = database.AssociationCollection{Coll: mt.DB.Collection("associations")}
		defer func() { ac = old }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".associations", mtest.FirstBatch, bson.D{{Key: "id", Value: "ABC"}, {Key: "tenantId", Value: "account-a"}}))
		req := expenseTestRequest("POST", "/api/mobile/catalog/create/association", `{"id":"ABC","name":"Example"}`)
		req.SetPathValue("type", "association")
		w := httptest.NewRecorder()
		MobileCatalogCreate(w, req)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "already exists") {
			mt.Fatalf("duplicate create: %d %s", w.Code, w.Body)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 1 || events[0].Command.Lookup("filter").Document().Lookup("tenantId").StringValue() != "account-a" {
			mt.Fatalf("duplicate query scope: %v", events)
		}
	})
}
