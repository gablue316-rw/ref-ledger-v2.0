// Package testcatalog supplies catalog setup for standalone load generators.
// It is test tooling, not a production administration API.
package testcatalog

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"sort"
	"strings"
)

func SeedSport(ctx context.Context, db *mongo.Database, sport database.Sport) error {
	if db == nil {
		return fmt.Errorf("test database is required")
	}
	if strings.TrimSpace(sport.ID) == "" || strings.TrimSpace(sport.Name) == "" {
		return fmt.Errorf("sport ID and name are required")
	}
	_, err := db.Collection("sports").UpdateOne(ctx, bson.M{"id": sport.ID, "tenantId": bson.M{"$exists": false}}, bson.M{"$set": sport}, options.Update().SetUpsert(true))
	return err
}
func SeedOfficialRoles(ctx context.Context, db *mongo.Database, request model.OfficialRolesRequest) error {
	if db == nil || strings.TrimSpace(request.Sport) == "" {
		return fmt.Errorf("test database and sport are required")
	}
	pipeline := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{"officialRoles": bson.M{"$concatArrays": bson.A{
		bson.M{"$filter": bson.M{"input": bson.M{"$ifNull": bson.A{"$officialRoles", bson.A{}}}, "as": "entry", "cond": bson.M{"$ne": bson.A{"$$entry.sport", bson.M{"$literal": request.Sport}}}}},
		bson.M{"$literal": bson.A{request}},
	}}}}}}
	_, err := db.Collection("roles").UpdateOne(ctx, bson.M{"_id": "load-test-catalog"}, pipeline, options.Update().SetUpsert(true))
	return err
}
func ActiveRoles(request model.OfficialRolesRequest) model.OfficialRolesRequest {
	result := model.OfficialRolesRequest{Sport: request.Sport, Roles: make([]model.OfficialRole, 0, len(request.Roles))}
	for _, role := range request.Roles {
		if role.Active {
			result.Roles = append(result.Roles, role)
		}
	}
	sort.SliceStable(result.Roles, func(i, j int) bool { return result.Roles[i].DisplayOrder < result.Roles[j].DisplayOrder })
	return result
}
