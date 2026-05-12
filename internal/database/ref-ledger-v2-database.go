package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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

var PermittedGameStatusValues []string = []string{"Cancelled", "Completed", "Paid", "Pending", "Deleted"}
var Associations []string = []string{"GOLLC", "MCBOA", "MSO"} // Won'b be needed after developing the Association Collection

func InitDbase(dbName, uri string) {
	Database = dbName
	URI = uri
}

func SetURI(uri string) {
	URI = uri
}

func GetSingleGame(parentCtx context.Context, gameId string) (model.GameDescriptor, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	id, err := utils.ConvertStrToInt64(gameId)

	if err != nil {
		fmt.Println(err)
		return model.GameDescriptor{}, err
	}

	filter := bson.M{
		"gameId": id,
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	cursor, err := coll.Find(ctx, filter)

	var results []model.GameDoc
	var gameRecord model.GameDescriptor

	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return model.GameDescriptor{}, err
	}

	if len(results) > 1 {
		return model.GameDescriptor{}, fmt.Errorf("Error!  Multiple game documents found!")
	}

	gameRecord = utils.ConvertGameDocToGameDescr(results[0])

	return gameRecord, nil
}

func QueryPayments(parentCtx context.Context, dbase, collection string) ([]model.PaymentDescriptor, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{}

	db := Client.Database(Database)
	coll := db.Collection(collection)

	cursor, err := coll.Find(ctx, filter)

	var results []model.PaymentDoc
	var paymentRecords []model.PaymentDescriptor

	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.PaymentDescriptor{}, err
	}

	for _, r := range results {

		paymentRecords = append(paymentRecords, utils.ConvertPaymentDocToPaymentDescr(r))

	}
	return paymentRecords, nil
}

func QueryAggregatedGames(parentCtx context.Context, dbase, collection, filter string) ([]model.GameDescriptor, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	mongoDbFilter, err := BuildMongoGameFilterFromFile(filter)

	fmt.Println(mongoDbFilter)

	if err != nil {
		fmt.Println("Failed to build Mongo DB Filter for games collection")
		return []model.GameDescriptor{}, err
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	pipeline := mongo.Pipeline{
		{
			{Key: "$match", Value: mongoDbFilter},
		},
		{
			{Key: "$addFields", Value: bson.D{
				{Key: "convertedDate", Value: bson.D{
					{Key: "$dateFromString", Value: bson.D{
						{Key: "dateString", Value: "$date"},
						{Key: "format", Value: "%m/%d/%Y"},
					}},
				}},
			}},
		},
		{
			{Key: "$sort", Value: bson.D{
				{Key: "convertedDate", Value: 1},
			}},
		},
		{
			{Key: "$project", Value: bson.D{
				{Key: "convertedDate", Value: 0},
			}},
		},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)

	if err != nil {
		fmt.Println("Aggregate failed.  Reason:", err)
		return []model.GameDescriptor{}, err
	}

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

func PaymentExists(doc model.PaymentDoc) (bool, error) {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()
	filter := bson.M{}

	var paymentExists bool = false

	db := Client.Database(Database)
	coll := db.Collection("payments")

	filter = bson.M{
		"paymentId": doc.PaymentId,
	}

	// Query to find all documents
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return false, fmt.Errorf("CountDocuments failure.  Reason: %s", err)
	}

	if count > 0 {
		fmt.Println("Game exists!")
		paymentExists = true
	}

	return paymentExists, nil
}

func GameExists(doc model.GameDoc) (bool, error) {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()
	filter := bson.M{}

	var gameExists bool = false

	db := Client.Database(Database)
	coll := db.Collection("games")

	filter = bson.M{
		"gameId": doc.GameId,
	}

	// Query to find all documents
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return false, fmt.Errorf("CountDocuments failure.  Reason: %s", err)
	}

	if count > 0 {
		fmt.Println("Game exists!")
		gameExists = true
	}

	return gameExists, nil
}

func BuildMongoGameFilter(filter model.GameFilter) bson.M {

	fmt.Println("Building MongoDb Game Filter")
	mongoFilter := bson.M{}

	if len(filter.Status) > 0 {
		mongoFilter["status"] = bson.M{
			"$in": filter.Status,
		}
	}

	if len(filter.Association) > 0 {
		mongoFilter["association"] = bson.M{
			"$in": filter.Association,
		}
	}

	if len(filter.GameId) > 0 {
		mongoFilter["gameId"] = bson.M{
			"$in": filter.GameId,
		}
	}

	if len(filter.Site) > 0 {
		mongoFilter["site"] = bson.M{
			"$in": filter.Site,
		}
	}

	if len(filter.Sport) > 0 {
		mongoFilter["sport"] = bson.M{
			"$in": filter.Sport,
		}
	}

	if len(filter.Level) > 0 {
		mongoFilter["level"] = bson.M{
			"$in": filter.Level,
		}
	}

	// Official filters
	var officials []bson.M

	if filter.Referee != "" {
		fmt.Println("BuildMongoGameFilter - Referee filter:", filter.Referee)
		officials = append(officials, bson.M{"referee": filter.Referee})
	}

	if filter.U1 != "" {
		fmt.Println("BuildMongoGameFilter - U1 filter:", filter.U1)
		officials = append(officials, bson.M{"u1": filter.U1})
	}

	if filter.U2 != "" {
		fmt.Println("BuildMongoGameFilter - U2 filter:", filter.U2)
		officials = append(officials, bson.M{"u2": filter.U2})
	}

	if len(officials) > 0 {
		fmt.Println("BuildMongoGameFilter - Officials filter:", officials)
		mongoFilter["$or"] = officials
	}

	// Date handling (your format: M/D/YYYY)
	if filter.Date != nil {

		fromTime, fromErr := time.Parse("1/2/2006", filter.Date.From)
		toTime, toErr := time.Parse("1/2/2006", filter.Date.To)

		if fromErr == nil && toErr == nil {
			mongoFilter["$expr"] = bson.M{
				"$and": []bson.M{
					{
						"$gte": []interface{}{
							bson.M{
								"$dateFromString": bson.M{
									"dateString": "$date",
									"format":     "%m/%d/%Y",
								},
							},
							fromTime,
						},
					},
					{
						"$lte": []interface{}{
							bson.M{
								"$dateFromString": bson.M{
									"dateString": "$date",
									"format":     "%m/%d/%Y",
								},
							},
							toTime,
						},
					},
				},
			}
		} else if fromErr == nil {
			mongoFilter["$expr"] = bson.M{
				"$and": []bson.M{
					{
						"$gte": []interface{}{
							bson.M{
								"$dateFromString": bson.M{
									"dateString": "$date",
									"format":     "%m/%d/%Y",
								},
							},
							fromTime,
						},
					},
				},
			}
		} else if toErr == nil {
			mongoFilter["$expr"] = bson.M{
				"$and": []bson.M{
					{
						"$lte": []interface{}{
							bson.M{
								"$dateFromString": bson.M{
									"dateString": "$date",
									"format":     "%m/%d/%Y",
								},
							},
							toTime,
						},
					},
				},
			}
		}
	}

	// Convert bson.M to raw BSON bytes
	text := fmt.Sprintf("%v", mongoFilter)

	// Write BSON bytes to file
	err := os.WriteFile("gamesReportFilters.bson", []byte(text), 0644)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Mongo DB Filter successfully built!")

	return mongoFilter
}

func BuildMongoGameFilterFromFile(path string) (bson.M, error) {

	if path == "" {
		return bson.M{}, nil
	}

	fmt.Println("Loading filter from", path)
	file, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	var filter model.GameFilter
	if err := json.Unmarshal(file, &filter); err != nil {
		fmt.Println(err)
		return nil, err
	}

	fmt.Println("Filter loaded!")
	mongoFilter := BuildMongoGameFilter(filter)

	return mongoFilter, nil
}

func QueryCollection(filter bson.M, dbase, collection string) *mongo.Cursor {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	if collection == "games" {

	}

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

	mongoDbFilter, err := BuildMongoGameFilterFromFile(filter)

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

	var result bson.M

	err = client.Database("admin").RunCommand(
		context.TODO(),
		bson.D{{Key: "buildInfo", Value: 1}},
	).Decode(&result)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("MongoDB Version:", result["version"])
	return nil

}

func GetContext() (context.Context, context.CancelFunc) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	return ctx, cancel
}

func DumpPaymentsCollection(parentCtx context.Context, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()

	fmt.Println("Printing", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, collection)

	var results []model.PaymentDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return
	}

	for _, r := range results {
		fmt.Println(r)
	}
}

func DumpOfficialsCollection(parentCtx context.Context, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()

	fmt.Println("Printing", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, collection)

	var results []model.OfficialDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return
	}

	for _, r := range results {
		fmt.Println(r)
	}

}

func DumpGamesCollection(parentCtx context.Context, dbase, collection string) {

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

	fmt.Println("Deleting", coll.Name())

	err := coll.Drop(ctx)
	if err != nil {
		fmt.Println("Failed to delete", coll.Name())
		log.Fatal(err)
		return
	}

	fmt.Println("Collection deleted successfully")

}

func UpdateManyDoc(parentCtx context.Context, filter, update bson.M, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	fmt.Println("Updating Many", coll.Name())
	coll.UpdateMany(ctx, filter, update)
}

func UpdateOneDoc(parentCtx context.Context, filter, update bson.M, dbase, collection string) error {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	//fmt.Println("Updating One", coll.Name())
	results, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("Attempt to update %s failed.  Reason: %s", coll.Name(), err)
	}

	if results.ModifiedCount != 1 {
		return fmt.Errorf("No records modified in collection %s", coll.Name())
	}

	return nil
}

func DeleteOneDoc(parentCtx context.Context, filter bson.M, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Deleting one document from", collectionName)

	result, err := coll.DeleteOne(ctx, filter)
	if err != nil {
		fmt.Println("Delete failed.  Reason:", err)
		return
	}

	fmt.Println("Deleted Record with GameId of", filter["gameId"], " Records Deleted:", result.DeletedCount)
}

func InsertOfficialDocs(parentCtx context.Context, game []model.OfficialDescriptor, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)

	officialId := 1

	recordsInserted := 0
	for _, v := range game {

		doc := utils.ConvertOfficialDescrToOfficialDoc(v)
		doc.OfficialId = officialId

		fmt.Println("Official Doc:", doc)
		_, err := coll.InsertOne(ctx, doc)
		if err != nil {
			fmt.Println("Insert failed.  Reason:", err)
			return
		}
		recordsInserted++
		officialId++
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted)
}

func UpdateGameStatusToPaid(parentCtx context.Context, gameIds []int64) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("games")
	collectionName := coll.Name()
	fmt.Println("Updating Games Status to Paid for game ids:", gameIds)

	recordsUpdated := 0
	totalErrors := 0

	var game model.GameDescriptor = model.GameDescriptor{}

	for _, id := range gameIds {

		gameIdStr, err := utils.ConvertSingleGameIdToStr(id)

		if err != nil {
			fmt.Println("Failed to convert Game Id to string.  Reason:", err)
			continue
		}

		game, err = GetSingleGame(parentCtx, gameIdStr)

		if err != nil {
			fmt.Println("Failed to get game record.  Reason:", err)
			continue
		}

		if game.Status == "Pending" || game.Status == "Completed" {

			filter := bson.M{
				"gameId": id,
			}

			update := bson.M{
				"$set": bson.M{
					"status": "Paid",
				},
			}

			err = UpdateOneDoc(ctx, filter, update, Database, "games")
			if err != nil {
				totalErrors++
				fmt.Println(err)
			} else {
				recordsUpdated++
			}
		}
	}

	fmt.Println("Total Records Updated", collectionName, ":", recordsUpdated, "Total Errors", totalErrors)
}

func InsertPaymentDocs(parentCtx context.Context, payment []model.PaymentDescriptor, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)

	recordsInserted := 0
	totalErrors := 0
	var gameIds []int64

	for _, v := range payment {

		doc := utils.ConvertPaymentDescrToPaymentDoc(v)

		paymentExists, err := PaymentExists(doc)

		if paymentExists || err != nil {
			if err != nil {
				totalErrors++
				fmt.Println(err)
			}
			continue
		}

		_, err = coll.InsertOne(ctx, doc)
		if err != nil {
			fmt.Println("Insert failed.  Reason:", err)
			totalErrors++
			continue
		}

		recordsInserted++

		gameIds, err = utils.ConvertGameIdStrToInt(v.GameIds)

		if err != nil {
			fmt.Println("Failed to convert Game Ids string to []int64.  Reason:", err)
		} else {
			UpdateGameStatusToPaid(ctx, gameIds)
		}
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted, "Total Errors:", totalErrors)
}

func InsertGameDocs(parentCtx context.Context, game []model.GameDescriptor, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)

	recordsInserted := 0
	totalErrors := 0

	for _, v := range game {

		doc := utils.ConvertGameDescrToGameDoc(v)

		gameExists, err := GameExists(doc)

		if gameExists || err != nil {
			totalErrors++
			if err != nil {
				fmt.Println(err)
			}
			continue
		}
		_, err = coll.InsertOne(ctx, doc)
		if err != nil {
			fmt.Println("Insert failed.  Reason:", err)
			return
		}
		recordsInserted++

		//fmt.Println("Inserted ID:", result.InsertedID)
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted)
	fmt.Println("Total Errors:", totalErrors)
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

func FindOfficial(parentCtx context.Context, name string) (bool, error) {

	var filter bson.M
	var names []string

	names = strings.Split(name, " ")

	if len(names) < 2 {
		return false, fmt.Errorf("Invalid name[%s].  Missing required parameter: first and last name are both required.", name)
	}

	if names[0] == "" || names[1] == "" {
		return false, fmt.Errorf("Invalid query.  Missing required parameter: first and last name are both required.")
	}

	filter = bson.M{
		"firstName": names[0],
		"lastName":  names[1],
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("officials")

	result := coll.FindOne(ctx, filter)

	if result.Err() == mongo.ErrNoDocuments {
		return false, nil
	}

	return true, nil
}
