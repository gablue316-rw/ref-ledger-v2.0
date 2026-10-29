package metrics

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// UpdateCollection records successful changes in the current minute's bucket.
func UpdateCollection(
	ctx context.Context,
	collection *mongo.Collection,
	tenantId string,
	collectionName string,
	added, updated, deleted, errors int64,
) error {

	fmt.Println("Updating metrics for collection:", collectionName)

	if collection == nil {
		return fmt.Errorf("metrics collection is nil")
	}

	if tenantId == "" || collectionName == "" {
		return fmt.Errorf("tenantId and collectionName are required")
	}

	if added < 0 || updated < 0 || deleted < 0 || errors < 0 {
		return fmt.Errorf("metric counts cannot be negative")
	}

	if added == 0 && updated == 0 && deleted == 0 && errors == 0 {
		return nil
	}

	filter := bson.M{
		"tenantId":    tenantId,
		"bucketStart": time.Now().UTC().Truncate(time.Minute),
		"category":    "collection",
		"collection":  collectionName,
	}

	update := bson.M{
		"$inc": bson.M{
			"added":   added,
			"updated": updated,
			"deleted": deleted,
			"errors":  errors,
		},
	}

	_, err := collection.UpdateOne(
		ctx,
		filter,
		update,
		options.Update().SetUpsert(true),
	)
	fmt.Println("Update result for collection:", collectionName)
	if err != nil {
		return fmt.Errorf("update %s metrics: %w", collectionName, err)
	}

	return nil
}

// EnsureIndexes should be called once during application startup.
func EnsureIndexes(ctx context.Context, collection *mongo.Collection) error {
	if collection == nil {
		return fmt.Errorf("metrics collection is nil")
	}

	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenantId", Value: 1},
			{Key: "bucketStart", Value: 1},
			{Key: "category", Value: 1},
			{Key: "collection", Value: 1},
		},
		Options: options.Index().
			SetName("unique_metrics_bucket").
			SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create metrics index: %w", err)
	}

	return nil
}
