package main

import (
	"encoding/json"
	"net/http/httptest"
	"ref-ledger-v2/internal/database"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestCompletedGamesDashboardQuery(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("tenant and completed status", func(mt *mtest.T) {
		oldClient, oldDB := database.Client, database.Database
		database.Client, database.Database = mt.Client, mt.DB.Name()
		defer func() { database.Client, database.Database = oldClient, oldDB }()

		game := bson.D{
			{Key: "gameId", Value: int64(101)}, {Key: "tenantId", Value: "account-a"},
			{Key: "status", Value: "Completed"}, {Key: "association", Value: "A"},
			{Key: "date", Value: "11/19/2026"}, {Key: "time", Value: "1:00 PM"},
			{Key: "numOfGames", Value: int64(1)}, {Key: "gameFee", Value: int64(7500)},
			{Key: "officials", Value: bson.A{}},
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".games", mtest.FirstBatch, game))
		w := httptest.NewRecorder()
		GetGames(w, expenseTestRequest("GET", "/api/dashboard?status=Completed&association=A&tenantId=other", ""))
		if w.Code != 200 {
			mt.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var games []struct {
			GameID        int64  `json:"GameId"`
			Status        string `json:"Status"`
			TotalEarnings string `json:"TotalEarnings"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &games); err != nil {
			mt.Fatal(err)
		}
		if len(games) != 1 || games[0].GameID != 101 || games[0].Status != "Completed" || games[0].TotalEarnings != "75.00" {
			mt.Fatalf("unexpected games: %s", w.Body)
		}
		event := mt.GetStartedEvent()
		filter := event.Command.Lookup("filter").Document()
		statuses, err := filter.Lookup("status").Document().Lookup("$in").Array().Values()
		if err != nil || len(statuses) != 1 || statuses[0].StringValue() != "Completed" {
			mt.Fatalf("status filter: %s", filter)
		}
		associations, err := filter.Lookup("association").Document().Lookup("$in").Array().Values()
		if err != nil || len(associations) != 1 || associations[0].StringValue() != "A" || filter.Lookup("tenantId").StringValue() != "account-a" {
			mt.Fatalf("tenant or association filter: %s", filter)
		}
	})
}
