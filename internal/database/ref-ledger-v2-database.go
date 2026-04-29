package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"encoding/json"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var Client *mongo.Client
var IsConnected bool
var GameFilters bson.M = bson.M{}
var Database string
var URI string

var DatabaseVersion string = "ref-ledger-database-v2.1.0"

var PermittedGameStatusValues []string = []string{"Cancelled", "Completed", "Paid", "Pending"}
var Associations []string = []string{"GOLLC", "MCBOA", "MSO"} // Won'b be needed after developing the Association Collection

type GameFilter struct {
	Status []string `json:"status"`
}

func InitDbase(dbName, uri string) {
	Database = dbName
	URI = uri
}

func SetURI(uri string) {
	URI = uri
}

func BuildMongoGameFilter(path string) (bson.M, error) {

	fmt.Println("Loading filter from", path)
	file, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	var filter GameFilter
	if err := json.Unmarshal(file, &filter); err != nil {
		fmt.Println(err)
		return nil, err
	}

	fmt.Println("Filter loaded!")
	fmt.Println("Building MongoDb Game Filter")
	mongoFilter := bson.M{}

	if len(filter.Status) > 0 {
		mongoFilter["status"] = bson.M{
			"$in": filter.Status,
		}
	}

	fmt.Println("Mongo DB Filter successfully built!")
	fmt.Println("Mongo Filter:", mongoFilter)
	return mongoFilter, nil
}

func QueryCollection(filter bson.M, dbase, collection string) *mongo.Cursor {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		log.Fatal(err)
	}

	count, err := coll.CountDocuments(ctx, filter)

	fmt.Println("coll.Find() found", count, "documents")
	return cursor

}

func QueryGames(parentCtx context.Context, dbase, collection, filter string) ([]model.GameDescriptor, error) {

	mongoDbFilter, err := BuildMongoGameFilter(filter)

	fmt.Println(mongoDbFilter)

	if err != nil {
		fmt.Println("Failed to build Mongo DB Filter for games collection")
		return []model.GameDescriptor{}, err
	}

	cursor := QueryCollection(mongoDbFilter, dbase, collection)

	var results []model.GameDoc
	var gameRecords []model.GameDescriptor

	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.GameDescriptor{}, err
	}

	for _, r := range results {

		gameRecords = append(gameRecords, utils.ConvertGameDocToGameDescr(r))

	}
	return gameRecords, nil
}

func Connect() error {

	if Client != nil {
		return nil
	}

	IsConnected = false

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(URI))

	if err != nil {
		return err
	}

	err = client.Ping(context.TODO(), nil)
	if err != nil {
		return err
	}

	IsConnected = true
	Client = client
	fmt.Println("Connected to MongoDb")
	return nil

}

func GetContext() (context.Context, context.CancelFunc) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	return ctx, cancel
}

func DumpCollection(parentCtx context.Context, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()

	fmt.Println("Printing", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, collection)

	var results []model.GameDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return
	}

	for _, r := range results {
		fmt.Println(r)
	}

}

func DelCollection(parentCtx context.Context, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	fmt.Println("Deleteing", coll.Name())

	err := coll.Drop(ctx)
	if err != nil {
		fmt.Println("Failed to delete", coll.Name())
		log.Fatal(err)
		return
	}

	fmt.Println("Collection deleted successfully")

}

func UpdateOneDoc(parentCtx context.Context, filter, update bson.M, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	fmt.Println("Updating", coll.Name())
	coll.UpdateOne(ctx, filter, update)
}

func DeleteOneDoc(parentCtx context.Context, doc model.GameDoc, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Deleting one record from", collectionName)

	result, err := coll.DeleteOne(ctx, doc)
	if err != nil {
		fmt.Println("Insert failed.  Reason:", err)
		return
	}

	fmt.Println("Deleted Record with GameId of", doc.GameId, " Records Deleted:", result.DeletedCount)
}

func InsertDocs(parentCtx context.Context, game []model.GameDescriptor, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)

	recordsInserted := 0
	for _, v := range game {

		doc := utils.ConvertGameDescrToGameDoc(v)
		_, err := coll.InsertOne(ctx, doc)
		if err != nil {
			fmt.Println("Insert failed.  Reason:", err)
			return
		}
		recordsInserted++

		//fmt.Println("Inserted ID:", result.InsertedID)
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted)
}

func ClearGameFilters() {
	GameFilters = bson.M{}
}

func SetGameFilters(field, value string) {
	GameFilters[field] = value
}

func GetGameFilters() bson.M {
	return GameFilters
}
