package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"ref-ledger-v2/internal/model"
	"sync"
	"testing"
	"time"
)

func TestRecordVersionsAreReadOnlyAndConsistent(t *testing.T) {
	raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: "r"}, {Key: "tenantId", Value: "a"}, {Key: "notes", Value: "$literal data"}})
	for _, v := range []interface{}{&model.GameDoc{}, &model.HtmlResponse{}, &model.ExpenseDoc{}, &model.PaymentDoc{}, &AssociationDoc{}, &SiteDoc{}, &OfficialDoc{}, &ExpenseDoc{}, &Level{}} {
		if err := bson.Unmarshal(raw, v); err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(wire, []byte(RecordVersion(raw))) {
			t.Fatalf("%T missing version: %s", v, wire)
		}
		stored, err := bson.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if bson.Raw(stored).Lookup("version").Type != 0 {
			t.Fatalf("%T persists client token", v)
		}
	}
}

func TestConditionalFilterRejectsMissingStaleAndRemoved(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, version := range []string{"", "stale", "removed"} {
		mt.Run(version, func(mt *mtest.T) {
			doc := bson.D{{Key: "_id", Value: "row"}, {Key: "tenantId", Value: "account-a"}, {Key: "value", Value: 1}}
			if version == "removed" {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch))
			} else {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, doc))
			}
			_, err := ConditionalFilter(context.Background(), mt.Coll, bson.M{"tenantId": "account-a"}, version)
			if !errors.Is(err, ErrConflict) {
				mt.Fatal(err)
			}
			events := mt.GetAllStartedEvents()
			if version == "" && len(events) != 0 {
				mt.Fatal("missing token queried database")
			}
			for _, event := range events {
				if event.CommandName != "find" {
					mt.Fatal("stale token attempted mutation")
				}
			}
		})
	}
}

func TestLevelReadsIncludeRecordVersion(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("level versions match stored document", func(mt *mtest.T) {
		doc := bson.D{
			{Key: "_id", Value: "level-row"},
			{Key: "id", Value: "level-1"},
			{Key: "tenantId", Value: "account-a"},
			{Key: "sport", Value: "Softball"},
			{Key: "name", Value: "Varsity"},
			{Key: "gameFee", Value: int64(6500)},
			{Key: "active", Value: false},
		}
		raw, err := bson.Marshal(doc)
		if err != nil {
			mt.Fatal(err)
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, doc))
		levels, err := (&LevelsCollection{Coll: mt.Coll}).GetLevels("account-a", false, "Softball")
		if err != nil {
			mt.Fatal(err)
		}
		if len(levels) != 1 || levels[0].Version != RecordVersion(raw) {
			mt.Fatalf("unexpected level version: %+v", levels)
		}
	})
}

// Opt-in integration suite: run only against a disposable local replica set.
// A uniquely named database is created and dropped; no application data is used.
func TestOptimisticConcurrencyIntegration(t *testing.T) {
	uri := os.Getenv("CONCURRENCY_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set CONCURRENCY_TEST_MONGODB_URI to a disposable replica set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	oldClient, oldDB := Client, Database
	Client, Database = client, fmt.Sprintf("concurrency_test_%d", time.Now().UnixNano())
	defer func() { client.Database(Database).Drop(context.Background()); Client, Database = oldClient, oldDB }()
	db := client.Database(Database)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	insert := func(coll string, doc interface{}) {
		t.Helper()
		_, err := db.Collection(coll).InsertOne(ctx, doc)
		must(err)
	}
	t.Run("atomic writers and newly added fields", func(t *testing.T) {
		coll := db.Collection("records")
		insert("records", bson.M{"_id": "one", "tenantId": "a", "value": 0})
		raw, err := coll.FindOne(ctx, bson.M{"_id": "one"}).Raw()
		must(err)
		version := RecordVersion(raw)
		f1, err := ConditionalFilter(ctx, coll, bson.M{"tenantId": "a"}, version)
		must(err)
		f2, err := ConditionalFilter(ctx, coll, bson.M{"tenantId": "a"}, version)
		must(err)
		var wg sync.WaitGroup
		counts := make(chan int64, 2)
		errs := make(chan error, 2)
		for i, f := range []bson.M{f1, f2} {
			wg.Add(1)
			go func(i int, f bson.M) {
				defer wg.Done()
				r, e := coll.UpdateOne(ctx, f, bson.M{"$set": bson.M{"value": i + 1}})
				if e != nil {
					errs <- e
					return
				}
				counts <- r.MatchedCount
			}(i, f)
		}
		wg.Wait()
		close(counts)
		close(errs)
		for err := range errs {
			must(err)
		}
		var total int64
		for n := range counts {
			total += n
		}
		if total != 1 {
			t.Fatalf("%d writers succeeded", total)
		}
		raw, err = coll.FindOne(ctx, bson.M{"_id": "one"}).Raw()
		must(err)
		_, err = coll.UpdateOne(ctx, bson.M{"_id": "one"}, bson.M{"$set": bson.M{"newField": "$data"}})
		must(err)
		result, err := coll.DeleteOne(ctx, SnapshotFilter(raw))
		must(err)
		if result.DeletedCount != 0 {
			t.Fatal("new field escaped snapshot comparison")
		}
		if _, err := ConditionalFilter(ctx, coll, bson.M{"tenantId": "other"}, version); !errors.Is(err, ErrConflict) {
			t.Fatal("tenant boundary", err)
		}
	})
	t.Run("catalog edit and stale delete", func(t *testing.T) {
		insert("associations", bson.M{"id": "MSO", "name": "Original", "tenantId": "a"})
		ac := AssociationCollection{Coll: db.Collection("associations")}
		a, err := ac.Get("MSO", "a")
		must(err)
		old := a.Version
		a.Name = "Updated"
		must(ac.Update(*a, "a"))
		if err := ac.Delete("MSO", "a", old); !errors.Is(err, ErrConflict) {
			t.Fatal("stale delete", err)
		}
		fresh, err := ac.Get("MSO", "a")
		must(err)
		must(ac.Delete("MSO", "a", fresh.Version))
	})
	t.Run("mobile and web use the same game snapshot", func(t *testing.T) {
		insert("games", bson.M{"gameId": int64(99), "association": "MSO", "tenantId": "a", "status": "Pending", "notes": "old"})
		snap, err := LoadMobileGame(ctx, "a", "MSO", 99)
		must(err)
		gc := GameCollection{Coll: db.Collection("games")}
		_, err = db.Collection("games").UpdateOne(ctx, bson.M{"gameId": int64(99)}, bson.M{"$set": bson.M{"notes": "web update"}})
		must(err)
		if err := WriteMobileGame(ctx, snap, nil, snap.Version); !errors.Is(err, ErrMobileConflict) {
			t.Fatal("stale mobile delete", err)
		}
		if err := gc.Delete("MSO", "99", "a", snap.Version); !errors.Is(err, ErrConflict) {
			t.Fatal("stale web delete", err)
		}
		snap, err = LoadMobileGame(ctx, "a", "MSO", 99)
		must(err)
		must(gc.Delete("MSO", "99", "a", snap.Version))
	})
	t.Run("web saves detect a mobile edit", func(t *testing.T) {
		insert("roles", bson.M{"officialRoles": bson.A{bson.M{"sport": "Softball", "roles": bson.A{bson.M{"roleId": "plate", "title": "Plate", "displayOrder": 1, "active": true}}}}})
		insert("games", model.GameDoc{GameId: 88, Association: "MSO", TenantId: "a", Date: "11/16/2026", Time: "7:00 PM", Sport: "Softball", Status: "Pending", NumOfGames: 1, GameFee: 5000, Officials: []model.GameOfficialDoc{{RoleId: "plate", Title: "Plate", DisplayOrder: 1, Name: "Unassigned"}}})
		game, err := GetSingleGame(ctx, "88", "MSO", "a")
		must(err)
		stale := game
		game.Notes = "first web save"
		must(UpdateOneGameDoc(ctx, game, Database, "games", "a"))
		stale.Notes = "stale form"
		if err := UpdateOneGameDoc(ctx, stale, Database, "games", "a"); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		snap, err := LoadMobileGame(ctx, "a", "MSO", 88)
		must(err)
		mobile := snap.Game
		mobile.Notes = "mobile save"
		must(WriteMobileGame(ctx, snap, &mobile, snap.Version))
		if err := UpdateOneGameDoc(ctx, snap.Game, Database, "games", "a"); !errors.Is(err, ErrConflict) {
			t.Fatal("stale web form overwrote mobile", err)
		}
	})
	t.Run("site official expense and level paths", func(t *testing.T) {
		insert("sites", bson.M{"name": "Park", "tenantId": "a"})
		sc := SiteCollection{Coll: db.Collection("sites")}
		site, err := sc.Get("Park", "a")
		must(err)
		site.Contact = "New"
		must(sc.Update(*site, "a"))
		if err := sc.Delete("Park", "a", site.Version); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		insert("officials", bson.M{"id": int64(1), "firstName": "Jane", "lastName": "Doe", "tenantId": "a"})
		oc := OfficialCollection{Coll: db.Collection("officials")}
		official, err := oc.Get("Jane", "Doe", "a")
		must(err)
		official.Phone = "123"
		must(oc.Update("a", official))
		if err := oc.Delete("Jane", "Doe", "a", official.Version); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		insert("expenses", bson.M{"expenseId": "E1", "association": "MSO", "tenantId": "a", "date": "11/16/2026", "type": "Equipment", "amount": int64(725)})
		raw, err := db.Collection("expenses").FindOne(ctx, bson.M{"expenseId": "E1"}).Raw()
		must(err)
		ec := ExpensesCollection{Coll: db.Collection("expenses")}
		must(ec.Update(Expense{Version: RecordVersion(raw), Date: "2026-11-16", Type: "Equipment", Amount: "8.25", TaxYear: "2026"}, "E1", "MSO", "a"))
		if err := ec.Delete("E1", "MSO", "a", RecordVersion(raw)); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		insert("sports", bson.M{"name": "Softball", "active": true})
		insert("levels", bson.M{"id": "L1", "name": "Varsity", "sport": "Softball", "gameFee": int64(5000), "active": true, "tenantId": "a"})
		var level Level
		must(db.Collection("levels").FindOne(ctx, bson.M{"id": "L1"}).Decode(&level))
		lc := LevelsCollection{DB: db, Coll: db.Collection("levels")}
		must(lc.Deactivate(level, "a"))
		if err := lc.Activate(level, "a"); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		must(db.Collection("levels").FindOne(ctx, bson.M{"id": "L1"}).Decode(&level))
		level.GameFee = 5500
		must(lc.Update(level, "a"))
		if err := lc.Delete("L1", "a", level.Version); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	})

	t.Run("payment snapshot and transaction rollback", func(t *testing.T) {
		insert("games", bson.M{"gameId": int64(77), "association": "MSO", "tenantId": "a", "status": "Paid", "numOfGames": int64(1), "gameFee": int64(5000), "notes": ""})
		insert("payments", bson.M{"paymentId": "P1", "association": "MSO", "tenantId": "a", "paymentDate": "11/16/2026", "paymentAmt": int64(5000), "gameIds": bson.A{int64(77)}, "taxYear": 2026})
		records, err := GetPaymentRegistry(model.PaymentRegistryFilter{TenantId: "a"})
		must(err)
		if len(records) != 1 {
			t.Fatal(records)
		}
		token := records[0].Version
		if err := MutatePayment(ctx, "a", "P1", "MSO", "stale", nil); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		var game model.GameDoc
		must(db.Collection("games").FindOne(ctx, bson.M{"gameId": int64(77)}).Decode(&game))
		if game.Status != "Paid" {
			t.Fatal("stale payment changed game")
		}
		invalid := records[0]
		invalid.PaymentAmt = "1.00"
		if err := MutatePayment(ctx, "a", "P1", "MSO", token, &invalid); err == nil {
			t.Fatal("invalid payment accepted")
		}
		must(db.Collection("games").FindOne(ctx, bson.M{"gameId": int64(77)}).Decode(&game))
		if game.Status != "Paid" {
			t.Fatal("failed transaction changed game")
		}
		change := records[0]
		change.PaymentDate = "11/15/2026"
		must(MutatePayment(ctx, "a", "P1", "MSO", token, &change))
		if err := MutatePayment(ctx, "a", "P1", "MSO", token, nil); !errors.Is(err, ErrConflict) {
			t.Fatal("stale payment delete", err)
		}
		records, err = GetPaymentRegistry(model.PaymentRegistryFilter{TenantId: "a"})
		must(err)
		must(MutatePayment(ctx, "a", "P1", "MSO", records[0].Version, nil))
		must(db.Collection("games").FindOne(ctx, bson.M{"gameId": int64(77)}).Decode(&game))
		if game.Status != "Completed" {
			t.Fatal(game.Status)
		}
	})
}
