package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ref-ledger-v2/internal/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestVerifySelectedGamesUsesOneScopedQuery(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("completed selection", func(mt *mtest.T) {
		oldClient, oldDB := Client, Database
		Client, Database = mt.Client, mt.DB.Name()
		defer func() { Client, Database = oldClient, oldDB }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, bson.D{{Key: "gameId", Value: int64(10)}}, bson.D{{Key: "gameId", Value: int64(30)}}))
		failures := VerifyGameStatus([]int64{10, 20, 30}, "MSO", "account-a")
		if len(failures) != 1 || !strings.Contains(failures[0].Error(), "game 20") {
			t.Fatalf("unexpected failures: %v", failures)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 1 || events[0].CommandName != "find" {
			t.Fatalf("expected a single find, got %d commands", len(events))
		}
		filter := events[0].Command.Lookup("filter").Document()
		if filter.Lookup("tenantId").StringValue() != "account-a" || filter.Lookup("association").StringValue() != "MSO" || filter.Lookup("status").StringValue() != "Completed" {
			t.Fatalf("unscoped filter: %s", filter)
		}
		ids, _ := filter.Lookup("gameId").Document().Lookup("$in").Array().Values()
		if len(ids) != 3 {
			t.Fatalf("expected all selected IDs: %s", filter)
		}
	})
}

func TestGameExistsIsScopedAndStopsAtFirstMatch(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("not found", func(mt *mtest.T) {
		oldClient, oldDB := Client, Database
		Client, Database = mt.Client, mt.DB.Name()
		defer func() { Client, Database = oldClient, oldDB }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch))
		exists, err := GameExists(model.GameDoc{GameId: 10, Association: "MSO", TenantId: "account-a"})
		if err != nil || exists {
			t.Fatalf("exists=%v err=%v", exists, err)
		}
		command := mt.GetStartedEvent().Command
		if command.Lookup("filter").Document().Lookup("tenantId").StringValue() != "account-a" {
			t.Fatalf("missing account scope: %s", command)
		}
		if command.Lookup("limit").Int64() != 1 {
			t.Fatalf("expected first-match limit: %s", command)
		}
	})
	mt.Run("database failure", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Message: "denied"}))
		exists, err := documentExists(context.Background(), mt.Coll, bson.M{})
		if exists || err == nil || errors.Is(err, mongo.ErrNoDocuments) {
			t.Fatalf("database error swallowed: %v %v", exists, err)
		}
	})
}

func TestMissingSelectedGamesPreservesSelectionOrder(t *testing.T) {
	failures := missingSelectedGames([]int64{4, 2, 4, 1}, map[int64]bool{2: true}, "MSO", true)
	if len(failures) != 3 || !strings.Contains(failures[0].Error(), "game 4") || !strings.Contains(failures[2].Error(), "game 1") || !strings.Contains(failures[0].Error(), "Completed Status") {
		t.Fatalf("unexpected errors: %v", failures)
	}
	if got := VerifyGameStatus(nil, "MSO", "account-a"); got != nil {
		t.Fatalf("empty selection should avoid database: %v", got)
	}
}

func TestInMemoryGameFiltersAreIndependent(t *testing.T) {
	a := BuildMongoGameFilter(model.GameFilter{TenantId: "account-a", GameId: []int64{10}, Home: "Team A", Date: &model.Date{From: "11/11/2026", To: "11/12/2026"}})
	b := BuildMongoGameFilter(model.GameFilter{TenantId: "account-b", GameId: []int64{20}})
	a["tenantId"] = "changed"
	if b["tenantId"] != "account-b" {
		t.Fatal("filter leaked between requests")
	}
	if a["home"] != "Team A" {
		t.Fatalf("home filter lost: %v", a)
	}
	if _, ok := b["home"]; ok {
		t.Fatal("home filter leaked")
	}
	if _, ok := a["$expr"]; !ok {
		t.Fatalf("date filter lost: %v", a)
	}
}
