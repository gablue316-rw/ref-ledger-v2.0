package database

import (
	"context"
	"errors"
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

var ExpenseTypes []string = []string{"Camp Fee", "Dues", "Equipment", "Food", "Mileage"}

var PermittedGameStatusValues []string = []string{"Cancelled", "Completed", "Paid", "Pending", "Deleted"}
var Associations []string = []string{"GOLLC", "MCBOA", "MSO"} // Won'b be needed after developing the Association Collection

type OfficialName struct {
	Name string `json:"name"`
}

type AssignorName struct {
	Name string `json:"name"`
}

type SiteName struct {
	Name string `json:"name"`
}

func foundAssociation(assoc string) bool {

	associations := []string{"GOLLC", "MCBOA", "MSO"}

	for _, a := range associations {
		if assoc == a {
			return true
		}
	}
	return false
}

func InitDbase(dbName, uri string) {
	Database = dbName
	URI = uri
}

func SetURI(uri string) {
	URI = uri
}

func ClearGames(parentCtx context.Context, gameIds []int64) {

	filter := bson.M{
		"gameId": bson.M{
			"$in": gameIds,
		},
	}

	update := bson.M{
		"$set": bson.M{
			"gameFee":     0,
			"travelPay":   0,
			"assignorFee": 0,
			"deductions":  0,
			"status":      "Cancelled",
		},
	}

	if len(gameIds) > 1 {
		UpdateManyDoc(parentCtx, filter, update, Database, "games")
	} else {
		err := UpdateOneDoc(parentCtx, filter, update, Database, "games")
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("Successfully updated game with game id[s]:", gameIds)
		}
	}
}

func GetGamesPaidForPerAssoc(assoc string) ([]int64, error) {

	var gameIdList []int64 = []int64{}

	if assoc == "" || !foundAssociation(assoc) {
		return gameIdList, fmt.Errorf("GetGamesPaidForPerAssoc failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
	}

	db := Client.Database(Database)
	coll := db.Collection("payments")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return gameIdList, fmt.Errorf("GetGamesPaidForPerAssoc failure.  Reason: %s", err)
	}

	var results []model.PaymentDoc
	err = cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return gameIdList, fmt.Errorf("GetGamesPaidForPerAssoc failure.  Reason: %s", err)
	}

	for _, r := range results {
		gameIdList = append(gameIdList, r.GameIds...)
	}

	return gameIdList, nil
}

func GetGamesInPaidStatusPerAssoc(assoc string) ([]int64, error) {

	var gameIdList []int64 = []int64{}

	if assoc == "" || !foundAssociation(assoc) {
		return gameIdList, fmt.Errorf("GetGamesInPaidStatusPerAssoc failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return gameIdList, fmt.Errorf("GetGamesInPaidStatusPerAssoc failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return gameIdList, fmt.Errorf("GetGamesInPaidStatusPerAssoc failure.  Reason: %s", err)
	}

	for _, r := range results {
		gameIdList = append(gameIdList, r.GameId)
	}

	return gameIdList, nil
}

func GetTotalMileage(assoc string) (int64, error) {

	totalMileage := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalMileage, fmt.Errorf("GetTotalMileage failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"type":        "Mileage",
	}

	db := Client.Database(Database)
	coll := db.Collection("expenses")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalMileage, fmt.Errorf("GetTotalMileage failure.  Reason: %s", err)
	}

	var results []model.ExpenseDoc
	err = cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalMileage, fmt.Errorf("GetTotalMileage failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalMileage += r.Amount
	}

	return totalMileage, nil

}

func GetTotalFoodExpense(assoc string) (int64, error) {

	totalFood := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalFood, fmt.Errorf("GetTotalFoodExpense failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"type":        "Food",
	}

	db := Client.Database(Database)
	coll := db.Collection("expenses")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalFood, fmt.Errorf("GetTotalFoodExpense failure.  Reason: %s", err)
	}

	var results []model.ExpenseDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalFood, fmt.Errorf("GetTotalFoodExpense failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalFood += r.Amount
	}

	return totalFood, nil

}

func GetTotalGames(assoc string) (int64, error) {

	totalGames := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalGames, fmt.Errorf("GetTotalGames failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalGames, fmt.Errorf("GetTotalGames failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalGames, fmt.Errorf("GetTotalGames failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalGames += r.NumOfGames
	}
	return totalGames, nil
}

func GetTotalEquipmentExpense(assoc string) (int64, error) {

	totalEquipment := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalEquipment, fmt.Errorf("GetTotalEquipmentExpense failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"type":        "Equipment",
	}

	db := Client.Database(Database)
	coll := db.Collection("expenses")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalEquipment, fmt.Errorf("GetTotalEquipmentExpense failure.  Reason: %s", err)
	}

	var results []model.ExpenseDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalEquipment, fmt.Errorf("GetTotalEquipmentExpense failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalEquipment += r.Amount
	}

	return totalEquipment, nil

}

func GetTotalDues(assoc string) (int64, error) {

	totalDues := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalDues, fmt.Errorf("GetTotalDues failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"type":        "Dues",
	}

	db := Client.Database(Database)
	coll := db.Collection("expenses")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalDues, fmt.Errorf("GetTotalDues failure.  Reason: %s", err)
	}

	var results []model.ExpenseDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalDues, fmt.Errorf("GetTotalDues failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalDues += r.Amount
	}

	return totalDues, nil

}

func GetTotalTravelPay(assoc string) (int64, error) {

	totalTravelPay := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalTravelPay, fmt.Errorf("GetTotalTravelPay failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalTravelPay, fmt.Errorf("GetTotalTravelPay failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalTravelPay, fmt.Errorf("GetTotalTravelPay failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalTravelPay += r.TravelPay
	}

	return totalTravelPay, nil

}

func GetTotalAssignorFee(assoc string) (int64, error) {

	totalAssignorFee := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalAssignorFee, fmt.Errorf("GetTotalAssignorFee failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalAssignorFee, fmt.Errorf("GetTotalAssignorFee failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalAssignorFee, fmt.Errorf("GetTotalAssignorFee failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalAssignorFee += r.AssignorFee
	}

	return totalAssignorFee, nil

}

func GetTotalDeductions(assoc string) (int64, error) {

	totalDeductions := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalDeductions, fmt.Errorf("GetTotalDeductions failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalDeductions, fmt.Errorf("GetTotalDeductions failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalDeductions, fmt.Errorf("GetTotalDeductions failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalDeductions += r.Deductions
	}

	return totalDeductions, nil

}

func GetTotalGrossGameFee(assoc string) (int64, error) {

	totalGrossGameFee := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalGrossGameFee, fmt.Errorf("GetTotalGrossGameFee failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"status":      "Paid",
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalGrossGameFee, fmt.Errorf("GetTotalGrossGameFee failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalGrossGameFee, fmt.Errorf("GetTotalGrossGameFee failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalGrossGameFee += r.GameFee * r.NumOfGames
	}

	return totalGrossGameFee, nil

}

func GetTotalCampFees(assoc string) (int64, error) {

	totalCampFees := int64(0)

	if assoc == "" || !foundAssociation(assoc) {
		return totalCampFees, fmt.Errorf("GetTotalCampFees failure.  Reason: Invalid Association: %s", assoc)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"association": assoc,
		"type":        "Camp Fees",
	}

	db := Client.Database(Database)
	coll := db.Collection("expenses")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totalCampFees, fmt.Errorf("GetTotalCampFees failure.  Reason: %s", err)
	}

	var results []model.ExpenseDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totalCampFees, fmt.Errorf("GetTotalCampFees failure.  Reason: %s", err)
	}

	for _, r := range results {
		totalCampFees += r.Amount
	}

	return totalCampFees, nil

}

func GetGameFee(gameIds []int64) (int64, error) {

	totGameFee := int64(0)

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	filter := bson.M{}

	if len(gameIds) > 0 {
		filter["gameId"] = bson.M{
			"$in": gameIds,
		}
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	// Query to find all documents
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return totGameFee, fmt.Errorf("GetGameFee failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return totGameFee, fmt.Errorf("GetGameFee failure.  Reason: %s", err)
	}

	for _, r := range results {

		totGameFee += r.GameFee*r.NumOfGames + r.TravelPay - r.Deductions - r.AssignorFee
	}

	return totGameFee, nil
}

// Used by HTML Web Pages
func GetGameByGameIdAndOrAssoc(assoc string, gameId string) (model.GameDescriptor, error) {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	id, err := utils.ConvertStrToInt64(gameId)
	if err != nil {
		fmt.Println(err)
		return model.GameDescriptor{}, err
	}

	if assoc == "" && gameId == "" {
		return model.GameDescriptor{}, fmt.Errorf("GetGameByAssocAndOrId failure.  Reason: Invalid parameters")
	}

	if gameId == "" {
		return model.GameDescriptor{}, fmt.Errorf("GetGameByAssocAndOrId failure.  Reason: Invalid parameters")
	}

	filter := bson.M{
		"gameId": id,
	}

	if assoc == "" {
		filter = bson.M{
			"association": assoc,
		}
	}

	db := Client.Database(Database)
	coll := db.Collection("games")

	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return model.GameDescriptor{}, fmt.Errorf("GetGameByAssocAndId failure.  Reason: %s", err)
	}

	var results []model.GameDoc
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return model.GameDescriptor{}, fmt.Errorf("GetGameByAssocAndId failure.  Reason: %s", err)
	}

	if len(results) == 0 {
		return model.GameDescriptor{}, fmt.Errorf("Game not found for association: %s and game ID: %s", assoc, gameId)
	}

	gameRecord := utils.ConvertGameDocToGameDescr(results[0])

	return gameRecord, nil
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

func QueryOfficials(parentCtx context.Context, dbase, collection, assoc, official string) ([]model.OfficialDescriptor, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{}

	if len(assoc) > 0 {
		assocValues := utils.ParseCsv(assoc)
		filter["association"] = bson.M{
			"$in": assocValues,
		}
	}

	if len(official) > 0 {
		parts := strings.Split(official, " ")
		if len(parts) == 1 {
			filter["$or"] = []bson.M{
				{"firstName": parts[0]},
				{"lastName": parts[0]},
			}
		} else {
			filter["firstName"] = parts[0]
			filter["lastName"] = parts[1]
		}
	}

	db := Client.Database(Database)
	coll := db.Collection(collection)

	cursor, err := coll.Find(ctx, filter)

	var results []model.OfficialDoc
	var officialRecords []model.OfficialDescriptor

	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.OfficialDescriptor{}, err
	}

	for _, r := range results {

		officialRecords = append(officialRecords, utils.ConvertOfficialDocToOfficialDescr(r))

	}
	return officialRecords, nil
}

func QueryPayments(parentCtx context.Context, dbase, collection, assoc string) ([]model.PaymentDescriptor, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{}

	if len(assoc) > 0 {
		assocValues := utils.ParseCsv(assoc)
		filter["association"] = bson.M{
			"$in": assocValues,
		}
	}

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
		"gameId":      doc.GameId,
		"association": doc.Association,
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

func BuildMongoExpenseFilter(filter model.ExpenseFilter) bson.M {
	fmt.Println("Building MongoDb Expense Filter")
	mongoFilter := bson.M{}

	if len(filter.Association) > 0 {
		mongoFilter["association"] = bson.M{
			"$in": filter.Association,
		}
	}

	if len(filter.ExpenseType) > 0 {
		mongoFilter["type"] = bson.M{
			"$in": filter.ExpenseType,
		}
	}

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
		}
	}
	return mongoFilter
}

func BuildMongoExpenseFilterFromFile(path string) (bson.M, error) {

	fmt.Println("Building MongoDb Expense Filter from File")
	if path == "" {
		return bson.M{}, nil
	}

	file, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	var filter model.ExpenseFilter
	if err := json.Unmarshal(file, &filter); err != nil {
		fmt.Println(err)
		return nil, err
	}

	fmt.Println("Filter is loaded!")
	mongoFilter := BuildMongoExpenseFilter(filter)

	fmt.Println("mongoFilter:", mongoFilter)
	return mongoFilter, nil
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
		officials = append(officials, bson.M{"referee": filter.Referee})
	}

	if filter.U1 != "" {
		officials = append(officials, bson.M{"u1": filter.U1})
	}

	if filter.U2 != "" {
		officials = append(officials, bson.M{"u2": filter.U2})
	}

	if len(officials) > 0 {
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

	fmt.Println("Building MongoDb Game Filter from File")
	if path == "" {
		return bson.M{}, nil
	}

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

	fmt.Println("Filter is loaded!")
	mongoFilter := BuildMongoGameFilter(filter)

	fmt.Println("mongoFilter:", mongoFilter)
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

func QueryExpenses(parentCtx context.Context, dbase, collection, filter string) ([]model.ExpenseDescriptor, error) {

	mongoDbFilter, err := BuildMongoExpenseFilterFromFile(filter)

	if err != nil {
		fmt.Println("Failed to build Mongo DB Filter for expenses collection")
		return []model.ExpenseDescriptor{}, err
	}

	fmt.Println("mongoDbFilter:", mongoDbFilter)
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	cursor, err := coll.Find(ctx, mongoDbFilter)
	if err != nil {
		fmt.Println("Error", err)
		return []model.ExpenseDescriptor{}, err
	}

	var results []model.ExpenseDoc
	var expenseRecords []model.ExpenseDescriptor
	err = cursor.All(context.TODO(), &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.ExpenseDescriptor{}, err
	}

	for _, r := range results {
		expenseRecords = append(expenseRecords, utils.ConvertExpenseDocToExpenseDescr(r))
	}
	return expenseRecords, nil
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

func GetExpensesCollection(parentCtx context.Context) ([]model.ExpenseDoc, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("expenses")
	collectionName := coll.Name()

	fmt.Println("Retrieving", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, "expenses")

	var results []model.ExpenseDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.ExpenseDoc{}, err
	}

	return results, nil
}

func GetOfficialNames() ([]OfficialName, error) {

	db := Client.Database(Database)
	coll := db.Collection("officials")
	result := []OfficialName{}

	opts := options.Find().
		SetSort(bson.D{
			{Key: "lastName", Value: 1},
			{Key: "firstName", Value: 1},
		})

	cursor, err := coll.Find(context.Background(), bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	var officials []model.OfficialDoc = []model.OfficialDoc{}

	if err := cursor.All(context.Background(), &officials); err != nil {
		return nil, err
	}

	//
	// Add Unassigned to Officials collection and then remove this
	//
	result = append(result, OfficialName{
		Name: "Unassigned",
	})

	for _, o := range officials {
		result = append(result, OfficialName{
			Name: strings.TrimSpace(o.FirstName + " " + o.LastName),
		})
	}

	return result, nil
}

func GetOfficialsCollection(parentCtx context.Context) ([]model.OfficialDoc, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("officials")
	collectionName := coll.Name()

	fmt.Println("Retrieving", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, "officials")

	var results []model.OfficialDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.OfficialDoc{}, err
	}

	return results, nil
}

func GetPaymentsCollection(parentCtx context.Context) ([]model.PaymentDoc, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("payments")
	collectionName := coll.Name()

	fmt.Println("Retrieving", collectionName, "collection")

	cursor := QueryCollection(bson.M{}, Database, "payments")

	var results []model.PaymentDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.PaymentDoc{}, err
	}

	return results, nil
}

func ClearGamesCollection(parentCtx context.Context, assoc string) {

	filter := bson.M{}

	if len(assoc) > 0 {
		assocValues := utils.ParseCsv(assoc)
		fmt.Println("Attempting to delete all games for association(s):", assocValues)
		filter["association"] = bson.M{
			"$in": assocValues,
		}
	}
	DeleteManyDoc(parentCtx, filter, Database, "games")

}

func GetGamesCollection(parentCtx context.Context, assoc string) ([]model.GameDoc, error) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{}

	if len(assoc) > 0 {
		assocValues := utils.ParseCsv(assoc)
		filter["association"] = bson.M{
			"$in": assocValues,
		}
	}

	db := Client.Database(Database)
	coll := db.Collection("games")
	collectionName := coll.Name()

	fmt.Println("Retrieving", collectionName, "collection")

	cursor := QueryCollection(filter, Database, "games")

	var results []model.GameDoc

	err := cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.GameDoc{}, err
	}

	return results, nil
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

func DeleteManyDoc(parentCtx context.Context, filter bson.M, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Deleting multiple documents from", collectionName)

	result, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		fmt.Println("Delete failed.  Reason:", err)
		return
	}

	fmt.Println("Deleted Records with GameIds of", filter["gameId"], " Records Deleted:", result.DeletedCount)
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

	utils.AuditLog.Printf("InsertOfficialDocs: Inserting %d official records into collection %s", len(game), collection)
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

func UpdateGameStatus(gameIds []int64, status string) {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("games")
	collectionName := coll.Name()
	fmt.Println("Updating Games Status to", status, "for game ids:", gameIds)

	recordsUpdated := 0
	totalErrors := 0

	var game model.GameDescriptor = model.GameDescriptor{}

	for _, id := range gameIds {

		gameIdStr, err := utils.ConvertSingleGameIdToStr(id)

		if err != nil {
			fmt.Println("Failed to convert Game Id to string.  Reason:", err)
			continue
		}

		game, err = GetSingleGame(ctx, gameIdStr)

		if err != nil {
			fmt.Println("Failed to get game record.  Reason:", err)
			continue
		}

		if game.Status == status {
			fmt.Println("Game Id", id, "already set to status", status)
			continue
		}

		filter := bson.M{
			"gameId": id,
		}

		update := bson.M{
			"$set": bson.M{
				"status": status,
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

	fmt.Println("Total Records Updated", collectionName, ":", recordsUpdated, "Total Errors", totalErrors)
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
			utils.AuditLog.Printf("Failed to get game record for GameId %s.  Reason: %v", gameIdStr, err)
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
	utils.AuditLog.Printf("InsertPaymentDocs: Inserting %d payment records into collection %s", len(payment), collection)
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
			utils.AuditLog.Printf("Failed to insert payment record for PaymentId %s.  Reason: %v", doc.PaymentId, err)
			fmt.Println("Insert failed.  Reason:", err)
			totalErrors++
			continue
		}

		recordsInserted++

		gameIds, err = utils.ConvertGameIdStrToInt(v.GameIds)

		if err != nil {
			utils.AuditLog.Printf("Failed to convert Game Ids string to []int64 for PaymentId %s.  Reason: %v", doc.PaymentId, err)
			fmt.Println("Failed to convert Game Ids string to []int64.  Reason:", err)
		} else {
			UpdateGameStatusToPaid(ctx, gameIds)
		}
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted, "Total Errors:", totalErrors)
}

func InsertExpenseDocs(parentCtx context.Context, expense []model.ExpenseDescriptor, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)
	recordsInserted := 0
	totalErrors := 0
	for _, v := range expense {

		doc := utils.ConvertExpenseDescrToExpenseDoc(v)
		_, err := coll.InsertOne(ctx, doc)
		if err != nil {
			fmt.Println("Insert failed.  Reason:", err)
			totalErrors++
			continue
		}
		fmt.Println("Inserted Expense with Id:", doc.ExpenseId, "Date:", doc.Date, "Type:", doc.Type, "Amount:", doc.Amount, "Association:", doc.Association, "GameId:", doc.GameId, "Description:", doc.Description)
		recordsInserted++
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted, "Total Errors:", totalErrors)

}

func UpdateOneGameDoc(parentCtx context.Context, game model.GameDescriptor, dbase, collection string) error {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	fmt.Println("Updating one game")

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	doc := utils.ConvertGameDescrToGameDoc(game)
	filter := bson.M{
		"gameId": doc.GameId,
	}

	update := bson.M{
		"$set": doc,
	}

	fmt.Println("Update filter:", filter, "update:", update)
	result, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	fmt.Println("Updated document:", result.ModifiedCount)
	return nil
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

// Association Colleciton, Documents and API Code
type AssociationJson struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	Contact   string `json:"contact"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Assignors string `json:"assignors"`
}

type AssociationDoc struct {
	Id        string `bson:"id,omitempty"`
	Name      string `bson:"name,omitempty"`
	Contact   string `bson:"contact,omitempty"`
	Phone     string `bson:"phone,omitempty"`
	Email     string `bson:"email,omitempty"`
	Assignors string `bson:"assignors,omitempty"`
}

type Association struct {
	Id        string
	Name      string
	Contact   string
	Phone     string
	Email     string
	Assignors string
}

type AssociationCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (ac *AssociationCollection) Init(client *mongo.Client) error {

	ac.DB = client.Database(Database)
	ac.Coll = ac.DB.Collection("associations")

	fmt.Println("Successfully initialized Associations Collection")
	return nil
}

func (ac *AssociationCollection) ConvAssocJsonToAssoc(aj AssociationJson) Association {
	return Association{
		Id:        aj.Id,
		Name:      aj.Name,
		Contact:   aj.Contact,
		Phone:     aj.Phone,
		Email:     aj.Email,
		Assignors: aj.Assignors,
	}
}

func (ac *AssociationCollection) convAssocToDoc(association Association) AssociationDoc {
	return AssociationDoc{
		Id:        association.Id,
		Name:      association.Name,
		Contact:   association.Contact,
		Phone:     association.Phone,
		Email:     association.Email,
		Assignors: association.Assignors,
	}
}

func (ac *AssociationCollection) convDocToAssociation(doc AssociationDoc) Association {
	return Association{
		Id:        doc.Id,
		Name:      doc.Name,
		Contact:   doc.Contact,
		Phone:     doc.Phone,
		Email:     doc.Email,
		Assignors: doc.Assignors,
	}
}

func (ac *AssociationCollection) Add(association Association) error {

	var result *mongo.InsertOneResult
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	doc := ac.convAssocToDoc(association)

	result, ac.LastError = ac.Coll.InsertOne(ctx, doc)
	if ac.LastError != nil {
		return fmt.Errorf("Insert failed.  Reason: %v", ac.LastError)
	}
	fmt.Println("Inserted ID:", result.InsertedID)

	return nil
}

func (ac *AssociationCollection) Get(id string) (*Association, error) {

	var filter bson.M
	var doc AssociationDoc

	filter = bson.M{
		"id": id,
	}

	err := ac.Coll.FindOne(context.TODO(), filter).Decode(&doc)

	if err != nil {
		fmt.Println("Error:", err)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("association not found")
		}
		return nil, err
	}
	association := ac.convDocToAssociation(doc)
	return &association, nil
}

func (ac *AssociationCollection) GetAssignorNames() ([]AssignorName, error) {
	var assignors []AssignorName = []AssignorName{}

	cursor, err := ac.Coll.Find(context.TODO(), bson.M{})
	if err != nil {
		return nil, fmt.Errorf("Failed to query assignors.  Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	for cursor.Next(context.TODO()) {
		var doc AssociationDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("Failed to decode assignor document.  Reason: %v", err)
		}

		for part := range strings.SplitSeq(doc.Assignors, ",") {
			assignors = append(assignors, AssignorName{Name: strings.TrimSpace(part)})
		}
	}

	return assignors, nil
}

func (ac *AssociationCollection) Update(id string, association Association) error {

	var filter bson.M
	var doc AssociationDoc
	var result *mongo.UpdateResult

	filter = bson.M{
		"id": id,
	}

	doc = ac.convAssocToDoc(association)

	result, ac.LastError = ac.Coll.ReplaceOne(context.TODO(), filter, doc)
	if ac.LastError != nil {
		return fmt.Errorf("Failed to update association.  Reason: %v", ac.LastError)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("association not found")
	}

	fmt.Println("Record updated in", ac.Coll.Name())
	return nil
}

func (ac *AssociationCollection) DeleteAll() error {

	var result *mongo.DeleteResult

	result, ac.LastError = ac.Coll.DeleteMany(context.TODO(), bson.M{})
	if ac.LastError != nil {
		return fmt.Errorf("Failed to delete all associations.  Reason: %v", ac.LastError)
	}

	fmt.Println("Deleted ", result.DeletedCount, " records from ", ac.Coll.Name())
	return nil
}

func (ac *AssociationCollection) Delete(id string) error {

	var filter bson.M
	var result *mongo.DeleteResult

	filter = bson.M{
		"id": id,
	}

	result, ac.LastError = ac.Coll.DeleteOne(context.TODO(), filter)
	if ac.LastError != nil {
		return fmt.Errorf("Failed to delete association.  Reason: %v", ac.LastError)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("association not found")
	}

	fmt.Println("Deleted Record with Association Id of", filter["id"], " in", ac.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

func (ac *AssociationCollection) Dump(id string) error {

	fmt.Println("Retrieving association with ID:", id)

	var assoc *Association
	assoc, err := ac.Get(id)
	if err != nil {
		return fmt.Errorf("Failed to get association.  Reason: %v", err)
	}

	fmt.Println("Dumping Record with Association Id of", assoc.Id)

	fmt.Println("Name:", assoc.Name)
	fmt.Println("Contact:", assoc.Contact)
	fmt.Println("Phone:", assoc.Phone)
	fmt.Println("Email:", assoc.Email)
	fmt.Println("Assignors:", assoc.Assignors)

	return nil
}

// Site Colleciton, Documents and API Code
type SiteJson struct {
	Id      string `json:"id"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
}

type SiteDoc struct {
	Id      string `bson:"id,omitempty"`
	Name    string `bson:"name,omitempty"`
	Contact string `bson:"contact,omitempty"`
	Phone   string `bson:"phone,omitempty"`
	Email   string `bson:"email,omitempty"`
}

type Site struct {
	Id      string
	Name    string
	Contact string
	Phone   string
	Email   string
}

type SiteCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (sc *SiteCollection) ConvJsonToSite(aj SiteJson) Site {
	return Site{
		Id:      aj.Id,
		Name:    aj.Name,
		Contact: aj.Contact,
		Phone:   aj.Phone,
		Email:   aj.Email,
	}
}

func (sc *SiteCollection) convSiteToDoc(site Site) SiteDoc {
	return SiteDoc{
		Id:      site.Id,
		Name:    site.Name,
		Contact: site.Contact,
		Phone:   site.Phone,
		Email:   site.Email,
	}
}

func (sc *SiteCollection) convDocToSite(doc SiteDoc) Site {
	return Site{
		Id:      doc.Id,
		Name:    doc.Name,
		Contact: doc.Contact,
		Phone:   doc.Phone,
		Email:   doc.Email,
	}
}

func (sc *SiteCollection) Init(client *mongo.Client) error {

	sc.DB = client.Database(Database)
	sc.Coll = sc.DB.Collection("sites")

	fmt.Println("Successfully initialized Site Collection")
	return nil
}

func (sc *SiteCollection) Add(site Site) error {

	var result *mongo.InsertOneResult
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	doc := sc.convSiteToDoc(site)

	result, sc.LastError = sc.Coll.InsertOne(ctx, doc)
	if sc.LastError != nil {
		return fmt.Errorf("Insert failed.  Reason: %v", sc.LastError)
	}
	fmt.Println("Inserted ID:", result.InsertedID)

	return nil
}

func (sc *SiteCollection) Get(id string) (*Site, error) {

	var filter bson.M
	var doc SiteDoc

	filter = bson.M{
		"id": id,
	}

	err := sc.Coll.FindOne(context.TODO(), filter).Decode(&doc)

	if err != nil {
		fmt.Println("Error:", err)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("site not found")
		}
		return nil, err
	}
	site := sc.convDocToSite(doc)
	return &site, nil
}

func (sc *SiteCollection) GetSiteName() ([]SiteName, error) {
	var sites []SiteName = []SiteName{}

	cursor, err := sc.Coll.Find(context.TODO(), bson.M{})
	if err != nil {
		return nil, fmt.Errorf("Failed to query sites.  Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	for cursor.Next(context.TODO()) {
		var doc SiteDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("Failed to decode site document.  Reason: %v", err)
		}

		for part := range strings.SplitSeq(doc.Name, ",") {
			sites = append(sites, SiteName{Name: strings.TrimSpace(part)})
		}
	}

	return sites, nil
}

func (sc *SiteCollection) Update(id string, site Site) error {

	var filter bson.M
	var doc SiteDoc
	var result *mongo.UpdateResult

	filter = bson.M{
		"id": id,
	}

	doc = sc.convSiteToDoc(site)

	result, sc.LastError = sc.Coll.ReplaceOne(context.TODO(), filter, doc)
	if sc.LastError != nil {
		return fmt.Errorf("Failed to update site.  Reason: %v", sc.LastError)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("site not found")
	}

	fmt.Println("Record updated in", sc.Coll.Name())
	return nil
}

func (sc *SiteCollection) DeleteAll() error {

	var result *mongo.DeleteResult

	result, sc.LastError = sc.Coll.DeleteMany(context.TODO(), bson.M{})
	if sc.LastError != nil {
		return fmt.Errorf("Failed to delete all sites.  Reason: %v", sc.LastError)
	}

	fmt.Println("Deleted ", result.DeletedCount, " records from ", sc.Coll.Name())
	return nil
}

func (sc *SiteCollection) Delete(id string) error {

	var filter bson.M
	var result *mongo.DeleteResult

	filter = bson.M{
		"id": id,
	}

	result, sc.LastError = sc.Coll.DeleteOne(context.TODO(), filter)
	if sc.LastError != nil {
		return fmt.Errorf("Failed to delete site.  Reason: %v", sc.LastError)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("site not found")
	}

	fmt.Println("Deleted Record with Site Id of", filter["id"], " in", sc.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

func (sc *SiteCollection) Dump(id string) error {

	fmt.Println("Retrieving site with ID:", id)

	var site *Site
	site, err := sc.Get(id)
	if err != nil {
		return fmt.Errorf("Failed to get site.  Reason: %v", err)
	}

	fmt.Println("Dumping Record with Site Id of", site.Id)

	fmt.Println("Name:", site.Name)
	fmt.Println("Contact:", site.Contact)
	fmt.Println("Phone:", site.Phone)
	fmt.Println("Email:", site.Email)

	return nil
}

// Games Collection, Documents and API Code

// Add to these structures when I completely migrate the games logic to methods
type Game struct {
	Id          string
	Association string
}

type GameJson struct {
	Id          string `json:"gameId"`
	Association string `json:"association"`
}

type GameDoc struct {
	Id          string `bson:"gameId,omitempty"`
	Association string `bson:"association,omitempty"`
}

type GameCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (gc *GameCollection) Init(client *mongo.Client) error {

	gc.DB = client.Database(Database)
	gc.Coll = gc.DB.Collection("games")

	fmt.Println("Successfully initialized Game Collection")
	return nil
}

func (gc *GameCollection) Delete(association string, gameId string) error {

	var filter bson.M
	var result *mongo.DeleteResult

	gameIdInt64, err := utils.ConvertStrToInt64(gameId)
	if err != nil {
		return fmt.Errorf("Failed to convert game ID.  Reason: %v", err)
	}

	filter = bson.M{
		"gameId":      gameIdInt64,
		"association": association,
	}

	result, gc.LastError = gc.Coll.DeleteOne(context.TODO(), filter)
	if gc.LastError != nil {
		return fmt.Errorf("Failed to delete game.  Reason: %v", gc.LastError)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("game not found")
	}

	fmt.Println("Deleted Record with Game Id of", filter["gameId"], " in", gc.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

// Official Collection, Documents and API Code
type OfficialJson struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Address   string `json:"address"`
}

type OfficialDoc struct {
	Id        int64  `bson:"id,omitempty"`
	FirstName string `bson:"firstName,omitempty"`
	LastName  string `bson:"lastName,omitempty"`
	Phone     string `bson:"phone,omitempty"`
	Email     string `bson:"email,omitempty"`
	Address   string `bson:"address,omitempty"`
}

type Official struct {
	FirstName string
	LastName  string
	Phone     string
	Email     string
	Address   string
}

type OfficialCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (oc *OfficialCollection) ConvJsonToOfficial(oj OfficialJson) Official {
	return Official{
		FirstName: oj.FirstName,
		LastName:  oj.LastName,
		Phone:     oj.Phone,
		Email:     oj.Email,
		Address:   oj.Address,
	}
}

func (oc *OfficialCollection) getNextId() (int64, error) {

	var result OfficialDoc

	opts := options.FindOne().
		SetSort(bson.D{{Key: "id", Value: -1}})

	err := oc.Coll.FindOne(
		context.Background(),
		bson.D{},
		opts,
	).Decode(&result)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			return 1, nil // first document
		}
		return 0, err
	}

	return result.Id + 1, nil
}

func (oc *OfficialCollection) convOfficialToDoc(official Official) (OfficialDoc, error) {

	id, err := oc.getNextId()
	if err != nil {
		return OfficialDoc{}, err
	}

	return OfficialDoc{
		Id:        id,
		FirstName: official.FirstName,
		LastName:  official.LastName,
		Phone:     official.Phone,
		Email:     official.Email,
		Address:   official.Address,
	}, nil
}

func (oc *OfficialCollection) convDocToOfficial(doc OfficialDoc) Official {
	return Official{
		FirstName: doc.FirstName,
		LastName:  doc.LastName,
		Phone:     doc.Phone,
		Email:     doc.Email,
		Address:   doc.Address,
	}
}

func (oc *OfficialCollection) Init(client *mongo.Client) error {

	oc.DB = client.Database(Database)
	oc.Coll = oc.DB.Collection("officials")

	fmt.Println("Successfully initialized Official Collection")
	return nil
}

func (oc *OfficialCollection) Add(official Official) error {

	var result *mongo.InsertOneResult
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	doc, err := oc.convOfficialToDoc(official)
	if err != nil {
		return fmt.Errorf("Failed to convert official to document.  Reason: %v", err)
	}

	result, oc.LastError = oc.Coll.InsertOne(ctx, doc)
	if oc.LastError != nil {
		return fmt.Errorf("Insert failed.  Reason: %v", oc.LastError)
	}
	fmt.Println("Inserted ID:", result.InsertedID)

	return nil
}

func (oc *OfficialCollection) Get(firstName, lastName string) (Official, error) {

	var filter bson.M
	var doc OfficialDoc

	filter = bson.M{
		"firstName": firstName,
		"lastName":  lastName,
	}

	err := oc.Coll.FindOne(context.TODO(), filter).Decode(&doc)

	if err != nil {
		fmt.Println("Error:", err)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Official{}, fmt.Errorf("official not found")
		}
		return Official{}, fmt.Errorf("Failed to get official.  Reason: %v", err)
	}

	official := oc.convDocToOfficial(doc)
	return official, nil
}

func (oc *OfficialCollection) GetOfficialsDirectory() ([]Official, error) {

	var filter bson.M
	var officials []Official

	filter = bson.M{}

	opts := options.Find().
		SetSort(bson.D{
			{Key: "lastName", Value: 1},
			{Key: "firstName", Value: 1},
		})

	cursor, err := oc.Coll.Find(context.TODO(), filter, opts)
	if err != nil {
		fmt.Println("Error:", err)
		return []Official{}, fmt.Errorf("Failed to query officials.  Reason: %v", err)
	}

	for cursor.Next(context.TODO()) {
		var doc OfficialDoc
		if err := cursor.Decode(&doc); err != nil {
			fmt.Println("Error:", err)
			return []Official{}, fmt.Errorf("Failed to decode official document.  Reason: %v", err)
		}
		officials = append(officials, oc.convDocToOfficial(doc))
	}

	return officials, nil
}

func (oc *OfficialCollection) Update(id string, official Official) error {

	var filter bson.M
	var doc OfficialDoc
	var result *mongo.UpdateResult

	filter = bson.M{
		"firstName": official.FirstName,
		"lastName":  official.LastName,
	}

	doc, err := oc.convOfficialToDoc(official)
	if err != nil {
		return fmt.Errorf("Failed to convert official to document.  Reason: %v", err)
	}

	result, oc.LastError = oc.Coll.ReplaceOne(context.TODO(), filter, doc)
	if oc.LastError != nil {
		return fmt.Errorf("Failed to update official.  Reason: %v", oc.LastError)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("official not found")
	}

	fmt.Println("Record updated in", oc.Coll.Name())
	return nil
}

func (oc *OfficialCollection) DeleteAll() error {

	var result *mongo.DeleteResult

	result, oc.LastError = oc.Coll.DeleteMany(context.TODO(), bson.M{})
	if oc.LastError != nil {
		return fmt.Errorf("Failed to delete all officials.  Reason: %v", oc.LastError)
	}

	fmt.Println("Deleted ", result.DeletedCount, " records from ", oc.Coll.Name())
	return nil
}

func (oc *OfficialCollection) Delete(firstName, lastName string) error {

	var filter bson.M
	var result *mongo.DeleteResult

	filter = bson.M{
		"firstName": firstName,
		"lastName":  lastName,
	}

	result, oc.LastError = oc.Coll.DeleteOne(context.TODO(), filter)
	if oc.LastError != nil {
		return fmt.Errorf("Failed to delete official.  Reason: %v", oc.LastError)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("official not found")
	}

	fmt.Println("Deleted Record with Official Id of", filter["firstName"], " in", oc.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

func (oc *OfficialCollection) Dump(firstName, lastName string) error {

	fmt.Println("Retrieving official with Name:", firstName, lastName)

	var official Official
	official, err := oc.Get(firstName, lastName)
	if err != nil {
		return fmt.Errorf("Failed to get official.  Reason: %v", err)
	}

	fmt.Println("Dumping Record with Official Id of", official.FirstName, official.LastName)

	fmt.Println("Name:", official.FirstName, official.LastName)
	fmt.Println("Contact:", official.Phone)
	fmt.Println("Phone:", official.Phone)
	fmt.Println("Email:", official.Email)

	return nil
}
