package main

import (
	"bytes"
	"encoding/json"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"strings"
	"testing"
)

func TestWebGameDeleteUsesLoadedVersionAndAtomicPredicate(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	stored := bson.D{{Key: "_id", Value: "game"}, {Key: "gameId", Value: int64(7)}, {Key: "association", Value: "MSO"}, {Key: "tenantId", Value: "account-a"}, {Key: "status", Value: "Pending"}}
	raw, _ := bson.Marshal(stored)
	for _, test := range []struct {
		name, version string
		n             int32
		want          int
	}{{"missing", "", 0, 409}, {"stale", "stale", 0, 409}, {"changed during delete", database.RecordVersion(raw), 0, 409}, {"current", database.RecordVersion(raw), 1, 204}} {
		mt.Run(test.name, func(mt *mtest.T) {
			old := gc
			gc = database.GameCollection{Coll: mt.Coll}
			defer func() { gc = old }()
			oldClient, oldDB := database.Client, database.Database
			database.Client, database.Database = mt.Client, mt.DB.Name()
			defer func() { database.Client, database.Database = oldClient, oldDB }()
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, stored), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: test.n}), mtest.CreateSuccessResponse())
			req := expenseTestRequest("DELETE", "/api/deleteGame/MSO/7", "")
			req.SetPathValue("association", "MSO")
			req.SetPathValue("gameId", "7")
			req.Header.Set("If-Match", test.version)
			w := httptest.NewRecorder()
			DeleteGame(w, req)
			if w.Code != test.want {
				mt.Fatalf("%d %s", w.Code, w.Body)
			}
			for _, event := range mt.GetAllStartedEvents() {
				if event.CommandName == "find" {
					identity := event.Command.Lookup("filter").Document()
					if identity.Lookup("tenantId").StringValue() != "account-a" {
						mt.Fatal(identity)
					}
				}
				if event.CommandName == "delete" {
					if test.version == "" || test.version == "stale" {
						mt.Fatal("stale delete reached database")
					}
					arr, _ := event.Command.Lookup("deletes").Array().Values()
					query := arr[0].Document().Lookup("q").Document()
					parts, _ := query.Lookup("$expr").Document().Lookup("$eq").Array().Values()
					if !bytes.Equal(parts[1].Document().Lookup("$literal").Document(), raw) {
						mt.Fatal("lost snapshot")
					}
				}
			}
		})
	}
}

func TestBulkStatusRequiresEveryVersionBeforeWriting(t *testing.T) {
	req := expenseTestRequest("POST", "/api/games/status", `{"status":"Completed","games":[{"gameId":1,"association":"MSO","version":"token"},{"gameId":2,"association":"MSO"}]}`)
	w := httptest.NewRecorder()
	UpdateGameStatus(w, req)
	if w.Code != 409 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestSingleGameReturnsOriginalVersion(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("read token", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()
		stored := bson.D{{Key: "_id", Value: "game"}, {Key: "gameId", Value: int64(7)}, {Key: "association", Value: "MSO"}, {Key: "tenantId", Value: "account-a"}, {Key: "status", Value: "Pending"}, {Key: "officials", Value: bson.A{}}}
		raw, _ := bson.Marshal(stored)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, stored))
		req := expenseTestRequest("GET", "/api/game/MSO/7", "")
		req.SetPathValue("association", "MSO")
		req.SetPathValue("gameid", "7")
		w := httptest.NewRecorder()
		GetSingleGame(w, req)
		var result map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &result)
		if w.Code != 200 || result["version"] != database.RecordVersion(raw) {
			mt.Fatalf("%d %s", w.Code, w.Body)
		}
	})
}

func TestConflictMessageAndHeaderParsing(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/?version=query", strings.NewReader(""))
	req.Header.Set("If-Match", `"snapshot"`)
	if requestVersion(req) != "snapshot" {
		t.Fatal("If-Match ignored")
	}
	w := httptest.NewRecorder()
	if !writeConflict(w, database.ErrConflict) || w.Code != 409 {
		t.Fatal(w.Code)
	}
}
