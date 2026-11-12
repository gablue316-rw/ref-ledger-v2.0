package testcatalog

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"testing"
)

func TestActiveRolesFiltersAndSortsWithoutMutatingInput(t *testing.T) {
	request := model.OfficialRolesRequest{Sport: "Softball", Roles: []model.OfficialRole{{RoleId: "u1", Active: true, DisplayOrder: 2}, {RoleId: "inactive", Active: false, DisplayOrder: 0}, {RoleId: "plate", Active: true, DisplayOrder: 1}}}
	got := ActiveRoles(request)
	if len(got.Roles) != 2 || got.Roles[0].RoleId != "plate" || got.Roles[1].RoleId != "u1" {
		t.Fatalf("unexpected active roles: %v", got)
	}
	if request.Roles[0].RoleId != "u1" {
		t.Fatal("mutated source catalog")
	}
}
func TestSeedCatalogUsesGlobalScopeAndLiteralRoles(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("sport", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}))
		if err := SeedSport(context.Background(), mt.DB, database.Sport{ID: "Softball", Name: "Softball", Active: true}); err != nil {
			t.Fatal(err)
		}
		var command struct {
			Updates []struct {
				Query  bson.M `bson:"q"`
				Upsert bool   `bson:"upsert"`
			} `bson:"updates"`
		}
		bson.Unmarshal(mt.GetStartedEvent().Command, &command)
		q := command.Updates[0].Query
		if q["id"] != "Softball" || q["tenantId"].(bson.M)["$exists"] != false || !command.Updates[0].Upsert {
			t.Fatalf("incorrect catalog scope: %v", q)
		}
	})
	mt.Run("role catalog", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}))
		err := SeedOfficialRoles(context.Background(), mt.DB, model.OfficialRolesRequest{Sport: "Softball", Roles: []model.OfficialRole{{RoleId: "plate", Title: "$Plate", Active: true}}})
		if err != nil {
			t.Fatal(err)
		}
		updates, _ := mt.GetStartedEvent().Command.Lookup("updates").Array().Values()
		if len(updates) != 1 {
			t.Fatal("expected one atomic catalog update")
		}
		entry := updates[0].Document()
		if entry.Lookup("q").Document().Lookup("_id").StringValue() != "load-test-catalog" {
			t.Fatal("unexpected catalog identity")
		}
		if entry.Lookup("u").Type != 4 {
			t.Fatal("roles update must use an atomic aggregation pipeline")
		}
	})
}
