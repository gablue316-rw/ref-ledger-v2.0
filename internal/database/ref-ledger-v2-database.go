package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"encoding/json"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var Client *mongo.Client
var IsConnected bool

var Database string
var URI string

var DatabaseVersion string = "ref-ledger-database-v2.1.0"

var ExpenseTypes []string = []string{"Camp Fee", "Dues", "Equipment", "Food", "Mileage"}

var PermittedGameStatusValues []string = []string{"Cancelled", "Completed", "Paid", "Pending", "Deleted"}
var Associations []string = []string{"GOLLC", "MCBOA", "MSO"} // Won'b be needed after developing the Association Collection

type GameDateTimeMatch struct {
	GameId      int64  `bson:"gameId"`
	Association string `bson:"association"`
}

type EnvironmentInfo struct {
	Environment string `json:"environment"`
	HostLabel   string `json:"hostLabel"`
	Database    string `json:"database"`
	Release     string `json:"release"`
}

type OfficialName struct {
	Name string `json:"name"`
}

type AssignorName struct {
	Name string `json:"name"`
}

type SiteName struct {
	Name string `json:"name"`
}

func ValidStatus(status string) bool {
	for _, s := range PermittedGameStatusValues {
		if status == s {
			return true
		}
	}
	return false
}

func MetricsAdded(tenantId, collection string, count int64) {
	err := incrementMetric(tenantId, collection, "added", count)
	if err != nil {
		log.Printf("Metrics failed: %v", err)
	}
}

func MetricsUpdated(tenantId, collection string, count int64) {
	err := incrementMetric(tenantId, collection, "updated", count)
	if err != nil {
		log.Printf("Metrics failed: %v", err)
	}
}

func MetricsDeleted(tenantId, collection string, count int64) {
	err := incrementMetric(tenantId, collection, "deleted", count)
	if err != nil {
		log.Printf("Metrics failed: %v", err)
	}
}

func MetricsErrors(tenantId, collection string, count int64) {
	err := incrementMetric(tenantId, collection, "errors", count)
	if err != nil {
		log.Printf("Metrics failed: %v", err)
	}
}

func incrementMetric(
	tenantId, collection, field string,
	count int64,
) error {
	if Client == nil || Database == "" {
		return fmt.Errorf("metrics database is not initialized")
	}
	if tenantId == "" || collection == "" {
		return fmt.Errorf("tenantId and collection are required")
	}
	if count < 0 {
		return fmt.Errorf("metric count must not be negative")
	}
	if count == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	filter := bson.M{
		"category":    "collection",
		"bucketStart": time.Now().UTC().Truncate(time.Minute),
		"collection":  collection,
		"tenantId":    tenantId,
	}

	// Initialize the other counters only when inserting a new bucket.
	initial := bson.M{}
	for _, counter := range []string{"added", "updated", "deleted", "errors"} {
		if counter != field {
			initial[counter] = int64(0)
		}
	}

	update := bson.M{
		"$inc":         bson.M{field: count},
		"$setOnInsert": initial,
	}

	_, err := Client.Database(Database).
		Collection("metrics").
		UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))

	if err != nil {
		return fmt.Errorf("increment %s metric for %s: %w",
			field, collection, err)
	}

	return nil
}

func convertDateStringToTime(date, gameTime string) (time.Time, error) {

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, err
	}

	dt, err := time.ParseInLocation(
		"1/2/2006 3:04 PM",
		date+" "+gameTime,
		loc,
	)
	if err != nil {
		return time.Time{}, err
	}

	return dt, nil
}

func GetEnvironment() EnvironmentInfo {
	envInfo := EnvironmentInfo{
		Environment: LogEnvironment(),
		HostLabel:   os.Getenv("MONGODB_ENV"),
		Database:    os.Getenv("MONGODB_NAME"),
		Release:     os.Getenv("APP_RELEASE"),
	}

	if envInfo.Environment == "" {
		envInfo.Environment = "development"
	}

	if envInfo.HostLabel == "" {
		envInfo.HostLabel = "local"
	}

	if envInfo.Database == "" {
		envInfo.Database = "refledger_v2_dev"
	}

	if envInfo.Release == "" {
		envInfo.Release = "unknown"
	}

	return envInfo
}

func GetMongoDbName() string {
	dbName := os.Getenv("MONGODB_NAME")
	if dbName == "" {
		dbName = "refLedger_v2_dev"
	}

	return dbName

}

func GetGameFee(gameIds []int64, tId, status, association string) (int64, error) {
	if strings.TrimSpace(tId) == "" || tId == "na" {
		return 0, fmt.Errorf("GetGameFee: valid tenant ID is required")
	}
	if strings.TrimSpace(association) == "" {
		return 0, fmt.Errorf("GetGameFee: association is required")
	}
	// An empty selection must never total every game for the tenant.
	if len(gameIds) == 0 {
		return 0, nil
	}
	for _, id := range gameIds {
		if id <= 0 {
			return 0, fmt.Errorf("GetGameFee: invalid game ID %d", id)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"tenantId":    tId,
		"association": association,
		"gameId":      bson.M{"$in": gameIds},
	}
	if status != "" {
		filter["status"] = status
	}

	coll := Client.Database(Database).Collection("games")
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("GetGameFee query failed: %w", err)
	}
	defer cursor.Close(ctx)

	var results []model.GameDoc
	if err := cursor.All(ctx, &results); err != nil {
		return 0, fmt.Errorf("GetGameFee read failed: %w", err)
	}

	var total int64
	for _, game := range results {
		total += gameEarningsCents(game)
	}
	return total, nil
}

// Used by HTML Web Pages
func GetGameByGameIdAndAssoc(assoc, gameId, tId string) (model.GameDescriptor, error) {

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

	filter := bson.M{
		"gameId":      id,
		"association": assoc,
		"tenantId":    tId,
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

	gameRecord := convertGameDocWithOfficials(results[0])

	return gameRecord, nil
}

func GetSingleGame(
	parentCtx context.Context,
	gameId, association, tenantId string,
) (model.GameDescriptor, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(gameId), 10, 64)
	if err != nil || id <= 0 {
		return model.GameDescriptor{}, fmt.Errorf("invalid game ID %q", gameId)
	}

	if strings.TrimSpace(association) == "" ||
		strings.TrimSpace(tenantId) == "" ||
		tenantId == "na" {
		return model.GameDescriptor{}, fmt.Errorf(
			"association and valid tenant ID are required",
		)
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{
		"gameId":      id,
		"association": association,
		"tenantId":    tenantId,
	}

	coll := Client.Database(Database).Collection("games")

	// Read at most two records so duplicate composite identities are detected.
	cursor, err := coll.Find(ctx, filter, options.Find().SetLimit(2))
	if err != nil {
		return model.GameDescriptor{}, fmt.Errorf("find game: %w", err)
	}
	defer cursor.Close(ctx)

	var games []model.GameDoc
	if err := cursor.All(ctx, &games); err != nil {
		return model.GameDescriptor{}, fmt.Errorf("read game: %w", err)
	}

	if len(games) == 0 {
		return model.GameDescriptor{}, fmt.Errorf(
			"game %d for association %q: %w",
			id, association, mongo.ErrNoDocuments,
		)
	}

	if len(games) > 1 {
		return model.GameDescriptor{}, fmt.Errorf(
			"multiple games found for game ID %d, association %q, tenant ID %q",
			id, association, tenantId,
		)
	}

	return convertGameDocWithOfficials(games[0]), nil
}

func GetExpenseRegistry(
	filter model.ExpenseRegistryFilter,
) ([]model.ExpenseDescriptor, error) {

	expenseFilter := bson.M{
		"tenantId": filter.TenantId,
	}

	if filter.ExpenseId != "" {
		expenseFilter["expenseId"] = filter.ExpenseId
	}

	if filter.Association != "" {
		expenseFilter["association"] = filter.Association
	}

	if filter.Type != "" {
		expenseFilter["type"] = filter.Type
	}

	if filter.Date != "" {
		expenseFilter["date"] = filter.Date
	}

	if filter.Description != "" {
		expenseFilter["description"] = filter.Description
	}

	if filter.TaxYear != "" {
		expenseFilter["taxYear"] = filter.TaxYear
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	coll := Client.
		Database(Database).
		Collection("expenses")

	cursor, err := coll.Find(ctx, expenseFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to find expenses: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	var results []model.ExpenseDoc

	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf(
			"failed to read expenses: %w",
			err,
		)
	}

	expenseRecords := make(
		[]model.ExpenseDescriptor,
		0,
		len(results),
	)

	for _, record := range results {
		expenseRecords = append(
			expenseRecords,
			utils.ConvertExpenseDocToExpenseDescr(record),
		)
	}

	return expenseRecords, nil
}

func GetPaymentRegistry(
	filter model.PaymentRegistryFilter,
) ([]model.PaymentDescriptor, error) {
	paymentFilter := bson.M{"tenantId": filter.TenantId}

	taxYear := strings.TrimSpace(filter.TaxYear)
	if taxYear != "" {
		year, err := strconv.Atoi(taxYear)
		if err != nil || year < 1000 || year > 9999 {
			return nil, fmt.Errorf("invalid tax year: %q", taxYear)
		}
		paymentFilter["taxYear"] = year
	}

	if filter.PaymentId != "" {
		paymentFilter["paymentId"] = filter.PaymentId
	}
	if filter.Association != "" {
		paymentFilter["association"] = filter.Association
	}
	if filter.Date != "" {
		paymentFilter["paymentDate"] = filter.Date
	}
	if filter.Amount > 0 {
		paymentFilter["paymentAmt"] = filter.Amount
	}
	if len(filter.GameIds) > 0 {
		paymentFilter["gameIds"] = bson.M{
			"$in": filter.GameIds,
		}
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("payments")

	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: paymentFilter}},
		bson.D{{Key: "$addFields", Value: bson.M{
			"sortDate": bson.M{
				"$dateFromString": bson.M{
					"dateString": "$paymentDate",
					"format":     "%m/%d/%Y",
				},
			},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "sortDate", Value: 1},
			{Key: "paymentId", Value: 1},
		}}},
		bson.D{{Key: "$unset", Value: "sortDate"}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		fmt.Println("Error", err)
		return []model.PaymentDescriptor{}, err
	}
	defer cursor.Close(ctx)

	var results []model.PaymentDoc
	paymentRecords := make([]model.PaymentDescriptor, 0)

	if err := cursor.All(ctx, &results); err != nil {
		fmt.Println("Error", err)
		return []model.PaymentDescriptor{}, err
	}

	for _, r := range results {
		record := utils.ConvertPaymentDocToPaymentDescr(r)

		record.TaxYear = ""
		if r.TaxYear != 0 {
			record.TaxYear = strconv.Itoa(r.TaxYear)
		}

		paymentRecords = append(paymentRecords, record)
	}

	return paymentRecords, nil
}

func DeletePayment(paymentId, association, tid string, versions ...string) error {
	return MutatePayment(context.Background(), tid, paymentId, association, firstVersion(versions), nil)
}

// documentExists stops at the first match and only retrieves its identity.
func documentExists(ctx context.Context, coll *mongo.Collection, filter bson.M) (bool, error) {
	err := coll.FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, err
}

func PaymentExists(doc model.PaymentDoc, tenantId string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return documentExists(ctx, Client.Database(Database).Collection("payments"), bson.M{
		"paymentId": doc.PaymentId, "association": doc.Association, "tenantId": tenantId,
	})
}

func GameExists(doc model.GameDoc) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return documentExists(ctx, Client.Database(Database).Collection("games"), bson.M{
		"gameId": doc.GameId, "association": doc.Association, "tenantId": doc.TenantId,
	})
}

func BuildMongoGameFilter(filter model.GameFilter) bson.M {

	mongoFilter := bson.M{}

	mongoFilter["tenantId"] = filter.TenantId

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

	if len(filter.Assignor) > 0 {
		mongoFilter["assignor"] = bson.M{
			"$in": filter.Assignor,
		}
	}

	if strings.TrimSpace(filter.TaxYear) != "" {
		mongoFilter["taxYear"] = strings.TrimSpace(filter.TaxYear)
	}

	var officialConditions bson.A
	if len(filter.Official) > 0 {
		officialConditions = append(
			officialConditions,
			bson.M{
				"officials": bson.M{
					"$elemMatch": bson.M{
						"$or": bson.A{
							bson.M{
								"name": bson.M{
									"$in": filter.Official,
								},
							},
							bson.M{
								"officialId": bson.M{
									"$in": filter.Official,
								},
							},
						},
					},
				},
			},
		)
	}

	//
	// Do I need this code
	//
	if len(filter.ECO) > 0 {
		officialConditions = append(officialConditions, roleOfficialFilter("ECO", filter.ECO))
	}
	// Preserve legacy positional-filter OR behavior without overwriting other filters.
	var positional bson.A
	if filter.Referee != "" {
		positional = append(positional, roleOfficialFilter("Referee", []string{filter.Referee}))
	}
	if filter.U1 != "" {
		positional = append(positional, roleOfficialFilter("U1", []string{filter.U1}))
	}
	if filter.U2 != "" {
		positional = append(positional, roleOfficialFilter("U2", []string{filter.U2}))
	}
	if len(positional) > 0 {
		officialConditions = append(officialConditions, bson.M{"$or": positional})
	}
	//
	// End of Do I Need this code
	//

	for _, assignment := range filter.Officials {
		entry := bson.M{}
		if assignment.RoleId != "" {
			entry["roleId"] = assignment.RoleId
		}
		if assignment.Title != "" {
			entry["title"] = assignment.Title
		}
		if assignment.OfficialId != "" {
			entry["officialId"] = assignment.OfficialId
		}
		if len(entry) > 0 {
			officialConditions = append(officialConditions, bson.M{"officials": bson.M{"$elemMatch": entry}})
		}
	}
	if len(officialConditions) > 0 {
		mongoFilter["$and"] = officialConditions
	}
	if strings.TrimSpace(filter.Home) != "" {
		mongoFilter["home"] = strings.TrimSpace(filter.Home)
	}
	if strings.TrimSpace(filter.Visitor) != "" {
		mongoFilter["visitor"] = strings.TrimSpace(filter.Visitor)
	}

	// Filter by gameDateTime. Dates are supplied as M/D/YYYY.
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		fmt.Println("Unable to load game timezone:", err)
	} else {
		dateRange := bson.M{}

		if begin := strings.TrimSpace(filter.BeginDate); begin != "" {
			beginDate, err := time.ParseInLocation("1/2/2006", begin, location)
			if err != nil {
				fmt.Println("Invalid Begin Date:", err)
			} else {
				dateRange["$gte"] = beginDate
			}
		}

		if end := strings.TrimSpace(filter.EndDate); end != "" {
			endDate, err := time.ParseInLocation("1/2/2006", end, location)
			if err != nil {
				fmt.Println("Invalid End Date:", err)
			} else {
				// Exclude midnight after End Date, including every game on End Date.
				dateRange["$lt"] = endDate.AddDate(0, 0, 1)
			}
		}

		if len(dateRange) > 0 {
			mongoFilter["gameDateTime"] = dateRange
		}
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
	err = os.WriteFile("gamesReportFilters.bson", []byte(text), 0644)
	if err != nil {
		fmt.Println(err)
	}

	return mongoFilter
}

func IsSessionIndexed() bool {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("sessions")

	cursor, err := coll.Indexes().List(ctx)
	if err != nil {
		fmt.Println(err)
		return false
	}

	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		fmt.Println(err)
		return false
	}

	if len(indexes) > 0 {
		return true
	}

	return false

}

func CreateSessionIndices() {

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("sessions")

	name, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})

	if err != nil {
		fmt.Println("Failed to create", name, "index for collection session")
		return
	}

	fmt.Println("Created index:", name)

}

func GetSession(r *http.Request) (*model.Session, error) {

	cookie, err := r.Cookie("rl_session")
	if err != nil {
		return nil, err
	}

	var session model.Session

	err = Client.
		Database(GetMongoDbName()).
		Collection("sessions").
		FindOne(
			r.Context(),
			bson.M{"sessionId": cookie.Value},
		).
		Decode(&session)

	if err != nil {
		return nil, err
	}

	// Session expired?
	if time.Now().After(session.ExpiresAt) {
		return nil, errors.New("session expired")
	}

	return &session, nil
}

func getEnv(name string, defaultValue string) string {
	value := os.Getenv(name)

	if value == "" {
		return defaultValue
	}

	return value
}

func BuildAtlasURI() (string, bool, error) {

	fmt.Println("Building Atlas URI")

	username := os.Getenv("MONGODB_USERNAME")
	password := os.Getenv("MONGODB_PASSWORD")

	if username == "" {
		return "", true, errors.New("MONGODB_USERNAME is not set")
	}

	if password == "" {
		return "", true, errors.New("MONGODB_PASSWORD is not set")
	}

	host := getEnv(
		"MONGODB_HOST",
		"ref-ledger-cluster.2hni1bk.mongodb.net",
	)

	appName := getEnv(
		"MONGODB_APP_NAME",
		"ref-ledger-cluster",
	)

	database := getEnv(
		"MONGODB_NAME",
		"refLedger_v2",
	)

	mongoURL := &url.URL{
		Scheme: "mongodb+srv",
		User:   url.UserPassword(username, password),
		Host:   host,
		Path:   "/" + database,
	}

	query := url.Values{}
	query.Set("appName", appName)

	mongoURL.RawQuery = query.Encode()

	return mongoURL.String(), true, nil
}

func BuildLocalURI() (string, bool, error) {

	fmt.Println("Building Local URI")
	host := getEnv(
		"MONGODB_HOST",
		"localhost:27017",
	)

	database := getEnv(
		"MONGODB_NAME",
		"refLedger_v2_dev",
	)

	replicaSet := getEnv(
		"MONGODB_REPLICA_SET",
		"refLedgerRS",
	)

	mongoURL := &url.URL{
		Scheme: "mongodb",
		Host:   host,
		Path:   "/" + database,
	}

	query := url.Values{}

	if replicaSet != "" {
		query.Set("replicaSet", replicaSet)
	}

	if os.Getenv("MONGODB_DIRECT_CONNECTION") == "true" {
		query.Set("directConnection", "true")
	}

	mongoURL.RawQuery = query.Encode()

	return mongoURL.String(), false, nil
}

func BuildMongoURI() (mongoURI string, isAtlas bool, err error) {

	fmt.Println("Building MONGO URI")
	mongoEnvironment := os.Getenv("MONGODB_ENV")

	switch mongoEnvironment {
	case "atlas":
		return BuildAtlasURI()

	default:
		return BuildLocalURI()
	}
}

func VerifyMongoConnection(client *mongo.Client) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	message := ""

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return message, fmt.Errorf("MongoDB ping failed: %w", err)
	}

	mongoURI := URI
	parsedURI, err := url.Parse(mongoURI)
	if err != nil {
		return message, fmt.Errorf("invalid MongoDB URI: %w", err)
	}

	message = fmt.Sprintf(
		"Successfully connected to MongoDB: host=%s database=%s",
		parsedURI.Hostname(),
		os.Getenv("MONGODB_NAME"))

	return message, nil
}

func Connect() error {

	if Client != nil {
		return nil
	}

	uri, _, err := BuildMongoURI()

	if err != nil {
		return err
	}

	fmt.Println("Connecting to configured MongoDB endpoint")

	Database = GetMongoDbName()
	URI = uri

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
		return fmt.Errorf("MongoDB build information check failed: %w", err)
	}

	fmt.Println("MongoDB Version:", result["version"])
	return nil

}

// DELETE THIS
/*
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
*/

func GetPayment(paymentId, association, tid string) (model.PaymentDoc, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(Database)
	coll := db.Collection("payments")

	filter := bson.M{
		"paymentId":   paymentId,
		"association": association,
		"tenantId":    tid,
	}

	var result model.PaymentDoc
	err := coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		return model.PaymentDoc{}, fmt.Errorf("FindOne failure.  Reason: %s", err)
	}

	return result, nil
}

func DeleteOneDoc(parentCtx context.Context, filter bson.M, dbase, collection string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	tenantId, _ := filter["tenantId"].(string)
	trace := NewWriteTrace(ctx, "document.delete", collectionName, tenantId, "", "", map[string]any{
		"gameId": filter["gameId"],
	})

	result, err := coll.DeleteOne(ctx, filter)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return
	}

	if tenantId != "" {
		MetricsDeleted(tenantId, collection, result.DeletedCount)
	}
	if result.DeletedCount == 0 {
		trace.Complete(map[string]any{"deletedCount": result.DeletedCount}, nil, "unmatched")
		return
	}
	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
}

func UpdateGameStatusToPaid(
	parentCtx context.Context,
	gameIds []int64,
	association, tenantId string,
) (int, error) {
	if strings.TrimSpace(association) == "" ||
		strings.TrimSpace(tenantId) == "" ||
		tenantId == "na" {
		return 0, fmt.Errorf("association and valid tenant ID are required")
	}

	// Validate all IDs before making changes.
	uniqueIDs := make([]int64, 0, len(gameIds))
	seen := make(map[int64]bool)

	for _, id := range gameIds {
		if id <= 0 {
			return 0, fmt.Errorf("invalid game ID %d", id)
		}
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	coll := Client.Database(Database).Collection("games")
	trace := NewWriteTrace(ctx, "games.mark_paid", coll.Name(), tenantId, "", "", map[string]any{
		"association": association, "gameCount": int64(len(uniqueIDs)),
	})
	updated := 0
	var updateErrors []error

	for _, id := range uniqueIDs {
		// Check the complete identity and detect duplicate documents.
		game, err := GetSingleGame(
			ctx,
			strconv.FormatInt(id, 10),
			association,
			tenantId,
		)
		if err != nil {
			updateErrors = append(updateErrors, err)
			continue
		}

		if game.Status == "Paid" {
			continue
		}

		if game.Status != "Pending" && game.Status != "Completed" {
			updateErrors = append(updateErrors, fmt.Errorf(
				"game %d for association %q cannot become Paid from status %q",
				id, association, game.Status,
			))
			continue
		}

		filter := bson.M{
			"gameId":      id,
			"association": association,
			"tenantId":    tenantId,
			// Recheck status during the write.
			"status": game.Status,
		}

		result, err := coll.UpdateOne(
			ctx,
			filter,
			bson.M{"$set": bson.M{"status": "Paid"}},
		)
		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf(
				"update game %d to Paid: %w", id, err,
			))
			continue
		}

		if result.MatchedCount == 0 {
			updateErrors = append(updateErrors, fmt.Errorf(
				"game %d for association %q was removed or its status changed; reload and retry",
				id, association,
			))
			continue
		}

		updated += int(result.ModifiedCount)
		trace.Event(LogLevelInfo, "game.status.updated", "Game status updated", map[string]any{
			"gameId": id, "association": association, "status": "Paid",
		})
	}

	if err := errors.Join(updateErrors...); err != nil {
		outcome := "failed"
		if updated > 0 {
			outcome = "partial"
		}
		trace.Failure(err, map[string]any{
			"modifiedCount": int64(updated), "partial": updated > 0,
		}, outcome)
		return updated, err
	}
	if updated == 0 {
		trace.Complete(map[string]any{"modifiedCount": int64(updated)}, nil, "no_op")
	} else {
		trace.Success(map[string]any{"modifiedCount": int64(updated)}, nil)
	}
	return updated, nil
}

func VerifyGameStatus(gameIds []int64, association, tenantId string) []error {
	return verifySelectedGames(gameIds, association, tenantId, true)
}

func VerifyGameMatchesAssociation(gameIds []int64, association, tenantId string) []error {
	return verifySelectedGames(gameIds, association, tenantId, false)
}

// Fetch all requested identities in one bounded query, rather than one round trip per game.
func verifySelectedGames(ids []int64, association, tenantID string, completedOnly bool) []error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filter := bson.M{"tenantId": tenantID, "association": association, "gameId": bson.M{"$in": ids}}
	if completedOnly {
		filter["status"] = "Completed"
	}
	cursor, err := Client.Database(Database).Collection("games").Find(ctx, filter,
		options.Find().SetProjection(bson.M{"_id": 0, "gameId": 1}))
	if err != nil {
		return []error{fmt.Errorf("verify games: %w", err)}
	}
	defer cursor.Close(ctx)
	var rows []struct {
		ID int64 `bson:"gameId"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return []error{fmt.Errorf("decode selected games: %w", err)}
	}
	found := make(map[int64]bool, len(rows))
	for _, row := range rows {
		found[row.ID] = true
	}
	return missingSelectedGames(ids, found, association, completedOnly)
}

func missingSelectedGames(ids []int64, found map[int64]bool, association string, completedOnly bool) []error {
	var failures []error
	for _, id := range ids {
		if found[id] {
			continue
		}
		message := fmt.Sprintf("game %d was not found assigned to association %s", id, association)
		if completedOnly {
			message += " in Completed Status"
		}
		failures = append(failures, errors.New(message))
	}
	return failures
}

//
// Example Calls
//

/*
MetricsAdded(tenantId, "games", 1)

MetricsUpdated(tenantId, "games", 3)

MetricsDeleted(tenantId, "sites", 2)

MetricsErrors(tenantId, "officials", 1)
*/

func InsertPaymentDocs(
	parentCtx context.Context,
	payment []model.PaymentDescriptor,
	dbase, collection, tenantId string,
) (int, int, int, []error) {
	if dbase != Database || collection != "payments" {
		err := fmt.Errorf("invalid payment database or collection")
		return 0, len(payment), 0, []error{err}
	}
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	recordsInserted := 0
	totalErrors := 0
	gamesUpdatedToPaid := 0
	importErrors := make([]error, 0)

	for _, row := range payment {
		paymentDate, err := time.Parse("1/2/2006", strings.TrimSpace(row.PaymentDate))
		if err != nil {
			importErrors = append(importErrors, fmt.Errorf("invalid payment date %q: %w", row.PaymentDate, err))
			totalErrors++
			continue
		}
		taxYear := paymentDate.Year()
		if yearText := strings.TrimSpace(row.TaxYear); yearText != "" {
			taxYear, err = strconv.Atoi(yearText)
			if err != nil || taxYear < utils.MinTaxYear || taxYear > utils.MaxTaxYear {
				importErrors = append(importErrors, fmt.Errorf("invalid tax year %q", row.TaxYear))
				totalErrors++
				continue
			}
		}
		amount, err := utils.ParseAmountCents(row.PaymentAmt)
		if err != nil {
			importErrors = append(importErrors, fmt.Errorf("invalid payment amount: %w", err))
			totalErrors++
			continue
		}
		deduction, deductionType, err := NormalizePaymentDeduction(row.DeductionAmount, row.DeductionType)
		if err != nil {
			importErrors = append(importErrors, err)
			totalErrors++
			continue
		}
		gameIDs, err := utils.ConvertGameIdStrToInt(row.GameIds)
		if err != nil || len(gameIDs) == 0 {
			if err == nil {
				err = fmt.Errorf("payment must include at least one game")
			}
			importErrors = append(importErrors, err)
			totalErrors++
			continue
		}
		row.PaymentId = strings.TrimSpace(row.PaymentId)
		row.Association = strings.TrimSpace(row.Association)
		if row.PaymentId == "" || row.Association == "" {
			importErrors = append(importErrors, fmt.Errorf("payment ID and association are required"))
			totalErrors++
			continue
		}
		doc := model.PaymentDoc{
			PaymentId:       row.PaymentId,
			PaymentDate:     paymentDate.Format("1/2/2006"),
			PaymentAmt:      amount,
			Association:     row.Association,
			GameIds:         gameIDs,
			TenantId:        tenantId,
			TaxYear:         taxYear,
			DeductionAmount: deduction,
			DeductionType:   deductionType,
		}

		trace := NewWriteTrace(ctx, "payment.import", "payments", tenantId, "", "", map[string]any{
			"paymentId": doc.PaymentId, "association": doc.Association,
			"gameCount": int64(len(gameIDs)), "taxYear": strconv.Itoa(doc.TaxYear),
			"affectedCollections": []string{"payments", "games", "expenses"},
		})
		var insertedPaymentID any
		var changedGames int64
		session, err := Client.StartSession()
		if err == nil {
			_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
				changedGames = 0
				insertedPaymentID = nil
				payments := Client.Database(Database).Collection("payments")
				games := Client.Database(Database).Collection("games")
				count, err := payments.CountDocuments(tx, bson.M{
					"tenantId": tenantId, "association": doc.Association, "paymentId": doc.PaymentId,
				})
				if err != nil {
					return nil, err
				}
				if count != 0 {
					return nil, fmt.Errorf("payment with ID already exists")
				}

				seen := make(map[int64]bool, len(gameIDs))
				var gameEarnings int64
				for _, gameID := range gameIDs {
					if gameID <= 0 || seen[gameID] {
						return nil, fmt.Errorf("payment game IDs must be distinct positive integers")
					}
					seen[gameID] = true
					var game model.GameDoc
					filter := bson.M{"tenantId": tenantId, "association": doc.Association, "gameId": gameID}
					if err := games.FindOne(tx, filter).Decode(&game); err != nil {
						return nil, fmt.Errorf("load selected game %d: %w", gameID, err)
					}
					if game.Status != "Completed" {
						return nil, fmt.Errorf("game %d must be Completed before payment", gameID)
					}
					gameEarnings += gameEarningsCents(game)
				}
				if deduction > gameEarnings {
					return nil, fmt.Errorf("payment deduction exceeds selected-game earnings")
				}
				expectedDeposit := gameEarnings - deduction
				if expectedDeposit < 0 || amount != expectedDeposit {
					return nil, fmt.Errorf(
						"payment amount mismatch: expected deposit %s, got %s",
						utils.ConvertInt64ToAmtStr(expectedDeposit),
						utils.ConvertInt64ToAmtStr(amount),
					)
				}

				inserted, err := payments.InsertOne(tx, doc)
				if err != nil {
					return nil, err
				}
				insertedPaymentID = inserted.InsertedID
				for _, gameID := range gameIDs {
					result, err := games.UpdateOne(
						tx,
						bson.M{"tenantId": tenantId, "association": doc.Association, "gameId": gameID, "status": "Completed"},
						bson.M{"$set": bson.M{"status": "Paid"}},
					)
					if err != nil {
						return nil, err
					}
					if result.MatchedCount != 1 {
						return nil, ErrConflict
					}
					changedGames += result.ModifiedCount
				}
				if err := syncPaymentDeduction(tx, tenantId, doc); err != nil {
					return nil, fmt.Errorf("save payroll deduction expense: %w", err)
				}
				return nil, nil
			})
			session.EndSession(ctx)
			gamesUpdatedToPaid += int(changedGames)
		}
		if err != nil {
			category, _ := classifyWriteError(err)
			trace.Failure(err, map[string]any{
				"modifiedCount": changedGames, "partial": false,
			}, writeFailureOutcome(category))
			importErrors = append(importErrors, fmt.Errorf("payment %s: %w", doc.PaymentId, err))
			totalErrors++
			MetricsErrors(tenantId, "payments", 1)
			continue
		}
		trace.EventForCollection("payments", LogLevelInfo, "payment.import.inserted", "Payment record inserted", map[string]any{
			"mongoId": fmt.Sprint(insertedPaymentID), "insertedCount": int64(1),
		})
		trace.EventForCollection("games", LogLevelInfo, "payment.import.games_updated", "Selected game statuses updated", map[string]any{
			"modifiedCount": changedGames,
		})
		trace.EventForCollection("expenses", LogLevelInfo, "payment.import.deduction_synced", "Payroll deduction synchronized", nil)
		trace.Success(map[string]any{
			"modifiedCount": changedGames, "affectedCollections": []string{"payments", "games", "expenses"},
		}, insertedPaymentID)
		recordsInserted++
		MetricsAdded(tenantId, "payments", 1)
		if deduction > 0 {
			MetricsAdded(tenantId, "expenses", 1)
		}
	}

	return recordsInserted, totalErrors, gamesUpdatedToPaid, importErrors
}

func UpdatePaymentDocs(parentCtx context.Context, payments []model.PaymentDescriptor, dbase, collection, tenantId string) (int, int, int, []error) {
	updated := 0
	failures := []error{}
	if dbase != Database || collection != "payments" {
		return 0, len(payments), 0, []error{fmt.Errorf("invalid payment database or collection")}
	}
	for _, payment := range payments {
		if err := MutatePayment(parentCtx, tenantId, payment.PaymentId, payment.Association, payment.Version, &payment); err != nil {
			failures = append(failures, err)
		} else {
			updated++
		}
	}
	return updated, len(failures), 0, failures
}

func UpdateOneGameDoc(parentCtx context.Context, game model.GameDescriptor, dbase, collection, tId string) error {
	if game.Version == "" {
		return ErrConflict
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	var err error

	db := Client.Database(dbase)
	coll := db.Collection(collection)

	doc := convertGameDescrWithOfficials(game)
	doc.TenantId = tId

	doc.GameDateTime, err = convertDateStringToTime(game.Date, game.Time)
	if err != nil {
		return err
	}

	filter := bson.M{
		"gameId":      doc.GameId,
		"association": doc.Association,
		"tenantId":    tId,
	}

	var previous model.GameDoc
	if err := coll.FindOne(ctx, filter).Decode(&previous); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrConflict
		}
		return err
	}
	if previous.Version != game.Version {
		return ErrConflict
	}
	doc.Deductions = previous.Deductions
	if err := prepareGameOfficials(ctx, coll.Database(), &doc, &previous); err != nil {
		return err
	}
	update := bson.M{
		"$set":   doc,
		"$unset": bson.M{"referee": "", "u1": "", "u2": "", "eco": ""},
	}

	conditional, conflictErr := ConditionalFilter(ctx, coll, filter, game.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "game.update", coll.Name(), tId, "", "", map[string]any{
		"gameId": doc.GameId, "association": doc.Association, "sport": doc.Sport,
		"level": doc.Level, "site": doc.Site, "field": doc.Field,
		"gameCount": doc.NumOfGames, "status": doc.Status, "date": game.Date,
	})
	result, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return err
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	MetricsUpdated(tId, "games", result.ModifiedCount)

	fmt.Println("Updated Match:", result.MatchedCount, "Updated document:", result.ModifiedCount)
	return nil
}

func InsertGameDocs(parentCtx context.Context, game []model.GameDescriptor, dbase, collection, tId string) {

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	var err error

	db := Client.Database(dbase)
	coll := db.Collection(collection)
	collectionName := coll.Name()
	fmt.Println("Inserting records into", collectionName)

	recordsInserted := 0
	var singleInsertedID any
	totalErrors := 0
	var failureErr error
	trace := NewWriteTrace(ctx, "games.import", collectionName, tId, "", "", map[string]any{
		"insertedCount": int64(0),
		"gameCount":     int64(len(game)),
	})

	for _, v := range game {

		doc := convertGameDescrWithOfficials(v)
		doc.TenantId = tId
		doc.GameDateTime, err = convertDateStringToTime(v.Date, v.Time)
		if err != nil {
			totalErrors++
			failureErr = errors.Join(failureErr, ErrStructuredLogValidation)
			MetricsErrors(tId, "games", 1)
			continue
		}

		if doc.GameDateTime.Year() < utils.MinTaxYear ||
			doc.GameDateTime.Year() > utils.MaxTaxYear {

			totalErrors++
			failureErr = errors.Join(failureErr, ErrStructuredLogValidation)
			fmt.Printf(
				"Game %s: invalid game date; expected a date between 1/1/%d and 12/31/%d\n",
				v.GameId,
				utils.MinTaxYear,
				utils.MaxTaxYear,
			)

			MetricsErrors(tId, "games", 1)

			continue
		}

		// Default a blank tax year to the game date's year.
		taxYearText := strings.TrimSpace(v.TaxYear)
		if taxYearText == "" {
			taxYearText = strconv.Itoa(doc.GameDateTime.Year())
		}

		taxYear, err := strconv.Atoi(taxYearText)
		if err != nil ||
			len(taxYearText) != 4 ||
			taxYear < utils.MinTaxYear ||
			taxYear > utils.MaxTaxYear {

			totalErrors++
			failureErr = errors.Join(failureErr, ErrStructuredLogValidation)
			fmt.Printf("Game %s: %v\n", v.GameId, utils.ErrInvalidTaxYear)

			MetricsErrors(tId, "games", 1)
			continue
		}

		doc.TaxYear = taxYearText

		if err := prepareGameOfficials(ctx, coll.Database(), &doc, nil); err != nil {
			totalErrors++
			failureErr = errors.Join(failureErr, err)
			fmt.Printf("Game import officials preparation failed: category=%s\n", SafeErrorCategory(err))
			taxYearText := strings.TrimSpace(v.TaxYear)
			if taxYearText == "" {
				taxYearText = strconv.Itoa(doc.GameDateTime.Year())
			}

			taxYear, err := strconv.Atoi(taxYearText)
			if err != nil ||
				len(taxYearText) != 4 ||
				taxYear < utils.MinTaxYear ||
				taxYear > utils.MaxTaxYear {

				totalErrors++
				fmt.Printf("Game %s: %v\n", v.GameId, utils.ErrInvalidTaxYear)
				MetricsErrors(tId, "games", 1)
				continue
			}

			doc.TaxYear = taxYearText
			continue
		}

		gameExists, err := GameExists(doc)

		if gameExists || err != nil {
			totalErrors++
			if err != nil {
				failureErr = errors.Join(failureErr, err)
				fmt.Printf("Game import duplicate check failed: category=%s\n", SafeErrorCategory(err))
			} else {
				failureErr = errors.Join(failureErr, ErrConflict)
			}
			MetricsErrors(tId, "games", 1)
			continue
		}
		result, err := coll.InsertOne(ctx, doc)
		if err != nil {
			trace.Failure(err, map[string]any{
				"insertedCount": int64(recordsInserted), "partial": recordsInserted > 0,
			}, map[bool]string{true: "partial", false: "failed"}[recordsInserted > 0])
			fmt.Printf("Game import insert failed: category=%s\n", SafeErrorCategory(err))
			MetricsErrors(tId, "games", 1)
			return
		}

		trace.EventWithMongoID(LogLevelInfo, "game.import.item_inserted", "Game import item inserted", map[string]any{
			"gameId": v.GameId, "association": doc.Association, "sport": doc.Sport,
			"level": doc.Level, "site": doc.Site, "field": doc.Field,
			"gameCount": doc.NumOfGames, "status": doc.Status, "date": v.Date,
		}, result.InsertedID)
		if len(game) == 1 {
			singleInsertedID = result.InsertedID
		}
		MetricsAdded(tId, "games", 1)
		recordsInserted++
	}
	fmt.Println("Total Records inserted into", collectionName, ":", recordsInserted)
	fmt.Println("Total Errors:", totalErrors)
	if len(game) == 0 {
		trace.Complete(nil, nil, "no_op")
		return
	}
	if totalErrors > 0 {
		trace.Failure(failureErr, map[string]any{
			"insertedCount": int64(recordsInserted), "partial": recordsInserted > 0,
		}, map[bool]string{true: "partial", false: "failed"}[recordsInserted > 0])
		return
	}
	trace.Success(map[string]any{"insertedCount": int64(recordsInserted)}, singleInsertedID)
}

// Association Colleciton, Documents and API Code
type AssociationJson struct {
	Version   string `bson:"-" json:"version"`
	Id        string `json:"id"`
	Name      string `json:"name"`
	Contact   string `json:"contact"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Assignors string `json:"assignors"`
}

type AssociationDoc struct {
	Version   string `bson:"-" json:"version"`
	Id        string `bson:"id,omitempty"`
	Name      string `bson:"name,omitempty"`
	Contact   string `bson:"contact,omitempty"`
	Phone     string `bson:"phone,omitempty"`
	Email     string `bson:"email,omitempty"`
	Assignors string `bson:"assignors,omitempty"`
	TenantId  string `bson:"tenantId"`
}

type Association struct {
	Version   string `bson:"-" json:"version"`
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

type AssociationName struct {
	Name string `json:"name"`
}

type AssociationId struct {
	Id string `json:"id"`
}

func (ac *AssociationCollection) Init(client *mongo.Client) error {

	ac.DB = client.Database(Database)
	ac.Coll = ac.DB.Collection("associations")

	return nil
}

func (ac *AssociationCollection) CreateIndices() error {
	_, err := ac.Coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenantId", Value: 1},
			{Key: "id", Value: 1},
		},
		Options: options.Index().SetName("tenant_association_id").SetUnique(true),
	})
	return err
}

func (ac *AssociationCollection) ConvAssocJsonToAssoc(
	association AssociationJson,
) Association {
	return Association{
		Version:   association.Version,
		Id:        association.Id,
		Name:      association.Name,
		Contact:   association.Contact,
		Phone:     association.Phone,
		Email:     association.Email,
		Assignors: association.Assignors,
	}
}

func (ac *AssociationCollection) convAssocToDoc(association Association) AssociationDoc {
	return AssociationDoc{
		Version:   association.Version,
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
		Version:   doc.Version,
		Id:        doc.Id,
		Name:      doc.Name,
		Contact:   doc.Contact,
		Phone:     doc.Phone,
		Email:     doc.Email,
		Assignors: doc.Assignors,
	}
}

func (ac *AssociationCollection) Exists(id string, tenantId string) (bool, error) {

	var doc AssociationDoc

	filter := bson.M{
		"id":       id,
		"tenantId": tenantId,
	}

	err := ac.Coll.FindOne(context.TODO(), filter).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (ac *AssociationCollection) Add(association Association, tenantId string) error {
	return ac.AddWithContext(context.TODO(), association, tenantId)
}

func (ac *AssociationCollection) AddWithContext(parentCtx context.Context, association Association, tenantId string) error {
	var result *mongo.InsertOneResult

	tenantId = strings.TrimSpace(tenantId)
	association.Id = strings.TrimSpace(association.Id)
	if tenantId == "" || tenantId == "na" || association.Id == "" {
		return fmt.Errorf("association ID and valid tenant ID are required")
	}
	association.Name = strings.TrimSpace(association.Name)
	association.Contact = strings.TrimSpace(association.Contact)
	association.Phone = strings.TrimSpace(association.Phone)
	association.Email = strings.TrimSpace(association.Email)
	if association.Email != "" && !validEmail(association.Email) {
		return fmt.Errorf("association email address is invalid")
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	assocExists, err := ac.Exists(association.Id, tenantId)
	if err != nil {
		MetricsErrors(tenantId, "associations", 1)
		return fmt.Errorf("error occurred while checking association existence: %v", err)
	}

	if assocExists {
		MetricsErrors(tenantId, "associations", 1)
		return fmt.Errorf("association with ID %q already exists", association.Id)
	}

	doc := ac.convAssocToDoc(association)
	doc.TenantId = tenantId

	trace := NewWriteTrace(ctx, "association.insert", ac.Coll.Name(), tenantId, "", "", map[string]any{
		"associationId": association.Id,
	})
	result, ac.LastError = ac.Coll.InsertOne(ctx, doc)

	if ac.LastError != nil {
		trace.Failure(ac.LastError, nil, "failed")
		MetricsErrors(tenantId, "associations", 1)
		if mongo.IsDuplicateKeyError(ac.LastError) {
			return fmt.Errorf("association with ID %q already exists", association.Id)
		}
		return fmt.Errorf("Insert failed.  Reason: %w", ac.LastError)
	}

	trace.Success(nil, result.InsertedID)
	MetricsAdded(tenantId, "associations", 1)

	fmt.Println("Inserted ID:", result.InsertedID)

	return nil
}

func (ac *AssociationCollection) AddAssignor(name, tenantId, associationId string) error {
	return ac.AddAssignorWithContext(context.Background(), name, tenantId, associationId)
}

func (ac *AssociationCollection) AddAssignorWithContext(parentCtx context.Context, name, tenantId, associationId string) error {
	name = strings.TrimSpace(name)
	tenantId = strings.TrimSpace(tenantId)
	associationId = strings.TrimSpace(associationId)
	if name == "" || strings.Contains(name, ",") || tenantId == "" || tenantId == "na" || associationId == "" {
		return fmt.Errorf("assignor name and association are required")
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	// Match a complete comma-separated identity; the conditional update makes
	// concurrent attempts to add the same assignor serialize on the association.
	identityPattern := `(?:^|,)\s*` + regexp.QuoteMeta(name) + `\s*(?:,|$)`
	filter := bson.M{
		"id":       associationId,
		"tenantId": tenantId,
		"assignors": bson.M{
			"$not": primitive.Regex{Pattern: identityPattern, Options: "i"},
		},
	}
	literalName := bson.M{"$literal": name}
	update := mongo.Pipeline{
		{{Key: "$set", Value: bson.M{
			"assignors": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{bson.M{"$ifNull": bson.A{"$assignors", ""}}, ""}},
				literalName,
				bson.M{"$concat": bson.A{"$assignors", ", ", literalName}},
			}},
		}}},
	}
	trace := NewWriteTrace(ctx, "association.assignor.add", ac.Coll.Name(), tenantId, "", "", map[string]any{
		"associationId": associationId,
	})
	result, err := ac.Coll.UpdateOne(ctx, filter, update)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("failed to add assignor: %w", err)
	}
	if result.MatchedCount == 1 {
		trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
		return nil
	}

	var association AssociationDoc
	if err := ac.Coll.FindOne(ctx, bson.M{"id": associationId, "tenantId": tenantId}).Decode(&association); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err := fmt.Errorf("association %q not found in this account", associationId)
			trace.Failure(err, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
			return err
		}
		trace.Failure(err, map[string]any{"matchedCount": result.MatchedCount}, "failed")
		return fmt.Errorf("failed to verify association assignors: %w", err)
	}
	err = fmt.Errorf("assignor %q already exists for association %q", name, associationId)
	trace.Failure(err, map[string]any{"matchedCount": result.MatchedCount}, "no_change")
	return err
}

func (ac *AssociationCollection) Get(id string, tenantId string) (*Association, error) {

	var filter bson.M
	var doc AssociationDoc

	filter = bson.M{
		"id":       id,
		"tenantId": tenantId,
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

func (ac *AssociationCollection) AssignorExists(
	name string,
	tenantId string,
	associationId string,
) (bool, error) {

	name = strings.TrimSpace(name)
	tenantId = strings.TrimSpace(tenantId)
	associationId = strings.TrimSpace(associationId)

	fmt.Printf(
		"AssignorExists: name=%q tenantId=%q associationId=%q\n",
		name,
		tenantId,
		associationId,
	)

	filter := bson.M{
		"id":       associationId,
		"tenantId": tenantId,
	}

	var doc AssociationDoc

	err := ac.Coll.FindOne(
		context.TODO(),
		filter,
	).Decode(&doc)

	if errors.Is(err, mongo.ErrNoDocuments) {
		fmt.Printf(
			"Association not found: id=%q tenantId=%q\n",
			associationId,
			tenantId,
		)

		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf(
			"failed to retrieve association %q: %w",
			associationId,
			err,
		)
	}

	for _, storedAssignor := range strings.Split(
		doc.Assignors,
		",",
	) {
		storedAssignor = strings.TrimSpace(storedAssignor)

		fmt.Printf(
			"Comparing imported assignor %q to stored assignor %q\n",
			name,
			storedAssignor,
		)

		if strings.EqualFold(storedAssignor, name) {
			fmt.Printf("Assignor found\n")
			return true, nil
		}
	}

	fmt.Printf("Assignor not found\n")

	return false, nil
}

func (ac *AssociationCollection) GetAssignorNames(
	tenantId string,
	associations ...string,
) ([]AssignorName, error) {
	assignors := make([]AssignorName, 0)

	filter := bson.M{"tenantId": tenantId}

	if len(associations) > 0 {
		association := strings.TrimSpace(associations[0])
		if association != "" {
			filter["id"] = association
		}
	}

	ctx := context.TODO()

	cursor, err := ac.Coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query assignors: %w", err)
	}
	defer cursor.Close(ctx)

	unique := make(map[string]struct{})

	for cursor.Next(ctx) {
		var doc AssociationDoc

		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("failed to decode assignor document: %w", err)
		}

		for part := range strings.SplitSeq(doc.Assignors, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}

			unique[name] = struct{}{}
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	for name := range unique {
		assignors = append(assignors, AssignorName{Name: name})
	}

	sort.Slice(assignors, func(i, j int) bool {
		return assignors[i].Name < assignors[j].Name
	})

	return assignors, nil
}

func (ac *AssociationCollection) GetAssociationsDirectory(tenantId string) ([]Association, error) {

	var associations []Association

	filter := bson.M{
		"tenantId": tenantId,
	}

	cursor, err := ac.Coll.Find(context.TODO(), filter)
	if err != nil {
		fmt.Println("Error:", err)
		return []Association{}, fmt.Errorf("failed to query associations. Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	for cursor.Next(context.TODO()) {
		var doc AssociationDoc

		if err := cursor.Decode(&doc); err != nil {
			fmt.Println("Error:", err)
			return []Association{}, fmt.Errorf("failed to decode association document. Reason: %v", err)
		}

		associations = append(associations, ac.convDocToAssociation(doc))
	}

	if err := cursor.Err(); err != nil {
		fmt.Println("Error:", err)
		return []Association{}, fmt.Errorf("cursor error while reading associations. Reason: %v", err)
	}

	return associations, nil

}

func (ac *AssociationCollection) Update(association Association, tenantId string) error {
	return ac.UpdateWithContext(context.Background(), association, tenantId)
}

func (ac *AssociationCollection) UpdateWithContext(parentCtx context.Context, association Association, tenantId string) error {
	if association.Version == "" {
		return ErrConflict
	}

	var filter bson.M
	var doc AssociationDoc

	filter = bson.M{
		"id":       association.Id,
		"tenantId": tenantId,
	}

	doc = ac.convAssocToDoc(association)
	doc.TenantId = tenantId

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, ac.Coll, filter, association.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "association.update", ac.Coll.Name(), tenantId, "", "", map[string]any{
		"associationId": association.Id,
	})
	result, writeErr := ac.Coll.ReplaceOne(ctx, filter, doc)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		MetricsErrors(tenantId, "associations", 1)
		return fmt.Errorf("Failed to update association.  Reason: %w", writeErr)
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	MetricsUpdated(tenantId, "associations", result.ModifiedCount)
	fmt.Println("Record updated in", ac.Coll.Name())
	return nil
}

func (ac *AssociationCollection) Delete(id string, tenantId string, versions ...string) error {
	return ac.DeleteWithContext(context.Background(), id, tenantId, versions...)
}

func (ac *AssociationCollection) DeleteWithContext(parentCtx context.Context, id string, tenantId string, versions ...string) error {

	var filter bson.M

	filter = bson.M{
		"id":       id,
		"tenantId": tenantId,
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, ac.Coll, filter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "association.delete", ac.Coll.Name(), tenantId, "", "", map[string]any{
		"associationId": id,
	})
	result, writeErr := ac.Coll.DeleteOne(ctx, filter)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		MetricsErrors(tenantId, "associations", 1)
		return fmt.Errorf("Failed to delete association.  Reason: %w", writeErr)
	}

	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}
	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	MetricsDeleted(tenantId, "associations", result.DeletedCount)
	fmt.Println("Deleted Record with Association Id of", filter["id"], " in", ac.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

func (ac *AssociationCollection) GetAssociationIds(tenantId string) ([]AssociationId, error) {
	var ids []AssociationId = []AssociationId{}

	filter := bson.M{
		"tenantId": tenantId,
	}

	fmt.Println("Retrieving association IDs for tenant:", tenantId)
	cursor, err := ac.Coll.Find(context.TODO(), filter)
	if err != nil {
		return nil, fmt.Errorf("Failed to query associations.  Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	var totalAssociations int64 = 0
	for cursor.Next(context.TODO()) {
		var doc AssociationDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("Failed to decode association document.  Reason: %v", err)
		}

		totalAssociations++
		for part := range strings.SplitSeq(doc.Id, ",") {
			ids = append(ids, AssociationId{Id: strings.TrimSpace(part)})
		}
	}
	fmt.Println("Total associations found for tenant", tenantId, ":", totalAssociations)

	return ids, nil
}

// Site Colleciton, Documents and API Code
type SiteJson struct {
	Version  string `bson:"-" json:"version"`
	Name     string `json:"name"`
	Contact  string `json:"contact"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	TenantId string `json:"-"`
}

type SiteDoc struct {
	Version  string `bson:"-" json:"version"`
	Name     string `bson:"name,omitempty"`
	Contact  string `bson:"contact,omitempty"`
	Phone    string `bson:"phone,omitempty"`
	Email    string `bson:"email,omitempty"`
	TenantId string `bson:"tenantId"`
}

type Site struct {
	Version string `bson:"-" json:"version"`
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
		Version: aj.Version,
		Name:    aj.Name,
		Contact: aj.Contact,
		Phone:   aj.Phone,
		Email:   aj.Email,
	}
}

func (sc *SiteCollection) convSiteToDoc(site Site) SiteDoc {
	return SiteDoc{
		Version: site.Version,
		Name:    site.Name,
		Contact: site.Contact,
		Phone:   site.Phone,
		Email:   site.Email,
	}
}

func (sc *SiteCollection) convDocToSite(doc SiteDoc) Site {
	return Site{
		Version: doc.Version,
		Name:    doc.Name,
		Contact: doc.Contact,
		Phone:   doc.Phone,
		Email:   doc.Email,
	}
}

func (sc *SiteCollection) IsIndexed() (bool, error, int) {

	ctx := context.Background()

	cursor, err := sc.Coll.Indexes().List(ctx)
	if err != nil {
		return false, err, -1
	}

	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		return false, err, -1
	}

	numOfIndices := len(indexes)

	switch numOfIndices {
	case 0:
		return false, fmt.Errorf("Sites collection has 0 indices"), numOfIndices
	case 1:
		return false, fmt.Errorf("Sites collection is not fully indexed.  It contains only the default _id index"), numOfIndices
	case 2:
		return false, fmt.Errorf("Sites collection is not fully indexed.  It is missing one index"), numOfIndices
	case 3:
		return true, nil, numOfIndices
	default:
		return false, fmt.Errorf("Sites collection contains more indices than expected!"), numOfIndices
	}

}

func (sc *SiteCollection) CreateIndices() error {

	index1 := mongo.IndexModel{
		Keys: bson.D{
			bson.E{Key: "tenantId", Value: 1},
			bson.E{Key: "name", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	}

	name, err := sc.Coll.Indexes().CreateOne(context.Background(), index1)
	if err != nil {
		return err
	}

	fmt.Println("Created index:", name)

	return nil
}

func (sc *SiteCollection) Init(client *mongo.Client) error {

	sc.DB = client.Database(Database)
	sc.Coll = sc.DB.Collection("sites")

	return nil
}

func (sc *SiteCollection) Add(site Site, tenantId string) error {
	return sc.AddWithContext(context.Background(), site, tenantId)
}

func (sc *SiteCollection) AddWithContext(parentCtx context.Context, site Site, tenantId string) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	site.Name = strings.TrimSpace(site.Name)
	tenantId = strings.TrimSpace(tenantId)
	if site.Name == "" || tenantId == "" || tenantId == "na" {
		return fmt.Errorf("site name and valid tenant ID are required")
	}
	site.Contact = strings.TrimSpace(site.Contact)
	site.Phone = strings.TrimSpace(site.Phone)
	site.Email = strings.TrimSpace(site.Email)
	if site.Email != "" && !validEmail(site.Email) {
		return fmt.Errorf("site email address is invalid")
	}
	doc := sc.convSiteToDoc(site)
	doc.TenantId = tenantId

	trace := NewWriteTrace(ctx, "site.insert", sc.Coll.Name(), tenantId, "", "", map[string]any{
		"siteName": site.Name,
	})
	result, err := sc.Coll.InsertOne(ctx, doc)
	if err != nil {
		trace.Failure(err, nil, "failed")
		MetricsErrors(tenantId, "sites", 1)
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("site %q already exists", doc.Name)
		}

		return fmt.Errorf("failed to insert site: %w", err)
	}

	trace.Success(nil, result.InsertedID)
	MetricsAdded(tenantId, "sites", 1)

	fmt.Println("Inserted ID:", result.InsertedID)
	return nil
}

func (sc *SiteCollection) Exists(name, tenantId string) (bool, error) {

	var doc SiteDoc

	filter := bson.M{
		"name":     name,
		"tenantId": tenantId,
	}

	err := sc.Coll.FindOne(context.TODO(), filter).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (sc *SiteCollection) Get(name, tenantId string) (*Site, error) {

	var filter bson.M
	var doc SiteDoc

	filter = bson.M{
		"name":     name,
		"tenantId": tenantId,
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

func (sc *SiteCollection) GetSiteNames(
	tenantId string,
) ([]SiteName, error) {

	sites := make([]SiteName, 0)

	filter := bson.M{
		"tenantId": tenantId,
	}

	fmt.Println(
		"Retrieving site names for tenant:",
		tenantId,
	)

	opts := options.Find().SetSort(
		bson.D{
			{Key: "name", Value: 1},
		},
	)

	cursor, err := sc.Coll.Find(
		context.TODO(),
		filter,
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to query sites: %w",
			err,
		)
	}
	defer cursor.Close(context.TODO())

	var totalSites int64

	for cursor.Next(context.TODO()) {
		var doc SiteDoc

		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf(
				"failed to decode site document: %w",
				err,
			)
		}

		totalSites++

		for part := range strings.SplitSeq(doc.Name, ",") {
			siteName := strings.TrimSpace(part)

			if siteName == "" {
				continue
			}

			sites = append(
				sites,
				SiteName{
					Name: siteName,
				},
			)
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"failed while reading site documents: %w",
			err,
		)
	}

	fmt.Println(
		"Total sites found for tenant",
		tenantId,
		":",
		totalSites,
	)

	return sites, nil
}

func (sc *SiteCollection) GetSitesDirectory(tenantId, siteName string) ([]Site, error) {

	// Ensures empty results encode as [] instead of null.
	sites := make([]Site, 0)

	if tenantId == "" {
		return sites, fmt.Errorf("tenant ID is empty")
	}

	filter := bson.M{
		"tenantId": tenantId,
	}

	if siteName != "" {
		filter["name"] = bson.M{
			"$regex":   siteName,
			"$options": "i",
		}
	}

	opts := options.Find().SetSort(
		bson.D{
			{Key: "name", Value: 1},
		},
	)

	ctx := context.TODO()

	cursor, err := sc.Coll.Find(ctx, filter, opts)
	if err != nil {
		return sites, fmt.Errorf(
			"failed to query sites: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var doc SiteDoc

		if err := cursor.Decode(&doc); err != nil {
			return sites, fmt.Errorf(
				"failed to decode site document: %w",
				err,
			)
		}

		sites = append(
			sites,
			sc.convDocToSite(doc),
		)
	}

	if err := cursor.Err(); err != nil {
		return sites, fmt.Errorf(
			"error reading site results: %w",
			err,
		)
	}

	return sites, nil
}

func (sc *SiteCollection) Update(site Site, tenantId string) error {
	return sc.UpdateWithContext(context.Background(), site, tenantId)
}

func (sc *SiteCollection) UpdateWithContext(parentCtx context.Context, site Site, tenantId string) error {
	if site.Version == "" {
		return ErrConflict
	}

	var filter bson.M
	var doc SiteDoc

	filter = bson.M{
		"name":     site.Name,
		"tenantId": tenantId,
	}

	doc = sc.convSiteToDoc(site)
	doc.TenantId = tenantId

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, sc.Coll, filter, site.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "site.update", sc.Coll.Name(), tenantId, "", "", map[string]any{
		"siteName": site.Name,
	})
	result, writeErr := sc.Coll.ReplaceOne(ctx, filter, doc)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		MetricsErrors(tenantId, "sites", 1)
		return fmt.Errorf("Failed to update site.  Reason: %w", writeErr)
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	MetricsUpdated(tenantId, "sites", result.ModifiedCount)

	fmt.Println("Record updated in", sc.Coll.Name())
	return nil
}

func (sc *SiteCollection) Delete(name, tenantId string, versions ...string) error {
	return sc.DeleteWithContext(context.Background(), name, tenantId, versions...)
}

func (sc *SiteCollection) DeleteWithContext(parentCtx context.Context, name, tenantId string, versions ...string) error {

	var filter bson.M

	filter = bson.M{
		"name":     name,
		"tenantId": tenantId,
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, sc.Coll, filter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "site.delete", sc.Coll.Name(), tenantId, "", "", map[string]any{
		"siteName": name,
	})
	result, writeErr := sc.Coll.DeleteOne(ctx, filter)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		MetricsErrors(tenantId, "sites", 1)
		return fmt.Errorf("Failed to delete site.  Reason: %w", writeErr)
	}

	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	MetricsDeleted(tenantId, "sites", result.DeletedCount)

	fmt.Println("Deleted Record with Site Name of", filter["name"], " in", sc.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

type GameCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (gc *GameCollection) Init(client *mongo.Client) error {

	gc.DB = client.Database(Database)
	gc.Coll = gc.DB.Collection("games")

	return nil
}

func (gc *GameCollection) ConvDocToGame(doc model.GameDoc) model.GameDescriptor {
	return convertGameDocWithOfficials(doc)
}

func (gc *GameCollection) Get(association string, gameId string, tenantId string) (model.GameDescriptor, error) {

	var filter bson.M
	var doc model.GameDoc

	fmt.Println("Getting game with game id", gameId, "for association", association, "and tenant id", tenantId)

	gameIdInt64, err := utils.ConvertStrToInt64(gameId)
	if err != nil {
		return model.GameDescriptor{}, fmt.Errorf("Failed to convert game ID.  Reason: %v", err)
	}

	filter = bson.M{
		"gameId":      gameIdInt64,
		"association": association,
		"tenantId":    tenantId,
	}

	err = gc.Coll.FindOne(context.TODO(), filter).Decode(&doc)

	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return model.GameDescriptor{}, fmt.Errorf("Game with game id %s for association %s not found", gameId, association)
		}
		return model.GameDescriptor{}, err
	}

	game := gc.ConvDocToGame(doc)
	return game, nil

}

func (gc *GameCollection) Delete(association, gameId, tid string, versions ...string) error {
	return gc.DeleteWithContext(context.Background(), association, gameId, tid, versions...)
}

func (gc *GameCollection) DeleteWithContext(parentCtx context.Context, association, gameId, tid string, versions ...string) error {

	var filter bson.M

	gameIdInt64, err := utils.ConvertStrToInt64(gameId)
	if err != nil {
		return fmt.Errorf("Failed to convert game ID.  Reason: %v", err)
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter = bson.M{
		"gameId":      gameIdInt64,
		"association": association,
		"tenantId":    tid,
		"status":      bson.M{"$ne": "Paid"},
	}

	conditional, conflictErr := ConditionalFilter(ctx, gc.Coll, filter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "game.delete", gc.Coll.Name(), tid, "", "", map[string]any{
		"gameId": gameId, "association": association,
	})
	result, writeErr := gc.Coll.DeleteOne(ctx, filter)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		return fmt.Errorf("Failed to delete game.  Reason: %w", writeErr)
	}

	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	MetricsDeleted(tid, "games", result.DeletedCount)

	fmt.Println("Deleted Record with Game Id of", filter["gameId"], " in", gc.Coll.Name(), "Records Deleted:", result.DeletedCount)

	return nil
}

func (gc *GameCollection) Exists(association string, gameId string, tenantId string) (bool, error) {

	var filter bson.M

	if tenantId == "na" {
		fmt.Println("Invalid tenantId")
	}

	gId, err := utils.ConvertStrToInt64(gameId)
	if err != nil {
		return false, fmt.Errorf("Failed to convert game ID.  Reason: %v", err)
	}

	filter = bson.M{
		"gameId":      gId,
		"association": association,
		"tenantId":    tenantId,
	}

	err = gc.Coll.FindOne(context.TODO(), filter).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (gc *GameCollection) Add(
	tenantId string,
	game model.GameDescriptor,
) error {
	return gc.AddWithContext(context.TODO(), tenantId, game)
}

func (gc *GameCollection) AddWithContext(parentCtx context.Context, tenantId string, game model.GameDescriptor) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	doc := convertGameDescrWithOfficials(game)
	doc.TenantId = tenantId

	gameDateTime, err := utils.ConvertDateStringToTime(
		game.Date,
		game.Time,
	)
	if err != nil {
		return fmt.Errorf(
			"invalid game date or time: date=%q time=%q: %w",
			game.Date,
			game.Time,
			err,
		)
	}

	doc.GameDateTime = gameDateTime

	trace := NewWriteTrace(ctx, "game.insert", gc.Coll.Name(), tenantId, "", "", map[string]any{
		"gameId": doc.GameId, "association": doc.Association, "sport": doc.Sport,
		"level": doc.Level, "site": doc.Site, "field": doc.Field,
		"gameCount": doc.NumOfGames, "status": doc.Status, "date": game.Date,
	})
	if err := prepareGameOfficials(ctx, gc.Coll.Database(), &doc, nil); err != nil {
		gc.LastError = err
		trace.Failure(err, nil, "failed")
		return err
	}
	result, err := gc.Coll.InsertOne(ctx, doc)
	if err != nil {
		gc.LastError = err
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("insert failed: %w", err)
	}

	trace.Success(nil, result.InsertedID)
	MetricsAdded(tenantId, "games", 1)

	gc.LastError = nil
	fmt.Println("Inserted ID:", result.InsertedID)

	return nil
}

func (gc *GameCollection) Get7DayPendingGames(tId string) ([]model.GameDoc, int, error) {

	ctx := context.Background()

	// Beginning of today (local time)
	now := time.Now()
	start := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		now.Location(),
	)

	// Beginning of 7 days from now
	end := start.Add(7 * 24 * time.Hour)

	filter := bson.M{
		"tenantId": tId,
		"status":   "Pending",
		"gameDateTime": bson.M{
			"$gte": start,
			"$lt":  end,
		},
	}

	cursor, err := gc.Coll.Find(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	defer cursor.Close(ctx)

	var games []model.GameDoc
	var game model.GameDoc
	var totalGames int64 = 0

	for cursor.Next(ctx) {

		game = model.GameDoc{}
		err := cursor.Decode(&game)
		if err != nil {
			return nil, 0, err
		}

		totalGames = totalGames + game.NumOfGames
		game.Officials = gameOfficialsForRead(game)
		games = append(games, game)

	}

	if err := cursor.Err(); err != nil {
		return nil, 0, err
	}
	fmt.Println("database/Get7DayPendingGames returning ", totalGames, "games")
	return games, int(totalGames), nil

}

func (gc *GameCollection) DupGamesWithDateAndTime(tId, date, time string) (bool, error) {
	ctx := context.Background()

	filter := bson.M{
		"tenantId": tId,
		"date":     date,
		"time":     time,
	}

	count, err := gc.Coll.CountDocuments(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (gc *GameCollection) GetTomorrowsPendingGames(tId string) ([]model.GameDoc, int, error) {

	ctx := context.Background()

	// Beginning of today (local time)
	now := time.Now()
	today := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		now.Location(),
	)

	// Beginning of tomorrow
	start := today.Add(24 * time.Hour)

	// Beginning of the day after tomorrow
	end := start.Add(1 * 24 * time.Hour)

	filter := bson.M{
		"tenantId": tId,
		"status":   "Pending",
		"gameDateTime": bson.M{
			"$gte": start,
			"$lt":  end,
		},
	}

	cursor, err := gc.Coll.Find(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	defer cursor.Close(ctx)

	var games []model.GameDoc
	var game model.GameDoc
	var totalGames int64 = 0

	for cursor.Next(ctx) {

		game = model.GameDoc{}
		err := cursor.Decode(&game)
		if err != nil {
			return nil, 0, err
		}

		totalGames = totalGames + game.NumOfGames
		game.Officials = gameOfficialsForRead(game)
		games = append(games, game)

	}

	if err := cursor.Err(); err != nil {
		return nil, 0, err
	}
	fmt.Println("database/GetTomorrowsPendingGames returning ", totalGames, "games")
	return games, int(totalGames), nil

}

func (gc *GameCollection) GetTodaysPendingGames(tId string) ([]model.GameDoc, int, error) {

	ctx := context.Background()

	// Beginning of today (local time)
	now := time.Now()
	start := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		now.Location(),
	)

	// Beginning of tomorrow
	end := start.Add(24 * time.Hour)

	filter := bson.M{
		"tenantId": tId,
		"status":   "Pending",
		"gameDateTime": bson.M{
			"$gte": start,
			"$lt":  end,
		},
	}

	cursor, err := gc.Coll.Find(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	defer cursor.Close(ctx)

	var games []model.GameDoc
	var game model.GameDoc
	var totalGames int64 = 0

	for cursor.Next(ctx) {

		game = model.GameDoc{}
		err := cursor.Decode(&game)
		if err != nil {
			return nil, 0, err
		}

		totalGames = totalGames + game.NumOfGames
		game.Officials = gameOfficialsForRead(game)
		games = append(games, game)

	}

	if err := cursor.Err(); err != nil {
		return nil, 0, err
	}
	fmt.Println("database/GetTodaysPendingGames returning ", totalGames, "games")
	return games, int(totalGames), nil

}

func (gc *GameCollection) GetGameIdsByDateTime(
	tId, date, timeStamp string,
) ([]GameDateTimeMatch, error) {
	ctx := context.Background()

	filter := bson.M{
		"tenantId": tId,
		"date":     date,
		"time":     timeStamp,
	}

	findOptions := options.Find().SetProjection(bson.M{
		"_id":         0,
		"gameId":      1,
		"association": 1,
	})

	cursor, err := gc.Coll.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var matches []GameDateTimeMatch

	if err := cursor.All(ctx, &matches); err != nil {
		return nil, err
	}

	return matches, nil
}

func SetGamesCompleted(
	tenantId, association string,
	gameIDs []int64,
) error {
	if strings.TrimSpace(association) == "" ||
		strings.TrimSpace(tenantId) == "" ||
		tenantId == "na" {
		return fmt.Errorf("association and valid tenant ID are required")
	}

	if len(gameIDs) == 0 {
		return fmt.Errorf("no game IDs were provided")
	}

	uniqueIDs := make([]int64, 0, len(gameIDs))
	seen := make(map[int64]bool)

	for _, id := range gameIDs {
		if id <= 0 {
			return fmt.Errorf("invalid game ID %d", id)
		}
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	coll := Client.Database(Database).Collection("games")
	trace := NewWriteTrace(ctx, "games.mark_completed", coll.Name(), tenantId, "", "", map[string]any{
		"association": association, "gameCount": int64(len(uniqueIDs)),
	})

	session, err := Client.StartSession()
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("failed to start MongoDB session: %w", err)
	}
	defer session.EndSession(ctx)

	var modifiedGames int64
	_, err = session.WithTransaction(
		ctx,
		func(tx mongo.SessionContext) (interface{}, error) {
			modifiedGames = 0
			for _, id := range uniqueIDs {
				filter := bson.M{
					"gameId":      id,
					"association": association,
					"tenantId":    tenantId,
				}

				count, err := coll.CountDocuments(tx, filter)
				if err != nil {
					return nil, fmt.Errorf("check game %d: %w", id, err)
				}
				if count != 1 {
					return nil, fmt.Errorf(
						"expected one game for ID %d and association %q; found %d",
						id, association, count,
					)
				}

				result, err := coll.UpdateOne(
					tx,
					filter,
					bson.M{"$set": bson.M{"status": "Completed"}},
				)
				if err != nil {
					return nil, fmt.Errorf(
						"update game %d to Completed: %w", id, err,
					)
				}
				if result.MatchedCount != 1 {
					return nil, fmt.Errorf("game %d was not found", id)
				}
				modifiedGames += result.ModifiedCount
			}

			return nil, nil
		},
	)
	if err != nil {
		trace.Failure(err, nil, "")
		return fmt.Errorf("game status transaction was rolled back: %w", err)
	}

	for _, id := range uniqueIDs {
		trace.EventForCollection("games", LogLevelInfo, "game.status.completed", "Game status set to Completed", map[string]any{
			"gameId": id, "association": association, "status": "Completed",
		})
	}
	if modifiedGames == 0 {
		trace.Complete(map[string]any{"modifiedCount": modifiedGames}, nil, "no_op")
	} else {
		trace.Success(map[string]any{"modifiedCount": modifiedGames}, nil)
	}
	return nil
}

var officialNameCollation = &options.Collation{
	Locale:   "en",
	Strength: 2,
}

// Official Collection, Documents and API Code
type OfficialJson struct {
	Version   string `bson:"-" json:"version"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Address   string `json:"address"`
	TenantId  string `json:"-"`
}

type OfficialDoc struct {
	Version    string `bson:"-" json:"version"`
	Id         int64  `bson:"id,omitempty"`
	OfficialId int64  `bson:"officialId,omitempty"`
	FirstName  string `bson:"firstName,omitempty"`
	LastName   string `bson:"lastName,omitempty"`
	Phone      string `bson:"phone,omitempty"`
	Email      string `bson:"email,omitempty"`
	Address    string `bson:"address,omitempty"`
	TenantId   string `bson:"tenantId"`
}

type Official struct {
	Version   string `bson:"-" json:"version"`
	Id        int64
	FirstName string
	LastName  string
	Phone     string
	Email     string
	Address   string
}

type OfficialCollection struct {
	DB          *mongo.Database
	Coll        *mongo.Collection
	LastError   error
	Initialized bool
}

func (oc *OfficialCollection) ConvJsonToOfficial(oj OfficialJson) Official {
	return Official{
		Version:   oj.Version,
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

func (oc *OfficialCollection) convOfficialToDoc(
	official Official,
	tenantId string,
	getNewId bool,
) (OfficialDoc, error) {

	var id int64 = 0
	var err error

	if getNewId {
		id, err = oc.getNextId()
	} else {
		id, err = getOfficialsId(official.FirstName, official.LastName, tenantId)
	}

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

	officialId := doc.OfficialId

	if officialId == 0 {
		officialId = doc.Id
	}

	return Official{
		Version:   doc.Version,
		Id:        officialId,
		FirstName: doc.FirstName,
		LastName:  doc.LastName,
		Phone:     doc.Phone,
		Email:     doc.Email,
		Address:   doc.Address,
	}
}

func (oc *OfficialCollection) CreateIndices() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	index := mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenantId", Value: 1},
			{Key: "lastName", Value: 1},
			{Key: "firstName", Value: 1},
		},
		Options: options.Index().
			SetName("tenant_last_first_ci").
			SetUnique(true).
			SetCollation(officialNameCollation),
	}

	name, err := oc.Coll.Indexes().CreateOne(ctx, index)
	if err != nil {
		return fmt.Errorf("failed to create unique officials index: %w", err)
	}

	fmt.Println("Created index:", name)
	return nil
}

func (oc *OfficialCollection) Init(client *mongo.Client) error {

	oc.DB = client.Database(Database)
	oc.Coll = oc.DB.Collection("officials")
	oc.Initialized = true

	return nil
}

func (oc *OfficialCollection) Add(official Official, tenantId string) error {
	return oc.AddWithContext(context.Background(), official, tenantId)
}

func (oc *OfficialCollection) AddWithContext(parentCtx context.Context, official Official, tenantId string) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	official.FirstName = strings.TrimSpace(official.FirstName)
	official.LastName = strings.TrimSpace(official.LastName)
	tenantId = strings.TrimSpace(tenantId)
	if official.FirstName == "" || official.LastName == "" || tenantId == "" || tenantId == "na" {
		return fmt.Errorf("official first name, last name, and valid tenant ID are required")
	}
	official.Phone = strings.TrimSpace(official.Phone)
	official.Email = strings.TrimSpace(official.Email)
	official.Address = strings.TrimSpace(official.Address)
	if official.Email != "" && !validEmail(official.Email) {
		return fmt.Errorf("official email address is invalid")
	}

	doc, err := oc.convOfficialToDoc(official, tenantId, true)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		return fmt.Errorf("failed to convert official to document: %w", err)
	}

	doc.TenantId = tenantId

	trace := NewWriteTrace(ctx, "official.insert", oc.Coll.Name(), tenantId, "", "", map[string]any{
		"officialId": doc.Id,
	})
	result, err := oc.Coll.InsertOne(ctx, doc)
	if err != nil {
		trace.Failure(err, nil, "failed")
		MetricsErrors(tenantId, "officials", 1)
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("official %s %s already exists", official.FirstName, official.LastName)
		}

		return fmt.Errorf("failed to insert official: %w", err)
	}

	trace.Success(nil, result.InsertedID)
	MetricsAdded(tenantId, "officials", 1)

	fmt.Println("Inserted ID:", result.InsertedID)
	return nil
}
func getOfficialsId(fname, lname, tenantId string) (int64, error) {

	var filter bson.M
	var doc OfficialDoc

	db := Client.Database(Database)
	coll := db.Collection("officials")

	filter = bson.M{
		"tenantId":  tenantId,
		"firstName": fname,
		"lastName":  lname,
	}

	err := coll.FindOne(context.TODO(), filter).Decode(&doc)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		if errors.Is(err, mongo.ErrNoDocuments) {
			fmt.Println("Official is not found!")
			return 0, fmt.Errorf(
				"official %s %s not found",
				fname, lname,
			)
		} else {
			fmt.Println("Error retrieving official with name", fname, " ", lname, "Error:", err)
		}

		fmt.Println("Returning with error", err)
		return 0, err
	}

	fmt.Println("Returning official id:", doc.Id)
	return doc.Id, nil
}

func (oc *OfficialCollection) GetById(officialId int64, tenantId string) (Official, error) {

	//
	// Currently the Officials Collection has some documents with id and officialId.
	// So we need to look using both fields.
	//
	// This needs to be fixed by removing id and only use officialId.  However, until then
	// we will use both.
	//

	var filter bson.M
	var doc OfficialDoc

	filter = bson.M{
		"tenantId": tenantId,
		"$or": bson.A{
			bson.M{"officialId": officialId},
			bson.M{"id": officialId},
		},
	}

	fmt.Println("Searching Official with id", officialId)

	err := oc.Coll.FindOne(context.TODO(), filter).Decode(&doc)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		if errors.Is(err, mongo.ErrNoDocuments) {
			fmt.Println("Official is not found!")
			return Official{}, fmt.Errorf(
				"official %d not found",
				officialId,
			)
		} else {
			fmt.Println("Error retrieving official with id", officialId, "Error:", err)
		}

		fmt.Println("Returning with error")
		return Official{}, err
	}

	fmt.Println("Converting Doc to Official Descriptor")
	official := oc.convDocToOfficial(doc)

	fmt.Println("Returning official:", official)
	return official, nil
}

func (oc *OfficialCollection) Get(firstName, lastName, tenantId string) (Official, error) {
	var doc OfficialDoc

	filter := bson.M{
		"firstName": firstName,
		"lastName":  lastName,
		"tenantId":  tenantId,
	}

	opts := options.FindOne().SetCollation(officialNameCollation)

	err := oc.Coll.FindOne(context.TODO(), filter, opts).Decode(&doc)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		fmt.Println("Error:", err)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Official{}, fmt.Errorf("official not found")
		}
		return Official{}, fmt.Errorf("failed to get official: %w", err)
	}

	return oc.convDocToOfficial(doc), nil
}

func (oc *OfficialCollection) GetOfficialsDirectory(firstName, lastName, tenantId string) ([]Official, error) {

	var officials []Official

	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)

	filter := bson.M{
		"tenantId": tenantId,
	}

	if firstName != "" {
		filter["firstName"] = firstName
	}

	if lastName != "" {
		filter["lastName"] = lastName
	}

	opts := options.Find().
		SetSort(bson.D{
			{Key: "lastName", Value: 1},
			{Key: "firstName", Value: 1},
		}).
		SetCollation(officialNameCollation)

	cursor, err := oc.Coll.Find(context.TODO(), filter, opts)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		fmt.Println("Error:", err)
		return []Official{}, fmt.Errorf("failed to query officials. Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	for cursor.Next(context.TODO()) {
		var doc OfficialDoc

		if err := cursor.Decode(&doc); err != nil {
			fmt.Println("Error:", err)
			MetricsErrors(tenantId, "officials", 1)
			return []Official{}, fmt.Errorf("failed to decode official document. Reason: %v", err)
		}

		officials = append(officials, oc.convDocToOfficial(doc))
	}

	if err := cursor.Err(); err != nil {
		fmt.Println("Error:", err)
		MetricsErrors(tenantId, "officials", 1)
		return []Official{}, fmt.Errorf("cursor error while reading officials. Reason: %v", err)
	}

	return officials, nil
}

func (oc *OfficialCollection) Update(tenantId string, official Official) error {
	return oc.UpdateWithContext(context.Background(), tenantId, official)
}

func (oc *OfficialCollection) UpdateWithContext(parentCtx context.Context, tenantId string, official Official) error {
	if official.Version == "" {
		return ErrConflict
	}

	var filter bson.M
	var doc OfficialDoc

	filter = bson.M{
		"firstName": official.FirstName,
		"lastName":  official.LastName,
		"tenantId":  tenantId,
	}

	doc, err := oc.convOfficialToDoc(official, tenantId, false)

	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		return fmt.Errorf("Failed to convert official to document.  Reason: %v", err)
	}

	doc.TenantId = tenantId

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, oc.Coll, filter, official.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "official.update", oc.Coll.Name(), tenantId, "", "", nil)
	result, writeErr := oc.Coll.ReplaceOne(ctx, filter, doc)
	if writeErr != nil {
		trace.Failure(writeErr, nil, "failed")
		MetricsErrors(tenantId, "officials", 1)
		return fmt.Errorf("Failed to update official.  Reason: %w", writeErr)
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	MetricsUpdated(tenantId, "officials", result.ModifiedCount)

	fmt.Println("Record updated in", oc.Coll.Name())
	return nil
}

func (oc *OfficialCollection) Delete(firstName, lastName, tenantId string, versions ...string) error {
	return oc.DeleteWithContext(context.Background(), firstName, lastName, tenantId, versions...)
}

func (oc *OfficialCollection) DeleteWithContext(parentCtx context.Context, firstName, lastName, tenantId string, versions ...string) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	filter := bson.M{
		"firstName": firstName,
		"lastName":  lastName,
		"tenantId":  tenantId,
	}

	var official OfficialDoc
	err := oc.Coll.FindOne(ctx, filter).Decode(&official)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrConflict
		}
		return fmt.Errorf("failed to retrieve official: %w", err)
	}

	// officialId is stored as a string in the games officials array.
	officialID := official.OfficialId
	if officialID == 0 {
		officialID = official.Id
	}
	var assignedID interface{} = strconv.FormatInt(officialID, 10)
	if official.Id != 0 && official.OfficialId != 0 && official.Id != official.OfficialId {
		assignedID = bson.M{"$in": bson.A{strconv.FormatInt(official.Id, 10), strconv.FormatInt(official.OfficialId, 10)}}
	}
	gameFilter := bson.M{
		"tenantId":             tenantId,
		"officials.officialId": assignedID,
	}

	gamesCollection := oc.Coll.Database().Collection("games")
	err = gamesCollection.FindOne(ctx, gameFilter).Err()
	if err == nil {

		return fmt.Errorf(
			"cannot delete %s %s: official is assigned to a game",
			firstName, lastName,
		)
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		MetricsErrors(tenantId, "officials", 1)
		return fmt.Errorf("failed to check game assignments: %w", err)
	}

	// Delete the exact official that was checked.
	deleteFilter := bson.M{
		"Id":       official.Id,
		"tenantId": tenantId,
	}
	// Retain the original name filter if the ID's BSON field differs.
	deleteFilter = filter

	conditional, conflictErr := ConditionalFilter(ctx, oc.Coll, deleteFilter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	deleteFilter = conditional

	trace := NewWriteTrace(ctx, "official.delete", oc.Coll.Name(), tenantId, "", "", nil)
	result, err := oc.Coll.DeleteOne(ctx, deleteFilter)
	if err != nil {
		trace.Failure(err, nil, "failed")
		MetricsErrors(tenantId, "officials", 1)
		return fmt.Errorf("failed to delete official: %w", err)
	}
	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	MetricsDeleted(tenantId, "officials", result.DeletedCount)
	fmt.Println("Deleted official:", firstName, lastName)
	return nil
}

func (oc *OfficialCollection) GetOfficialsNames(tenantId string) ([]OfficialName, error) {

	result := []OfficialName{}

	filter := bson.M{
		"tenantId": tenantId,
	}

	fmt.Println("Retrieving officials names for tenant:", tenantId)
	opts := options.Find().
		SetSort(bson.D{
			{Key: "lastName", Value: 1},
			{Key: "firstName", Value: 1},
		})

	cursor, err := oc.Coll.Find(context.TODO(), filter, opts)
	if err != nil {
		MetricsErrors(tenantId, "officials", 1)
		fmt.Println("Error:", err)
		return []OfficialName{}, fmt.Errorf("failed to query officials. Reason: %v", err)
	}
	defer cursor.Close(context.TODO())

	//
	// Add Unassigned to Officials collection and then remove this
	//
	result = append(result, OfficialName{
		Name: "Unassigned",
	})

	var doc OfficialDoc
	var totalOfficials int64 = 0

	for cursor.Next(context.TODO()) {

		if err := cursor.Decode(&doc); err != nil {
			fmt.Println("Error:", err)
			MetricsErrors(tenantId, "officials", 1)
			return []OfficialName{}, fmt.Errorf("failed to decode official document. Reason: %v", err)
		}

		result = append(result, OfficialName{Name: doc.FirstName + " " + doc.LastName})
		totalOfficials++
	}

	if err := cursor.Err(); err != nil {
		fmt.Println("Error:", err)
		MetricsErrors(tenantId, "officials", 1)
		return []OfficialName{}, fmt.Errorf("cursor error while reading officials. Reason: %v", err)
	}

	fmt.Println("Total officials found for tenant", tenantId, ":", totalOfficials)
	return result, nil
}

func (oc *OfficialCollection) Exists(name, tenantId string) (bool, error) {

	var filter bson.M
	var names []string

	if tenantId == "na" {
		fmt.Println("Invalid tenantId")
	}

	fmt.Println("Checking for", name, "in officials collection")
	names = strings.Split(name, " ")

	if len(names) < 2 || names[0] == "" || names[1] == "" {
		return false, fmt.Errorf("Invalid name[%s].  Missing required parameter: first and last name are both required.", name)
	}

	filter = bson.M{
		"firstName": names[0],
		"lastName":  names[1],
		"tenantId":  tenantId,
	}

	result := oc.Coll.FindOne(context.TODO(), filter)

	if result.Err() == mongo.ErrNoDocuments {
		return false, nil
	}

	return true, nil
}

// Expense Colleciton, Documents and API Code
type Expense struct {
	Version     string `bson:"-" json:"version"`
	Date        string
	Type        string
	Amount      string
	Mileage     string
	Association string
	GameId      string
	Description string
	TaxYear     string
}

type ExpenseJson struct {
	Version     string `bson:"-" json:"version"`
	Date        string `json:"date"`
	ExpenseType string `json:"expenseType"`
	Amount      int64  `json:"amount"`
	Mileage     int64  `json:"mileage"`
	Description string `json:"description"`
	Association string `json:"association"`
	GameId      int64  `json:"gameId"`
	TaxYear     string `json:"taxYear"`
}

type ExpenseDoc struct {
	Version     string `bson:"-" json:"version"`
	ExpenseId   string `bson:"expenseId,omitempty"`
	Date        string `bson:"date,omitempty"`
	Type        string `bson:"type,omitempty"`
	Amount      int64  `bson:"amount,omitempty"`
	Mileage     int64  `bson:"mileage,omitempty"`
	Association string `bson:"association,omitempty"`
	GameId      int64  `bson:"gameId,omitempty"`
	Description string `bson:"description,omitempty"`
	TaxYear     string `bson:"taxYear,omitempty"`
	TenantId    string `bson:"tenantId"`
	Source      string `bson:"source,omitempty"`
	PaymentRef  string `bson:"paymentRef,omitempty"`
}

type ExpensesCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

//
// Example Calls
//

/*
MetricsAdded(tenantId, "games", 1)

MetricsUpdated(tenantId, "games", 3)

MetricsDeleted(tenantId, "sites", 2)

MetricsErrors(tenantId, "officials", 1)
*/

func (ec *ExpensesCollection) Init(client *mongo.Client) error {

	ec.DB = client.Database(Database)
	ec.Coll = ec.DB.Collection("expenses")

	return nil
}

func (ec *ExpensesCollection) convExpenseToDoc(expense Expense) ExpenseDoc {
	var mileage int64 = 0
	var amt int64 = 0

	if expense.Type == "Mileage" {
		mileage, _ = utils.ConvertStrMilesToInt64(expense.Amount)
		amt = 0
	} else {
		amt, _ = utils.ConvertAmtStrToInt64(expense.Amount)
	}

	gameId, _ := utils.ConvertStrToInt64(expense.GameId)
	return ExpenseDoc{
		Version:     expense.Version,
		Date:        expense.Date,
		Type:        expense.Type,
		Amount:      amt,
		Mileage:     mileage,
		Association: expense.Association,
		GameId:      gameId,
		Description: expense.Description,
		TaxYear:     expense.TaxYear,
	}
}

func (ec *ExpensesCollection) ConvJsonToExpense(expenseJson ExpenseJson) Expense {

	fmt.Println("Converting JSON to Expense", expenseJson)
	expenseAmt := utils.ConvertInt64ToAmtStr(expenseJson.Amount)
	mileage := utils.ConvertInt64ToStr(expenseJson.Mileage / 100)
	gameId := utils.ConvertInt64ToStr(expenseJson.GameId)
	return Expense{
		Version:     expenseJson.Version,
		Date:        expenseJson.Date,
		Type:        expenseJson.ExpenseType,
		Amount:      expenseAmt,
		Mileage:     mileage,
		Association: expenseJson.Association,
		GameId:      gameId,
		Description: expenseJson.Description,
		TaxYear:     expenseJson.TaxYear,
	}
}

func (ec *ExpensesCollection) GenerateExpenseId(expense Expense) string {

	var month int
	var day int
	var year int
	var expTypeId string
	var dollar string
	var cents string
	var expenseId string = ""

	expTypeParts := strings.Split(expense.Type, " ")
	if len(expTypeParts) > 1 {
		expTypeId = expTypeParts[0][0:1] + expTypeParts[1][0:1]
	} else if len(expTypeParts) == 1 {
		expTypeId = expTypeParts[0][0:2]
	}

	dollarCents := strings.Split(expense.Amount, ".")

	if len(dollarCents) == 2 {
		dollar = dollarCents[0]
		cents = dollarCents[1]
	} else {
		dollar = expense.Amount
		cents = "00"
	}

	n, err := fmt.Sscanf(expense.Date, "%d/%d/%d", &month, &day, &year)
	if err != nil || n != 3 {
		fmt.Println("Error parsing date for expense", "Reason:", err)
		return fmt.Sprintf("EXP%s", utils.GenerateRandomString(10))
	}
	if len(expense.Association) != 0 {
		expTypeId = expTypeId + "-" + expense.Association
	}
	expenseId = fmt.Sprintf("%s-%d%d%d-%s%s", expTypeId, month, day, year, dollar, cents)
	return expenseId

}

func (ec *ExpensesCollection) Delete(expenseId, association, tenantId string, versions ...string) error {
	return ec.DeleteWithContext(context.Background(), expenseId, association, tenantId, versions...)
}

func (ec *ExpensesCollection) DeleteWithContext(parentCtx context.Context, expenseId, association, tenantId string, versions ...string) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	fmt.Println("===== Attempting to delete Expense Id", expenseId, "for Association", association, "and tenant id", tenantId, "=====")

	expenseId = strings.TrimSpace(expenseId)
	association = strings.TrimSpace(association)
	tenantId = strings.TrimSpace(tenantId)

	if expenseId == "" {
		return fmt.Errorf("expense ID is required")
	}

	if association == "" {
		return fmt.Errorf("association is required")
	}

	if tenantId == "" {
		return fmt.Errorf("tenant ID is required")
	}

	filter := bson.M{
		"expenseId":   expenseId,
		"association": association,
		"tenantId":    tenantId,
	}
	var existing ExpenseDoc
	if err := ec.Coll.FindOne(ctx, filter).Decode(&existing); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrConflict
		}
		return fmt.Errorf("failed to load expense %q: %w", expenseId, err)
	}
	if existing.Source == "Payroll Deduction" || existing.PaymentRef != "" {
		return fmt.Errorf("payroll deduction expenses must be changed through their payment")
	}

	conditional, conflictErr := ConditionalFilter(ctx, ec.Coll, filter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "expense.delete", ec.Coll.Name(), tenantId, "", "", map[string]any{
		"expenseId": expenseId, "association": association, "date": existing.Date,
		"taxYear": existing.TaxYear, "gameId": existing.GameId,
	})
	result, err := ec.Coll.DeleteOne(
		ctx,
		filter,
	)

	if err != nil {
		trace.Failure(err, nil, "failed")
		MetricsErrors(tenantId, "expenses", 1)
		return fmt.Errorf(
			"failed to delete expense %q for association %q: %w",
			expenseId,
			association,
			err,
		)
	}

	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}
	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	MetricsDeleted(tenantId, "expenses", result.DeletedCount)
	return nil
}

func (ec *ExpensesCollection) Update(
	expense Expense,
	expenseId string,
	association string,
	tenantId string,
) error {
	return ec.UpdateWithContext(context.Background(), expense, expenseId, association, tenantId)
}

func (ec *ExpensesCollection) UpdateWithContext(parentCtx context.Context, expense Expense, expenseId string, association string, tenantId string) error {
	if expense.Version == "" {
		return ErrConflict
	}

	ctx, cancel := context.WithTimeout(
		parentCtx,
		10*time.Second,
	)
	defer cancel()

	expenseId = strings.TrimSpace(expenseId)
	association = strings.TrimSpace(association)
	tenantId = strings.TrimSpace(tenantId)

	if expenseId == "" {
		return fmt.Errorf("expense ID is required")
	}

	if association == "" {
		return fmt.Errorf("association is required")
	}

	if tenantId == "" {
		return fmt.Errorf("tenant ID is required")
	}

	dt, err := time.Parse("2006-01-02", expense.Date)
	if err != nil {
		return fmt.Errorf(
			"invalid expense date %q: %w",
			expense.Date,
			err,
		)
	}

	formattedDate := dt.Format("1/2/2006")

	filter := bson.M{
		"expenseId":   expenseId,
		"association": association,
		"tenantId":    tenantId,
	}
	var existing ExpenseDoc
	if err := ec.Coll.FindOne(ctx, filter).Decode(&existing); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrConflict
		}
		return fmt.Errorf("failed to load expense %q: %w", expenseId, err)
	}
	if existing.Source == "Payroll Deduction" || existing.PaymentRef != "" {
		return fmt.Errorf("payroll deduction expenses must be changed through their payment")
	}

	var amt int64 = 0
	var mileage int64 = 0

	if expense.Type == "Mileage" {
		fmt.Println("Update Expense converting mileage:", expense.Amount)
		mileage, err = utils.ConvertStrMilesToInt64(expense.Amount)
		if err != nil {
			fmt.Println("Error converting Mileage:", err)
		}
		amt = 0
		fmt.Println("Update Expense converted mileage to :", mileage)
	} else {
		fmt.Println("Update Expense converting amount:", expense.Amount)
		amt, err = utils.ConvertAmtStrToInt64(expense.Amount)

		if err != nil {
			fmt.Println("Error converting Amount:", err)
		}
		fmt.Println("Update Expense converted amount to :", amt)
	}

	update := bson.M{
		"$set": bson.M{
			"date":        formattedDate,
			"description": strings.TrimSpace(expense.Description),
			"amount":      amt,
			"mileage":     mileage,
			"type":        strings.TrimSpace(expense.Type),
			"taxYear":     strings.TrimSpace(expense.TaxYear),
		},
	}

	conditional, conflictErr := ConditionalFilter(ctx, ec.Coll, filter, expense.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "expense.update", ec.Coll.Name(), tenantId, "", "", map[string]any{
		"expenseId": expenseId, "association": association, "gameId": existing.GameId,
		"date": formattedDate, "taxYear": expense.TaxYear,
	})
	result, err := ec.Coll.UpdateOne(
		ctx,
		filter,
		update,
	)
	if err != nil {
		trace.Failure(err, nil, "failed")
		MetricsErrors(tenantId, "expenses", 1)

		return fmt.Errorf(
			"failed to update expense %q for association %q: %w",
			expenseId,
			association,
			err,
		)
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	MetricsUpdated(tenantId, "expenses", result.ModifiedCount)

	fmt.Printf(
		"Expense %s updated. Matched: %d, Modified: %d\n",
		expenseId,
		result.MatchedCount,
		result.ModifiedCount,
	)

	return nil
}

func (ec *ExpensesCollection) Add(expense Expense, tenantId string) error {
	return ec.AddWithTrace(context.TODO(), expense, tenantId, nil)
}

func (ec *ExpensesCollection) AddWithContext(parentCtx context.Context, expense Expense, tenantId string) error {
	return ec.AddWithTrace(parentCtx, expense, tenantId, nil)
}

func (ec *ExpensesCollection) AddWithTrace(parentCtx context.Context, expense Expense, tenantId string, parentTrace *WriteTrace) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	dt, err := time.Parse("2006-01-02", expense.Date)
	if err != nil {
		return err
	}
	formattedDate := dt.Format("1/2/2006")

	expense.Date = formattedDate

	doc := ec.convExpenseToDoc(expense)

	doc.TenantId = tenantId
	doc.ExpenseId = ec.GenerateExpenseId(expense)

	trace := parentTrace
	ownsTrace := trace == nil
	if ownsTrace {
		trace = NewWriteTrace(ctx, "expense.insert", ec.Coll.Name(), tenantId, "", "", map[string]any{
			"expenseId": doc.ExpenseId, "association": doc.Association, "gameId": doc.GameId,
			"date": doc.Date, "taxYear": doc.TaxYear,
		})
	}
	var result *mongo.InsertOneResult
	result, ec.LastError = ec.Coll.InsertOne(ctx, doc)
	if ec.LastError != nil {
		if ownsTrace {
			trace.Failure(ec.LastError, nil, "failed")
		}
		MetricsErrors(tenantId, "expenses", 1)
		return fmt.Errorf("Insert failed.  Reason: %w", ec.LastError)
	}
	if ownsTrace {
		trace.Success(nil, result.InsertedID)
	} else {
		trace.EventWithMongoID(LogLevelInfo, "expense.import.item_inserted", "Expense import item inserted", map[string]any{
			"expenseId": doc.ExpenseId, "association": doc.Association, "gameId": doc.GameId,
			"date": doc.Date, "taxYear": doc.TaxYear,
		}, result.InsertedID)
	}
	fmt.Println("Inserted ID:", result.InsertedID)

	MetricsAdded(tenantId, "expenses", 1)
	return nil
}

type SessionDoc struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	SessionID string             `bson:"sessionId" json:"sessionId"`
	Username  string             `bson:"username" json:"username"`
	TenantID  string             `bson:"tenantId" json:"tenantId"`
	Role      string             `bson:"role" json:"role"`
	ExpiresAt time.Time          `bson:"expiresAt" json:"expiresAt"`
}

type SessionsCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (sc *SessionsCollection) Init(client *mongo.Client) error {

	sc.DB = client.Database(Database)
	sc.Coll = sc.DB.Collection("sessions")

	return nil
}

func (sc *SessionsCollection) GetTenantID(sessionID string) (string, error) {

	filter := bson.M{
		"sessionId": sessionID,
	}

	var session SessionDoc

	err := sc.Coll.FindOne(context.TODO(), filter).Decode(&session)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", fmt.Errorf("session not found")
		}
		return "", fmt.Errorf("failed to retrieve session: %v", err)
	}

	return session.TenantID, nil
}

type UsersCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (uc *UsersCollection) Init(client *mongo.Client) error {

	uc.DB = client.Database(Database)
	uc.Coll = uc.DB.Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := uc.Coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

func (uc *UsersCollection) SetSportsSupported(
	tenantId string,
	sports []string,
) error {
	return uc.SetSportsSupportedWithContext(context.Background(), tenantId, sports)
}

func (uc *UsersCollection) SetSportsSupportedWithContext(
	parentCtx context.Context,
	tenantId string,
	sports []string,
) error {
	if tenantId == "" {
		return fmt.Errorf("tenant ID is empty")
	}

	if sports == nil {
		sports = []string{}
	}

	filter := bson.M{
		"tenantId": tenantId,
	}

	update := bson.M{
		"$set": bson.M{
			"sports": sports,
		},
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	trace := NewWriteTrace(ctx, "user.sports.update", uc.Coll.Name(), tenantId, "", "", nil)
	result, err := uc.Coll.UpdateOne(
		ctx,
		filter,
		update,
	)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("failed to save supported sports: %w", err)
	}

	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return fmt.Errorf("user not found for tenant ID %q", tenantId)
	}

	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	return nil
}

func (uc *UsersCollection) EnableSportSupported(tenantId, sport string) error {
	return uc.EnableSportSupportedWithContext(context.Background(), tenantId, sport)
}

func (uc *UsersCollection) EnableSportSupportedWithContext(parentCtx context.Context, tenantId, sport string) error {
	tenantId = strings.TrimSpace(tenantId)
	sport = strings.TrimSpace(sport)
	if tenantId == "" || tenantId == "na" || sport == "" {
		return fmt.Errorf("sport and valid tenant ID are required")
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	var catalogSport Sport
	if err := uc.DB.Collection("sports").FindOne(ctx, bson.M{
		"name": sport, "active": true,
	}).Decode(&catalogSport); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return fmt.Errorf("sport %q is not active in the shared catalog", sport)
		}
		return fmt.Errorf("failed to validate sport: %w", err)
	}

	trace := NewWriteTrace(ctx, "user.sports.enable", uc.Coll.Name(), tenantId, "", "", map[string]any{
		"sport": sport,
	})
	result, err := uc.Coll.UpdateOne(ctx, bson.M{"tenantId": tenantId},
		bson.M{"$addToSet": bson.M{"sports": sport}})
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("failed to enable sport for this account: %w", err)
	}
	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return fmt.Errorf("account not found for tenant ID %q", tenantId)
	}
	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	return nil
}

func (uc *UsersCollection) IsSportSupported(
	tenantId string,
	sport string,
) (bool, error) {
	sports, err := uc.GetSportsSupported(tenantId)
	if err != nil {
		return false, err
	}

	for _, s := range sports {
		if s == sport {
			return true, nil
		}
	}

	return false, nil
}

func (uc *UsersCollection) GetSportsSupported(
	tenantId string,
) ([]string, error) {
	if tenantId == "" {
		return nil, fmt.Errorf("tenant ID is empty")
	}

	// No need to check sports here for Get operation

	filter := bson.M{
		"tenantId": tenantId,
	}

	var user struct {
		Sports []string `bson:"sports"`
	}

	err := uc.Coll.FindOne(context.TODO(), filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("user not found for tenant ID %q", tenantId)
		}
		return nil, fmt.Errorf("failed to retrieve supported sports: %w", err)
	}

	if user.Sports == nil {
		user.Sports = []string{}
	}

	return user.Sports, nil

}

func (uc *UsersCollection) GetName(tenantId, username string) (string, error) {

	filter := bson.M{
		"tenantId": tenantId,
		"username": username,
	}

	var user model.User

	// Query to find all documents
	err := uc.Coll.FindOne(context.TODO(), filter).Decode(&user)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve user: %v", err)
	}

	return user.Name, nil
}

var (
	ErrUserAlreadyExists = errors.New("failed to create user account") // Don't tell the user that the username already exists, just return a generic error message
	ErrInvalidEmail      = errors.New("username must be a valid email address")
)

func (uc *UsersCollection) Add(
	ctx context.Context,
	req model.UserRequest,
) (string, error) {

	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.Name = strings.TrimSpace(req.Name)

	if req.Username == "" {
		return "", fmt.Errorf("username is required")
	}

	if len(req.Password) < 8 || len(req.Password) > 72 {
		return "", fmt.Errorf("password must be 8 to 72 bytes")
	}

	if !validEmail(req.Username) {
		return "", ErrInvalidEmail
	}

	err := uc.Coll.FindOne(
		ctx,
		bson.M{"username": req.Username},
	).Err()

	switch {
	case err == nil:
		return "", ErrUserAlreadyExists

	case !errors.Is(err, mongo.ErrNoDocuments):
		return "", fmt.Errorf(
			"failed to check whether user exists: %w",
			err,
		)
	}

	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	tenantID := primitive.NewObjectID().Hex()

	verified := false
	user := model.User{
		EmailVerified: &verified,
		Username:      req.Username,
		PasswordHash:  string(passwordHash),
		TenantID:      tenantID,
		Role:          "user",
		CreatedAt:     time.Now(),
		Name:          req.Name,
	}

	trace := NewWriteTrace(ctx, "user.account.insert", uc.Coll.Name(), tenantID, "", "", nil)
	result, err := uc.Coll.InsertOne(ctx, user)
	if err != nil {
		trace.Failure(err, nil, "failed")
		if mongo.IsDuplicateKeyError(err) {
			return "", ErrUserAlreadyExists
		}
		return "", fmt.Errorf("failed to save user account: %w", err)
	}

	trace.Success(nil, result.InsertedID)
	return tenantID, nil
}

func validEmail(username string) bool {
	address, err := mail.ParseAddress(username)
	if err != nil {
		return false
	}

	// Prevent values such as "Randall <randall@example.com>".
	return address.Address == username
}

func (uc *UsersCollection) GetNumOfUserAccounts() (int64, error) {

	filter := bson.M{}

	count, err := uc.Coll.CountDocuments(context.TODO(), filter)
	if err != nil {
		return 0, fmt.Errorf("failed to count user accounts: %w", err)
	}

	return count, nil
}

type Level struct {
	Version  string `bson:"-" json:"version"`
	Sport    string `bson:"sport" json:"sport"`
	GameFee  int64  `bson:"gameFee" json:"-"`
	ID       string `bson:"id" json:"id"`
	Name     string `bson:"name" json:"name"`
	Active   bool   `bson:"active" json:"active"`
	TenantID string `bson:"tenantId" json:"tenantId"`
}

// BSON stores cents; the HTTP API exchanges dollar strings.
func (level Level) MarshalJSON() ([]byte, error) {
	type levelAlias Level
	return json.Marshal(struct {
		levelAlias
		GameFee string `json:"gameFee"`
	}{
		levelAlias: levelAlias(level),
		GameFee:    utils.ConvertInt64ToAmtStr(level.GameFee),
	})
}

func (level *Level) UnmarshalJSON(data []byte) error {
	type levelAlias Level
	var wire struct {
		levelAlias
		GameFee string `json:"gameFee"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	fee, err := normalizeLevelGameFee(wire.GameFee)
	if err != nil {
		return err
	}
	// The normalized string has exactly two fractional digits.
	cents, err := strconv.ParseInt(strings.ReplaceAll(fee, ".", ""), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid game fee: %w", err)
	}
	*level = Level(wire.levelAlias)
	level.GameFee = cents
	return nil
}

type LevelsCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (lc *LevelsCollection) Init(client *mongo.Client) error {

	lc.DB = client.Database(Database)
	lc.Coll = lc.DB.Collection("levels")

	return nil
}

func (lc *LevelsCollection) CreateIndices() error {
	_, err := lc.Coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenantId", Value: 1},
			{Key: "sport", Value: 1},
			{Key: "name", Value: 1},
		},
		Options: options.Index().SetName("unique_tenant_sport_level").SetUnique(true),
	})
	return err
}

func (lc *LevelsCollection) Add(level Level, tenantId string) error {
	return lc.AddWithContext(context.Background(), level, tenantId)
}

func (lc *LevelsCollection) AddWithContext(parentCtx context.Context, level Level, tenantId string) error {
	level.Name = strings.TrimSpace(level.Name)
	level.Sport = strings.TrimSpace(level.Sport)
	level.TenantID = strings.TrimSpace(tenantId)
	if level.Name == "" || level.Sport == "" || level.TenantID == "" || level.TenantID == "na" {
		return fmt.Errorf("level name, sport, and valid tenant ID are required")
	}
	if len([]rune(level.Name)) > 50 {
		return fmt.Errorf("level name must contain 1 to 50 characters")
	}
	if level.GameFee < 0 {
		return fmt.Errorf("game fee cannot be negative")
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	if err := lc.validateLevelSport(ctx, level.Sport); err != nil {
		return err
	}
	err := lc.Coll.FindOne(ctx, bson.M{
		"tenantId": level.TenantID, "sport": level.Sport, "name": level.Name,
	}).Err()
	if err == nil {
		return fmt.Errorf("level %q already exists for %s", level.Name, level.Sport)
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("failed to check level: %w", err)
	}
	// Independent IDs allow the same level name in different sports.
	level.ID = primitive.NewObjectID().Hex()
	level.Active = false
	trace := NewWriteTrace(ctx, "level.insert", lc.Coll.Name(), tenantId, "", "", map[string]any{
		"levelId": level.ID, "sport": level.Sport, "level": level.Name,
	})
	result, err := lc.Coll.InsertOne(ctx, level)
	if err != nil {
		trace.Failure(err, nil, "failed")
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("level %q already exists for %s", level.Name, level.Sport)
		}
		return fmt.Errorf("failed to add level: %w", err)
	}
	trace.Success(nil, result.InsertedID)
	return nil
}

func (lc *LevelsCollection) validateLevelSport(ctx context.Context, sport string) error {
	err := lc.DB.Collection("sports").FindOne(ctx, bson.M{"name": sport, "active": true}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("sport %q is not active in the shared catalog", sport)
	}
	if err != nil {
		return fmt.Errorf("failed to validate sport: %w", err)
	}
	return nil
}

func normalizeLevelGameFee(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "0.00", nil
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", fmt.Errorf("game fee must be a non-negative amount with at most two decimal places")
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return "", fmt.Errorf("invalid game fee")
			}
		}
	}
	fraction := "00"
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return "", fmt.Errorf("game fee must have at most two decimal places")
		}
		fraction = (parts[1] + "00")[:2]
	}
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	if len(whole) > 12 {
		return "", fmt.Errorf("game fee is too large")
	}
	return whole + "." + fraction, nil
}

// Update assigns the sport and fee for an existing level owned by this tenant.
func (lc *LevelsCollection) Update(level Level, tenantId string) error {
	return lc.UpdateWithContext(context.Background(), level, tenantId)
}

func (lc *LevelsCollection) UpdateWithContext(parentCtx context.Context, level Level, tenantId string) error {
	if level.Version == "" {
		return ErrConflict
	}
	level.ID = strings.TrimSpace(level.ID)
	level.Name = strings.TrimSpace(level.Name)
	level.Sport = strings.TrimSpace(level.Sport)
	tenantId = strings.TrimSpace(tenantId)
	if level.ID == "" || tenantId == "" || tenantId == "na" || level.Sport == "" {
		return fmt.Errorf("level ID, sport, and valid tenant ID are required")
	}
	if level.Name == "" || len([]rune(level.Name)) > 50 {
		return fmt.Errorf("level name must contain 1 to 50 characters")
	}
	if level.GameFee < 0 {
		return fmt.Errorf("game fee cannot be negative")
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	filter := bson.M{"id": level.ID, "tenantId": tenantId}
	var existing Level
	if err := lc.Coll.FindOne(ctx, filter).Decode(&existing); err != nil {
		return err
	}
	if err := lc.validateLevelSport(ctx, level.Sport); err != nil {
		return err
	}
	// Preserve the stable ID while editing the configured name, sport, or fee.
	err := lc.Coll.FindOne(ctx, bson.M{
		"tenantId": tenantId, "sport": level.Sport, "name": level.Name,
		"id": bson.M{"$ne": existing.ID},
	}).Err()
	if err == nil {
		return fmt.Errorf("level %q already exists for %s", level.Name, level.Sport)
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("failed to check level: %w", err)
	}
	conditional, conflictErr := ConditionalFilter(ctx, lc.Coll, filter, level.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "level.update", lc.Coll.Name(), tenantId, "", "", map[string]any{
		"levelId": level.ID, "sport": level.Sport, "level": level.Name,
	})
	result, err := lc.Coll.UpdateOne(ctx, filter,
		bson.M{"$set": bson.M{"name": level.Name, "sport": level.Sport, "gameFee": level.GameFee}})
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("failed to update level: %w", err)
	}
	if result.MatchedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return ErrConflict
	}
	trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
	return nil
}

func (lc *LevelsCollection) Activate(level Level, tenantId string) error {
	return lc.ActivateWithContext(context.Background(), level, tenantId)
}

func (lc *LevelsCollection) ActivateWithContext(parentCtx context.Context, level Level, tenantId string) error {
	level.ID = strings.TrimSpace(level.ID)
	level.Name = strings.TrimSpace(level.Name)

	fmt.Println("Level:", level)
	filter := bson.M{
		"id":       level.ID,
		"tenantId": tenantId,
	}

	update := bson.M{
		"$set": bson.M{
			"active": true,
		},
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, lc.Coll, filter, level.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "level.activate", lc.Coll.Name(), tenantId, "", "", map[string]any{
		"levelId": level.ID, "sport": level.Sport, "level": level.Name,
	})
	result, err := lc.Coll.UpdateOne(ctx, filter, update)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("Attempt to activate %s failed.  Reason: %s", level.Name, err)
	}

	fmt.Println("Activate Results: ", result)

	if result.ModifiedCount == 1 {
		trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
		return nil
	}

	if result.MatchedCount != 1 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return fmt.Errorf("%s not found.", level.Name)
	}

	if result.MatchedCount == 1 && result.ModifiedCount == 0 {
		trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
		return nil
	}

	return fmt.Errorf("Generic error attempting to activate %s", level.Name)

}

func (lc *LevelsCollection) Deactivate(level Level, tenantId string) error {
	return lc.DeactivateWithContext(context.Background(), level, tenantId)
}

func (lc *LevelsCollection) DeactivateWithContext(parentCtx context.Context, level Level, tenantId string) error {
	level.ID = strings.TrimSpace(level.ID)
	level.Name = strings.TrimSpace(level.Name)

	fmt.Println("Level:", level)
	filter := bson.M{
		"id":       level.ID,
		"tenantId": tenantId,
	}

	update := bson.M{
		"$set": bson.M{
			"active": false,
		},
	}

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	conditional, conflictErr := ConditionalFilter(ctx, lc.Coll, filter, level.Version)
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "level.deactivate", lc.Coll.Name(), tenantId, "", "", map[string]any{
		"levelId": level.ID, "sport": level.Sport, "level": level.Name,
	})
	result, err := lc.Coll.UpdateOne(ctx, filter, update)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf("Attempt to deactivate %s failed.  Reason: %s", level.Name, err)
	}

	fmt.Println("Deactivate Results: ", result)
	if result.ModifiedCount == 1 {
		trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
		return nil
	}

	if result.MatchedCount != 1 {
		trace.Failure(ErrConflict, map[string]any{"matchedCount": result.MatchedCount}, "unmatched")
		return fmt.Errorf("%s not found.", level.Name)
	}

	if result.MatchedCount == 1 && result.ModifiedCount == 0 {
		trace.Success(map[string]any{"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount}, nil)
		return nil
	}

	return fmt.Errorf("Generic error attempting to deactivate %s", level.Name)
}

func (lc *LevelsCollection) Delete(levelID string, tenantID string, versions ...string) error {
	return lc.DeleteWithContext(context.Background(), levelID, tenantID, versions...)
}

func (lc *LevelsCollection) DeleteWithContext(parentCtx context.Context, levelID string, tenantID string, versions ...string) error {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	levelID = strings.TrimSpace(levelID)
	tenantID = strings.TrimSpace(tenantID)

	if levelID == "" {
		return fmt.Errorf("level ID is required")
	}

	if tenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}

	var existing Level
	if err := lc.Coll.FindOne(ctx, bson.M{
		"id": levelID, "tenantId": tenantID,
	}).Decode(&existing); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrConflict
		}
		return err
	}
	gamesFilter := bson.M{"tenantId": tenantID, "level": existing.Name}
	// Unassigned legacy levels may be referenced by any sport.
	if existing.Sport != "" {
		gamesFilter["sport"] = existing.Sport
	}

	gamesCollection := Client.Database(Database).Collection("games")

	count, err := gamesCollection.CountDocuments(ctx, gamesFilter)
	if err != nil {
		return fmt.Errorf("failed to query games collection: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("Failed to delete level as it is assigned to games.")
	}

	filter := bson.M{
		"id":       levelID,
		"tenantId": tenantID,
	}

	conditional, conflictErr := ConditionalFilter(ctx, lc.Coll, filter, firstVersion(versions))
	if conflictErr != nil {
		return conflictErr
	}
	filter = conditional

	trace := NewWriteTrace(ctx, "level.delete", lc.Coll.Name(), tenantID, "", "", map[string]any{
		"levelId": levelID, "sport": existing.Sport, "level": existing.Name,
	})
	result, err := lc.Coll.DeleteOne(
		ctx,
		filter,
	)
	if err != nil {
		trace.Failure(err, nil, "failed")
		return fmt.Errorf(
			"failed to delete level %q: %w",
			levelID,
			err,
		)
	}

	if result.DeletedCount == 0 {
		trace.Failure(ErrConflict, map[string]any{"deletedCount": result.DeletedCount}, "unmatched")
		return ErrConflict
	}

	trace.Success(map[string]any{"deletedCount": result.DeletedCount}, nil)
	return nil
}

func (lc *LevelsCollection) GetLevels(tenantId string, activeOnly bool, sports ...string) ([]Level, error) {

	ctx := context.TODO()

	filter := bson.M{
		"tenantId": tenantId,
	}

	if len(sports) > 0 && strings.TrimSpace(sports[0]) != "" {
		filter["sport"] = strings.TrimSpace(sports[0])
	}

	if activeOnly {
		filter["active"] = true
	}

	fmt.Println("Searching for Levels for tenantId", tenantId)

	opts := options.Find().
		SetSort(bson.D{
			{Key: "sport", Value: 1},
			{Key: "name", Value: 1},
		})

	cursor, err := lc.Coll.Find(ctx, filter, opts)
	if err != nil {
		fmt.Println("Failed to query levels: Error=[", err, "]")
		return nil, fmt.Errorf(
			"failed to query levels: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	levels := []Level{}
	for cursor.Next(ctx) {
		raw := cursor.Current
		var level Level
		if err := bson.Unmarshal(raw, &level); err != nil {
			fmt.Println("failed to decode levels. err=[", err, "]")
			return nil, fmt.Errorf("failed to decode levels: %w", err)
		}
		level.Version = RecordVersion(raw)
		levels = append(levels, level)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("failed to decode levels: %w", err)
	}

	fmt.Println("Total levels found for tenant", tenantId, ":", len(levels))

	return levels, nil
}

func (lc *LevelsCollection) LevelExists(
	tenantId string,
	levelName string,
) (bool, error) {

	ctx := context.TODO()

	tenantId = strings.TrimSpace(tenantId)
	levelName = strings.TrimSpace(levelName)

	if tenantId == "" {
		return false, fmt.Errorf("tenant ID is required")
	}

	if levelName == "" {
		return false, fmt.Errorf("level name is required")
	}

	filter := bson.M{
		"tenantId": tenantId,
		"name":     levelName,
	}

	fmt.Printf(
		"Searching for level %q for tenantId %s\n",
		levelName,
		tenantId,
	)

	err := lc.Coll.FindOne(ctx, filter).Err()

	if err == nil {
		fmt.Printf(
			"Level %q exists for tenantId %s\n",
			levelName,
			tenantId,
		)
		return true, nil
	}

	if errors.Is(err, mongo.ErrNoDocuments) {
		fmt.Printf(
			"Level %q does not exist for tenantId %s\n",
			levelName,
			tenantId,
		)
		return false, nil
	}

	return false, fmt.Errorf(
		"failed to verify level %q: %w",
		levelName,
		err,
	)
}

type Sport struct {
	ID     string `bson:"id" json:"id"`
	Name   string `bson:"name" json:"name"`
	Active bool   `bson:"active" json:"active"`
}

type SportsCollection struct {
	DB        *mongo.Database
	Coll      *mongo.Collection
	LastError error
}

func (sc *SportsCollection) Init(client *mongo.Client) error {

	sc.DB = client.Database(Database)
	sc.Coll = sc.DB.Collection("sports")

	return nil
}

func (sc *SportsCollection) GetSports(activeOnly bool) ([]Sport, error) {

	ctx := context.TODO()

	filter := bson.M{}

	if activeOnly {
		filter["active"] = true
	}

	opts := options.Find().
		SetSort(bson.D{
			{Key: "name", Value: 1},
		})

	cursor, err := sc.Coll.Find(ctx, filter, opts)
	if err != nil {
		fmt.Println("Failed to query sports: Error=[", err, "]")
		return nil, fmt.Errorf(
			"failed to query sports: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	sports := []Sport{}

	if err := cursor.All(ctx, &sports); err != nil {
		fmt.Println("failed to decode sports. err=[", err, "]")
		return nil, fmt.Errorf(
			"failed to decode sports: %w",
			err,
		)
	}

	fmt.Println("Total sports found:", len(sports))
	return sports, nil
}

func formatInt64Hundredths(value int64) string {
	sign := ""

	if value < 0 {
		sign = "-"
		value = -value
	}

	return fmt.Sprintf(
		"%s%d.%02d",
		sign,
		value/100,
		value%100,
	)
}

func formatCurrency(cents int64) string {
	if cents < 0 {
		return "-$" + formatInt64Hundredths(-cents)
	}

	return "$" + formatInt64Hundredths(cents)
}

func Get1099Reports(
	tId string,
	associations []string,
	taxYear string,
) (map[string]model.ReportResponse1099, error) {
	paymentFilter := bson.M{
		"tenantId": tId,
	}

	// A blank tax year includes all years.
	taxYear = strings.TrimSpace(taxYear)
	if taxYear != "" {
		year, err := strconv.Atoi(taxYear)
		if err != nil || year < 1000 || year > 9999 {
			return nil, fmt.Errorf("invalid tax year: %q", taxYear)
		}
		paymentFilter["taxYear"] = year
	}

	// Remove empty association values and duplicates.
	selectedAssociations := make([]string, 0, len(associations))
	seenAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)
		if associationName == "" {
			continue
		}
		if _, exists := seenAssociations[associationName]; exists {
			continue
		}

		seenAssociations[associationName] = struct{}{}
		selectedAssociations = append(selectedAssociations, associationName)
	}

	// No association filter means include all associations.
	if len(selectedAssociations) > 0 {
		paymentFilter["association"] = bson.M{
			"$in": selectedAssociations,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paymentColl := Client.Database(Database).Collection("payments")
	paymentCursor, err := paymentColl.Find(ctx, paymentFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"Get1099Reports payment query failed: %w", err,
		)
	}
	defer paymentCursor.Close(ctx)

	records1099 := make(map[string]model.Report1099)

	for paymentCursor.Next(ctx) {
		var payment model.PaymentDoc
		if err := paymentCursor.Decode(&payment); err != nil {
			return nil, fmt.Errorf(
				"Get1099Reports payment decode failed: %w", err,
			)
		}

		// Group payments by both tax year and association.
		key := fmt.Sprintf("%d|%s", payment.TaxYear, payment.Association)

		record := records1099[key]
		record.Taxyear = payment.TaxYear
		record.Association = payment.Association
		record.Amount += payment.PaymentAmt
		records1099[key] = record
	}

	if err := paymentCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"Get1099Reports payment cursor failed: %w", err,
		)
	}

	response := make(map[string]model.ReportResponse1099, len(records1099))

	for key, record := range records1099 {
		// Format cents as dollars without floating-point conversion.
		dollars := record.Amount / 100
		cents := record.Amount % 100
		sign := ""

		if record.Amount < 0 {
			sign = "-"
			dollars = -dollars
			cents = -cents
		}

		response[key] = model.ReportResponse1099{
			Taxyear:     record.Taxyear,
			Association: record.Association,
			Amount:      fmt.Sprintf("%s%d.%02d", sign, dollars, cents),
		}
	}

	return response, nil
}

func GetReconciliationReports(
	tId string,
	associations []string,
	taxYear string,
) (map[string]model.ReconciliationReportResponse, error) {
	ctx := context.Background()

	reconciliationRecords :=
		make(map[string]model.ReconciliationReport)

	// Remove empty association values and duplicates.
	selectedAssociations := make([]string, 0, len(associations))
	seenAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)

		if associationName == "" {
			continue
		}

		if _, exists := seenAssociations[associationName]; exists {
			continue
		}

		seenAssociations[associationName] = struct{}{}
		selectedAssociations = append(
			selectedAssociations,
			associationName,
		)
	}

	paymentFilter := bson.M{
		"tenantId": tId,
	}

	if len(selectedAssociations) > 0 {
		paymentFilter["association"] = bson.M{
			"$in": selectedAssociations,
		}
	}

	// A blank tax year includes payments from all years.
	taxYear = strings.TrimSpace(taxYear)

	if taxYear != "" {
		selectedTaxYear, err := strconv.Atoi(taxYear)
		if err != nil ||
			len(taxYear) != 4 ||
			selectedTaxYear < 1000 ||
			selectedTaxYear > 9999 {
			return nil, fmt.Errorf(
				"tax year must be a four-digit year from 1000 to 9999",
			)
		}

		paymentFilter["taxYear"] = selectedTaxYear
	}

	database := Client.Database(Database)
	paymentColl := database.Collection("payments")
	gamesColl := database.Collection("games")

	paymentCursor, err := paymentColl.Find(ctx, paymentFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"GetReconciliationReports payment query failed: %w",
			err,
		)
	}
	defer paymentCursor.Close(ctx)

	for paymentCursor.Next(ctx) {
		var payment model.PaymentDoc

		if err := paymentCursor.Decode(&payment); err != nil {
			return nil, fmt.Errorf(
				"GetReconciliationReports payment decode failed: %w",
				err,
			)
		}

		paymentKey := payment.PaymentId

		record := model.ReconciliationReport{
			Association:    payment.Association,
			PaymentId:      payment.PaymentId,
			PaymentDate:    payment.PaymentDate,
			TaxYear:        strconv.Itoa(payment.TaxYear),
			PaymentAmt:     payment.PaymentAmt,
			PaymentGameIds: payment.GameIds,
		}

		// Reconcile every game referenced by the payment,
		// regardless of the game's tax year.
		for _, gameID := range payment.GameIds {
			gameFilter := bson.M{
				"tenantId":    tId,
				"association": payment.Association,
				"gameId":      gameID,
			}

			var game model.GameDoc

			err := gamesColl.FindOne(
				ctx,
				gameFilter,
			).Decode(&game)

			if errors.Is(err, mongo.ErrNoDocuments) {
				continue
			}

			if err != nil {
				return nil, fmt.Errorf(
					"GetReconciliationReports game query failed "+
						"for payment %s, game %d: %w",
					payment.PaymentId,
					gameID,
					err,
				)
			}

			record.CalculatedAmt +=
				game.NumOfGames*game.GameFee +
					game.TravelPay -
					game.AssignorFee
		}
		record.CalculatedAmt -= payment.DeductionAmount

		switch {
		case record.CalculatedAmt == record.PaymentAmt:
			record.Status = "Reconciled"

		case record.CalculatedAmt > record.PaymentAmt:
			record.Status = "Under Paid"

		default:
			record.Status = "Over Paid"
		}

		reconciliationRecords[paymentKey] = record
	}

	if err := paymentCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"GetReconciliationReports payment cursor failed: %w",
			err,
		)
	}

	response := make(
		map[string]model.ReconciliationReportResponse,
		len(reconciliationRecords),
	)

	for paymentKey, record := range reconciliationRecords {
		response[paymentKey] =
			model.ReconciliationReportResponse{
				Association:    record.Association,
				PaymentId:      record.PaymentId,
				PaymentDate:    record.PaymentDate,
				TaxYear:        record.TaxYear,
				PaymentAmt:     formatCurrency(record.PaymentAmt),
				CalculatedAmt:  formatCurrency(record.CalculatedAmt),
				PaymentGameIds: record.PaymentGameIds,
				Status:         record.Status,
			}
	}

	return response, nil
}

func GetRevenueReports(
	tId string,
	associations []string,
	taxYear string,
) (map[string]model.RevenueReportResponse, error) {

	ctx := context.Background()

	taxYear = strings.TrimSpace(taxYear)

	if taxYear != "" {
		year, err := strconv.Atoi(taxYear)
		if err != nil || len(taxYear) != 4 ||
			year < 1000 || year > 9999 {
			return nil, fmt.Errorf(
				"tax year must be between 1000 and 9999",
			)
		}
	}

	type reportKey struct {
		Association string
		TaxYear     string
	}

	revenueRecords := make(map[reportKey]model.RevenueReport)
	selectedAssociations := make([]string, 0, len(associations))
	allowedAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)

		if associationName == "" {
			continue
		}

		if _, exists := allowedAssociations[associationName]; exists {
			continue
		}

		allowedAssociations[associationName] = struct{}{}
		selectedAssociations = append(
			selectedAssociations,
			associationName,
		)
	}

	if len(selectedAssociations) == 0 {
		associationsColl :=
			Client.Database(Database).Collection("associations")

		cursor, err := associationsColl.Find(
			ctx,
			bson.M{"tenantId": tId},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"GetRevenueReports associations query failed: %w",
				err,
			)
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var associationDoc AssociationDoc

			if err := cursor.Decode(&associationDoc); err != nil {
				return nil, fmt.Errorf(
					"GetRevenueReports association decode failed: %w",
					err,
				)
			}

			associationName := strings.TrimSpace(associationDoc.Id)

			if associationName != "" {
				allowedAssociations[associationName] = struct{}{}
			}
		}

		if err := cursor.Err(); err != nil {
			return nil, fmt.Errorf(
				"GetRevenueReports associations cursor failed: %w",
				err,
			)
		}
	}

	gameFilter := bson.M{
		"tenantId": tId,
		"status":   "Paid",
	}

	if taxYear != "" {
		gameFilter["taxYear"] = taxYear
	}

	if len(selectedAssociations) > 0 {
		gameFilter["association"] = bson.M{
			"$in": selectedAssociations,
		}
	}

	gamesColl := Client.Database(Database).Collection("games")

	gameCursor, err := gamesColl.Find(ctx, gameFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"GetRevenueReports games query failed: %w",
			err,
		)
	}
	defer gameCursor.Close(ctx)

	for gameCursor.Next(ctx) {
		var game model.GameDoc

		if err := gameCursor.Decode(&game); err != nil {
			return nil, fmt.Errorf(
				"GetRevenueReports game decode failed: %w",
				err,
			)
		}

		associationName := strings.TrimSpace(game.Association)

		if _, allowed := allowedAssociations[associationName]; !allowed {
			continue
		}

		key := reportKey{
			Association: associationName,
			TaxYear:     strings.TrimSpace(game.TaxYear),
		}

		record := revenueRecords[key]
		record.Association = key.Association
		record.TaxYear = key.TaxYear

		gameFees := game.NumOfGames * game.GameFee
		revenue := gameFees + game.TravelPay

		record.NumOfGames += game.NumOfGames
		record.TotGameFees += gameFees
		record.TotTravelPay += game.TravelPay
		record.TotAssignorFees += game.AssignorFee
		record.GrossRevenue += revenue
		record.NetRevenue += revenue - game.AssignorFee

		revenueRecords[key] = record
	}

	if err := gameCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"GetRevenueReports games cursor failed: %w",
			err,
		)
	}

	expenseFilter := bson.M{
		"tenantId":   tId,
		"source":     PayrollDeductionSource,
		"paymentRef": bson.M{"$exists": true, "$ne": ""},
	}
	if len(selectedAssociations) > 0 {
		expenseFilter["association"] = bson.M{"$in": selectedAssociations}
	}
	if taxYear != "" {
		expenseFilter["taxYear"] = taxYear
	}
	payrollCursor, err := Client.Database(Database).Collection("expenses").Find(ctx, expenseFilter)
	if err != nil {
		return nil, fmt.Errorf("GetRevenueReports payroll expenses query failed: %w", err)
	}
	defer payrollCursor.Close(ctx)
	for payrollCursor.Next(ctx) {
		var expense model.ExpenseDoc
		if err := payrollCursor.Decode(&expense); err != nil {
			return nil, fmt.Errorf("GetRevenueReports payroll expense decode failed: %w", err)
		}
		associationName := strings.TrimSpace(expense.Association)
		if _, allowed := allowedAssociations[associationName]; !allowed {
			continue
		}
		key := reportKey{Association: associationName, TaxYear: strings.TrimSpace(expense.TaxYear)}
		record := revenueRecords[key]
		record.Association = key.Association
		record.TaxYear = key.TaxYear
		record.PayrollDeductions += expense.Amount
		record.NetRevenue -= expense.Amount
		revenueRecords[key] = record
	}
	if err := payrollCursor.Err(); err != nil {
		return nil, fmt.Errorf("GetRevenueReports payroll expenses cursor failed: %w", err)
	}

	response := make(
		map[string]model.RevenueReportResponse,
		len(revenueRecords),
	)

	for key, record := range revenueRecords {
		responseKey := fmt.Sprintf(
			"%d:%s:%s",
			len(key.Association),
			key.Association,
			key.TaxYear,
		)

		response[responseKey] = model.RevenueReportResponse{
			Association:       record.Association,
			TaxYear:           record.TaxYear,
			NumOfGames:        strconv.FormatInt(record.NumOfGames, 10),
			TotGameFees:       formatCurrency(record.TotGameFees),
			TotTravelPay:      formatCurrency(record.TotTravelPay),
			GrossRevenue:      formatCurrency(record.GrossRevenue),
			TotAssignorFees:   formatCurrency(record.TotAssignorFees),
			PayrollDeductions: formatCurrency(record.PayrollDeductions),
			NetRevenue:        formatCurrency(record.NetRevenue),
		}
	}

	return response, nil
}

func GetFinancialReports(
	tId string,
	associations []string,
	taxyear string,
) (map[string]model.FinancialReportResponse, error) {

	ctx := context.Background()

	taxyear = strings.TrimSpace(taxyear)

	if taxyear != "" {
		year, err := strconv.Atoi(taxyear)
		if err != nil || len(taxyear) != 4 ||
			year < 1000 || year > 9999 {
			return nil, fmt.Errorf(
				"tax year must be between 1000 and 9999",
			)
		}
	}

	type reportKey struct {
		Association string
		TaxYear     string
	}

	financialRecords := make(
		map[reportKey]model.FinancialReport,
	)

	selectedAssociations := make([]string, 0, len(associations))
	allowedAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)

		if associationName == "" {
			continue
		}

		if _, exists := allowedAssociations[associationName]; exists {
			continue
		}

		allowedAssociations[associationName] = struct{}{}
		selectedAssociations = append(
			selectedAssociations,
			associationName,
		)
	}

	if len(selectedAssociations) == 0 {
		associationsColl :=
			Client.Database(Database).Collection("associations")

		cursor, err := associationsColl.Find(
			ctx,
			bson.M{"tenantId": tId},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"GetFinancialReports associations query failed: %w",
				err,
			)
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var associationDoc AssociationDoc

			if err := cursor.Decode(&associationDoc); err != nil {
				return nil, fmt.Errorf(
					"GetFinancialReports association decode failed: %w",
					err,
				)
			}

			associationName := strings.TrimSpace(associationDoc.Id)

			if associationName != "" {
				allowedAssociations[associationName] = struct{}{}
			}
		}

		if err := cursor.Err(); err != nil {
			return nil, fmt.Errorf(
				"GetFinancialReports associations cursor failed: %w",
				err,
			)
		}
	}

	buildFilter := func() bson.M {
		filter := bson.M{
			"tenantId": tId,
		}

		if len(selectedAssociations) > 0 {
			filter["association"] = bson.M{
				"$in": selectedAssociations,
			}
		}

		if taxyear != "" {
			filter["taxYear"] = taxyear
		}

		return filter
	}

	// Group earnings from paid games by association and tax year.
	gameFilter := buildFilter()
	gameFilter["status"] = "Paid"

	gamesColl := Client.Database(Database).Collection("games")

	gameCursor, err := gamesColl.Find(ctx, gameFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"GetFinancialReports games query failed: %w",
			err,
		)
	}
	defer gameCursor.Close(ctx)

	for gameCursor.Next(ctx) {
		var game struct {
			Association string `bson:"association"`
			TaxYear     string `bson:"taxYear"`
			NumOfGames  int64  `bson:"numOfGames"`
			GameFee     int64  `bson:"gameFee"`
			TravelPay   int64  `bson:"travelPay"`
			AssignorFee int64  `bson:"assignorFee"`
		}

		if err := gameCursor.Decode(&game); err != nil {
			return nil, fmt.Errorf(
				"GetFinancialReports game decode failed: %w",
				err,
			)
		}

		associationName := strings.TrimSpace(game.Association)

		if _, allowed := allowedAssociations[associationName]; !allowed {
			continue
		}

		key := reportKey{
			Association: associationName,
			TaxYear:     strings.TrimSpace(game.TaxYear),
		}

		record := financialRecords[key]
		record.Association = key.Association
		record.TaxYear = key.TaxYear

		record.GrossIncome +=
			game.NumOfGames*game.GameFee +
				game.TravelPay -
				game.AssignorFee

		financialRecords[key] = record
	}

	if err := gameCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"GetFinancialReports games cursor failed: %w",
			err,
		)
	}

	// Group expenses and mileage by association and tax year.
	expensesColl :=
		Client.Database(Database).Collection("expenses")

	expenseCursor, err := expensesColl.Find(ctx, buildFilter())
	if err != nil {
		return nil, fmt.Errorf(
			"GetFinancialReports expenses query failed: %w",
			err,
		)
	}
	defer expenseCursor.Close(ctx)

	for expenseCursor.Next(ctx) {
		var expense struct {
			Association string `bson:"association"`
			TaxYear     string `bson:"taxYear"`
			Type        string `bson:"type"`
			Amount      int64  `bson:"amount"`
			Mileage     int64  `bson:"mileage"`
			Source      string `bson:"source"`
		}

		if err := expenseCursor.Decode(&expense); err != nil {
			return nil, fmt.Errorf(
				"GetFinancialReports expense decode failed: %w",
				err,
			)
		}

		associationName := strings.TrimSpace(expense.Association)

		if _, allowed := allowedAssociations[associationName]; !allowed {
			continue
		}

		key := reportKey{
			Association: associationName,
			TaxYear:     strings.TrimSpace(expense.TaxYear),
		}

		record := financialRecords[key]
		record.Association = key.Association
		record.TaxYear = key.TaxYear

		if expense.Source == PayrollDeductionSource {
			record.TotalExpenses += expense.Amount
		} else if strings.EqualFold(strings.TrimSpace(expense.Type), "Mileage") {
			record.TotalMileage += expense.Mileage
		} else {
			record.TotalExpenses += expense.Amount
		}

		financialRecords[key] = record
	}

	if err := expenseCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"GetFinancialReports expenses cursor failed: %w",
			err,
		)
	}

	response := make(
		map[string]model.FinancialReportResponse,
		len(financialRecords),
	)

	for key, record := range financialRecords {
		record.NetIncome = record.GrossIncome - record.TotalExpenses

		responseKey := fmt.Sprintf(
			"%d:%s:%s",
			len(key.Association),
			key.Association,
			key.TaxYear,
		)

		response[responseKey] = model.FinancialReportResponse{
			Association:   record.Association,
			TaxYear:       record.TaxYear,
			GrossIncome:   formatCurrency(record.GrossIncome),
			TotalExpenses: formatCurrency(record.TotalExpenses),
			NetIncome:     formatCurrency(record.NetIncome),
			TotalMileage:  formatInt64Hundredths(record.TotalMileage),
		}
	}

	return response, nil
}

func GetExpenseReports(
	tId string,
	associations []string,
	taxyear string,
) (map[string]model.ExpenseReportResponse, error) {

	ctx := context.Background()

	taxyear = strings.TrimSpace(taxyear)

	if taxyear != "" {
		year, err := strconv.Atoi(taxyear)
		if err != nil || len(taxyear) != 4 ||
			year < 1000 || year > 9999 {
			return nil, fmt.Errorf(
				"tax year must be between 1000 and 9999",
			)
		}
	}

	type reportKey struct {
		Association string
		TaxYear     string
	}

	expenseRecords := make(map[reportKey]model.ExpenseReport)
	selectedAssociations := make([]string, 0, len(associations))
	allowedAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)

		if associationName == "" {
			continue
		}

		if _, exists := allowedAssociations[associationName]; exists {
			continue
		}

		allowedAssociations[associationName] = struct{}{}
		selectedAssociations = append(
			selectedAssociations,
			associationName,
		)
	}

	if len(selectedAssociations) == 0 {
		associationsColl :=
			Client.Database(Database).Collection("associations")

		cursor, err := associationsColl.Find(
			ctx,
			bson.M{"tenantId": tId},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"GetExpenseReports associations query failed: %w",
				err,
			)
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var associationDoc AssociationDoc

			if err := cursor.Decode(&associationDoc); err != nil {
				return nil, fmt.Errorf(
					"GetExpenseReports association decode failed: %w",
					err,
				)
			}

			associationName := strings.TrimSpace(associationDoc.Id)

			if associationName != "" {
				allowedAssociations[associationName] = struct{}{}
			}
		}

		if err := cursor.Err(); err != nil {
			return nil, fmt.Errorf(
				"GetExpenseReports associations cursor failed: %w",
				err,
			)
		}
	}

	// Pre-fill selected associations when a specific year is requested.
	if taxyear != "" {
		for associationName := range allowedAssociations {
			key := reportKey{
				Association: associationName,
				TaxYear:     taxyear,
			}

			expenseRecords[key] = model.ExpenseReport{
				Association: associationName,
				TaxYear:     taxyear,
			}
		}
	}

	buildFilter := func() bson.M {
		filter := bson.M{
			"tenantId": tId,
		}

		if len(selectedAssociations) > 0 {
			filter["association"] = bson.M{
				"$in": selectedAssociations,
			}
		}

		if taxyear != "" {
			filter["taxYear"] = taxyear
		}

		return filter
	}

	// Group expenses by association and tax year.
	expensesColl :=
		Client.Database(Database).Collection("expenses")

	expenseCursor, err := expensesColl.Find(ctx, buildFilter())
	if err != nil {
		return nil, fmt.Errorf(
			"GetExpenseReports expenses query failed: %w",
			err,
		)
	}
	defer expenseCursor.Close(ctx)

	for expenseCursor.Next(ctx) {
		var expense struct {
			Association string `bson:"association"`
			TaxYear     string `bson:"taxYear"`
			Type        string `bson:"type"`
			Amount      int64  `bson:"amount"`
			Mileage     int64  `bson:"mileage"`
			Source      string `bson:"source"`
		}

		if err := expenseCursor.Decode(&expense); err != nil {
			return nil, fmt.Errorf(
				"GetExpenseReports expense decode failed: %w",
				err,
			)
		}

		associationName := strings.TrimSpace(expense.Association)

		if _, allowed := allowedAssociations[associationName]; !allowed {
			continue
		}

		key := reportKey{
			Association: associationName,
			TaxYear:     strings.TrimSpace(expense.TaxYear),
		}

		record := expenseRecords[key]
		record.Association = key.Association
		record.TaxYear = key.TaxYear

		if expense.Source == PayrollDeductionSource {
			record.PayrollDeductions += expense.Amount
			record.TotalExpenses += expense.Amount
		} else {
			switch strings.ToLower(strings.TrimSpace(expense.Type)) {
			case "food":
				record.Food += expense.Amount
			case "dues", "association dues":
				record.Dues += expense.Amount
			case "camp fees", "camp fee", "campfees":
				record.CampFees += expense.Amount
			case "equipment":
				record.Equipment += expense.Amount
			case "mileage":
				record.Mileage += expense.Mileage
			}
			if !strings.EqualFold(strings.TrimSpace(expense.Type), "Mileage") {
				record.TotalExpenses += expense.Amount
			}
		}

		expenseRecords[key] = record
	}

	if err := expenseCursor.Err(); err != nil {
		return nil, fmt.Errorf(
			"GetExpenseReports expenses cursor failed: %w",
			err,
		)
	}

	response := make(
		map[string]model.ExpenseReportResponse,
		len(expenseRecords),
	)

	for key, record := range expenseRecords {
		// Length-prefixed association prevents composite-key collisions.
		responseKey := fmt.Sprintf(
			"%d:%s:%s",
			len(key.Association),
			key.Association,
			key.TaxYear,
		)

		response[responseKey] = model.ExpenseReportResponse{
			Association:       record.Association,
			TaxYear:           record.TaxYear,
			Food:              formatCurrency(record.Food),
			Dues:              formatCurrency(record.Dues),
			CampFees:          formatCurrency(record.CampFees),
			Equipment:         formatCurrency(record.Equipment),
			Mileage:           formatInt64Hundredths(record.Mileage),
			PayrollDeductions: formatCurrency(record.PayrollDeductions),
			TotalExpenses:     formatCurrency(record.TotalExpenses),
		}
	}

	return response, nil
}

func GetAccountsReceivableReport(
	tId string,
	associations []string,
) (map[string]model.AccountsReceivableReportResponse, error) {

	ctx := context.Background()

	acctsReceivableRecords :=
		make(map[string]model.AccountsReceivableReport)

	// Remove empty association names and duplicates.
	selectedAssociations := make([]string, 0, len(associations))
	seenAssociations := make(map[string]struct{})

	for _, associationName := range associations {
		associationName = strings.TrimSpace(associationName)

		if associationName == "" {
			continue
		}

		if _, exists := seenAssociations[associationName]; exists {
			continue
		}

		seenAssociations[associationName] = struct{}{}

		selectedAssociations = append(
			selectedAssociations,
			associationName,
		)
	}

	if len(selectedAssociations) > 0 {
		// Pre-fill the report with the associations selected
		// by the customer.
		for _, associationName := range selectedAssociations {
			acctsReceivableRecords[associationName] =
				model.AccountsReceivableReport{
					Association: associationName,
					GameIds:     make([]int64, 0),
				}
		}
	} else {
		// No associations were selected, so load every
		// association belonging to the tenant.
		associationFilter := bson.M{
			"tenantId": tId,
		}

		associationsColl :=
			Client.Database(Database).Collection("associations")

		associationCursor, err :=
			associationsColl.Find(ctx, associationFilter)
		if err != nil {
			return nil, fmt.Errorf(
				"GetAccountsReceivableReport associations query failed: %w",
				err,
			)
		}

		for associationCursor.Next(ctx) {
			var associationDoc AssociationDoc

			if err := associationCursor.Decode(&associationDoc); err != nil {
				associationCursor.Close(ctx)

				return nil, fmt.Errorf(
					"GetAccountsReceivableReport association decode failed: %w",
					err,
				)
			}

			associationName :=
				strings.TrimSpace(associationDoc.Id)

			if associationName == "" {
				continue
			}

			acctsReceivableRecords[associationName] =
				model.AccountsReceivableReport{
					Association: associationName,
					GameIds:     make([]int64, 0),
				}
		}

		if err := associationCursor.Err(); err != nil {
			associationCursor.Close(ctx)

			return nil, fmt.Errorf(
				"GetAccountsReceivableReport associations cursor failed: %w",
				err,
			)
		}

		if err := associationCursor.Close(ctx); err != nil {
			return nil, fmt.Errorf(
				"GetAccountsReceivableReport failed to close associations cursor: %w",
				err,
			)
		}
	}

	// Load completed games.
	gameFilter := bson.M{
		"tenantId": tId,
		"status":   "Completed",
	}

	if len(selectedAssociations) > 0 {
		gameFilter["association"] = bson.M{
			"$in": selectedAssociations,
		}
	}

	gamesColl :=
		Client.Database(Database).Collection("games")

	gameCursor, err := gamesColl.Find(ctx, gameFilter)
	if err != nil {
		return nil, fmt.Errorf(
			"GetAccountsReceivableReport games query failed: %w",
			err,
		)
	}

	for gameCursor.Next(ctx) {
		var game model.GameDoc

		if err := gameCursor.Decode(&game); err != nil {
			gameCursor.Close(ctx)

			return nil, fmt.Errorf(
				"GetAccountsReceivableReport game decode failed: %w",
				err,
			)
		}

		associationName :=
			strings.TrimSpace(game.Association)

		record, exists :=
			acctsReceivableRecords[associationName]
		if !exists {
			// Ignore games whose association is not in the
			// associations collection for this tenant.
			continue
		}

		record.AccountsReceivable +=
			game.NumOfGames*game.GameFee +
				game.TravelPay -
				game.AssignorFee

		record.GameIds = append(
			record.GameIds,
			game.GameId,
		)

		acctsReceivableRecords[associationName] = record
	}

	if err := gameCursor.Err(); err != nil {
		gameCursor.Close(ctx)

		return nil, fmt.Errorf(
			"GetAccountsReceivableReport games cursor failed: %w",
			err,
		)
	}

	if err := gameCursor.Close(ctx); err != nil {
		return nil, fmt.Errorf(
			"GetAccountsReceivableReport failed to close games cursor: %w",
			err,
		)
	}

	// Convert the database records into values formatted
	// for the web response.
	response := make(
		map[string]model.AccountsReceivableReportResponse,
		len(acctsReceivableRecords),
	)

	for associationName, record := range acctsReceivableRecords {

		response[associationName] =
			model.AccountsReceivableReportResponse{
				Association: record.Association,
				AccountsReceivable: formatCurrency(
					record.AccountsReceivable,
				),
				GameIds: record.GameIds,
			}
	}

	return response, nil
}

func GetGameReport(filter model.GameFilter) ([]model.GameView, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gameReportRecords := []model.GameView{}

	//
	// Create Filter
	//
	gameFilter := BuildMongoGameFilter(filter)

	fmt.Println("===== GetGameReport:", gameFilter, "=====")

	//
	// Get Game Documents
	//
	cursor, err := Client.Database(Database).Collection("games").Find(ctx, gameFilter)
	if err != nil {
		return nil, fmt.Errorf("query games report: %w", err)
	}
	defer cursor.Close(ctx)

	var results []model.GameDoc

	err = cursor.All(ctx, &results)
	if err != nil {
		fmt.Println("Error", err)
		return []model.GameView{}, err
	}

	for _, r := range results {

		view := model.GameView{
			Version:     r.Version,
			GameId:      r.GameId,
			Time:        r.Time,
			Sport:       r.Sport,
			Site:        r.Site,
			Field:       r.Field,
			NumOfGames:  r.NumOfGames,
			Level:       r.Level,
			Home:        r.Home,
			Visitor:     r.Visitor,
			Status:      r.Status,
			Association: r.Association,
			Assignor:    r.Assignor,
			GameFee:     utils.ConvertInt64ToAmtStr(r.GameFee),
			AssignorFee: utils.ConvertInt64ToAmtStr(r.AssignorFee),
			TravelPay:   utils.ConvertInt64ToAmtStr(r.TravelPay),
			TaxYear:     r.TaxYear,
			Notes:       r.Notes,
		}

		gameRec := model.GameDescriptor{
			Version:     r.Version,
			GameFee:     utils.ConvertInt64ToAmtStr(r.GameFee),
			NumOfGames:  utils.ConvertInt64ToStr(r.NumOfGames),
			TravelPay:   utils.ConvertInt64ToAmtStr(r.TravelPay),
			AssignorFee: utils.ConvertInt64ToAmtStr(r.AssignorFee),
		}

		totalEarnings := utils.CalculateGameFee(gameRec)
		view.TotalEarnings = utils.ConvertInt64ToAmtStr(totalEarnings)

		abbrev := utils.DayOfWeekAbbreviation(r.Date)
		view.Date = fmt.Sprintf("%s (%s)", r.Date, abbrev)

		view.Officials = make([]model.OfficialView, 0)
		for _, assignment := range gameOfficialsForRead(r) {
			id, _ := strconv.ParseInt(assignment.OfficialId, 10, 64)
			name := assignment.Name
			if name == "" {
				name = "Unassigned"
			}
			view.Officials = append(view.Officials, model.OfficialView{
				OfficialId: id, Name: name, RoleId: assignment.RoleId,
				Title: assignment.Title, DisplayOrder: assignment.DisplayOrder,
			})
			// Transitional response for pages that still display ECO separately.
			if assignment.Title == "ECO" {
				view.ECO = name
			}
		}

		gameReportRecords = append(gameReportRecords, view)

	}

	fmt.Println("Game Report Records:", gameReportRecords)

	return gameReportRecords, nil
}

type ExpenseType struct {
	ExpenseType string `json:"expenseType"`
}

func GetExpenseTypes() []ExpenseType {
	return []ExpenseType{
		{ExpenseType: "Food"},
		{ExpenseType: "Hotel"},
		{ExpenseType: "Equipment"},
		{ExpenseType: "Mileage"},
		{ExpenseType: "Camp Fees"},
		{ExpenseType: "Association Dues"},
	}
}

type StatusType struct {
	Status string `json:"status"`
}

func GetStatusTypes() []StatusType {
	return []StatusType{
		{Status: "Cancelled"},
		{Status: "Completed"},
		{Status: "Delete"},
		{Status: "Paid"},
		{Status: "Pending"},
	}
}

func CreateSettingsIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	coll := Client.Database(Database).Collection("settings")

	index := mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenantId", Value: 1},
		},
		Options: options.Index().
			SetUnique(true).
			SetName("settings_tenantId_unique"),
	}

	_, err := coll.Indexes().CreateOne(ctx, index)
	if err != nil {
		return fmt.Errorf("failed to create settings index: %w", err)
	}

	return nil
}

func GetOfficialRoles(sport string) (model.OfficialRolesRequest, error) {
	response := model.OfficialRolesRequest{
		Sport: sport,
		Roles: make([]model.OfficialRole, 0),
	}

	if strings.TrimSpace(sport) == "" {
		return response, fmt.Errorf("sport is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	coll := Client.Database(Database).Collection("roles")

	filter := bson.M{
		"officialRoles": bson.M{
			"$elemMatch": bson.M{"sport": sport},
		},
	}

	projection := bson.M{
		"_id": 0,
		"officialRoles": bson.M{
			"$elemMatch": bson.M{"sport": sport},
		},
	}

	var doc struct {
		OfficialRoles []model.OfficialRolesRequest `bson:"officialRoles"`
	}

	err := coll.FindOne(
		ctx,
		filter,
		options.FindOne().SetProjection(projection),
	).Decode(&doc)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return response, nil
	}
	if err != nil {
		return response, fmt.Errorf(
			"failed to get official roles for %q: %w",
			sport,
			err,
		)
	}

	if len(doc.OfficialRoles) > 0 && doc.OfficialRoles[0].Roles != nil {
		response.Roles = doc.OfficialRoles[0].Roles
	}

	sort.SliceStable(response.Roles, func(i, j int) bool {
		return response.Roles[i].DisplayOrder < response.Roles[j].DisplayOrder
	})

	return response, nil
}

func convertGameDescrWithOfficials(game model.GameDescriptor) model.GameDoc {
	return utils.ConvertGameDescrToGameDoc(game)
}

func convertGameDocWithOfficials(doc model.GameDoc) model.GameDescriptor {
	game := utils.ConvertGameDocToGameDescr(doc)
	game.Officials = gameOfficialsForRead(doc)
	return game
}

func gameOfficialsForRead(doc model.GameDoc) []model.GameOfficialDoc {
	result := append([]model.GameOfficialDoc{}, doc.Officials...)

	if len(result) > 0 && Client != nil {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()

		db := Client.Database(Database)

		for i := range result {
			if err := resolveGameOfficial(
				ctx,
				db,
				doc.TenantId,
				&result[i],
			); err != nil {
				// Retain the saved snapshot if resolution fails.
				log.Printf(
					"game %d role %q: %v",
					doc.GameId,
					result[i].Title,
					err,
				)
			}
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].DisplayOrder < result[j].DisplayOrder
	})

	return result
}

// resolveGameOfficial uses tenant-scoped IDs for new assignments and supports names
// only for the legacy input path. The server supplies the canonical name snapshot.
func resolveGameOfficial(ctx context.Context, db *mongo.Database, tenantId string, assignment *model.GameOfficialDoc) error {
	assignment.OfficialId = strings.TrimSpace(assignment.OfficialId)
	assignment.Name = strings.TrimSpace(assignment.Name)
	if assignment.OfficialId == "Unassigned" {
		assignment.OfficialId = ""
	}
	if assignment.OfficialId == "" && (assignment.Name == "" || assignment.Name == "Unassigned") {
		assignment.Name = "Unassigned"
		return nil
	}
	filter := bson.M{"tenantId": tenantId}
	if assignment.OfficialId != "" {
		id, err := strconv.ParseInt(assignment.OfficialId, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid officialId %q for role %q", assignment.OfficialId, assignment.Title)
		}
		filter["$or"] = bson.A{bson.M{"officialId": id}, bson.M{"id": id}}
	} else {
		parts := strings.Fields(assignment.Name)
		if len(parts) < 2 {
			return fmt.Errorf("invalid official name %q for role %q", assignment.Name, assignment.Title)
		}
		filter["firstName"], filter["lastName"] = parts[0], parts[len(parts)-1]
	}
	var official struct {
		OfficialId int64  `bson:"officialId"`
		Id         int64  `bson:"id"`
		FirstName  string `bson:"firstName"`
		LastName   string `bson:"lastName"`
	}
	err := db.Collection("officials").FindOne(ctx, filter,
		options.FindOne().SetCollation(&options.Collation{Locale: "en", Strength: 2})).Decode(&official)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("official not found for role %q", assignment.Title)
	}
	if err != nil {
		return fmt.Errorf("look up official for role %q: %w", assignment.Title, err)
	}
	id := official.OfficialId
	if id == 0 {
		id = official.Id
	}
	if id <= 0 {
		return fmt.Errorf("official for role %q has no valid ID", assignment.Title)
	}
	assignment.OfficialId = strconv.FormatInt(id, 10)
	assignment.Name = strings.TrimSpace(official.FirstName + " " + official.LastName)
	return nil
}

func loadGameRoleDefinitions(ctx context.Context, db *mongo.Database, sport string) ([]model.OfficialRole, error) {
	var doc struct {
		OfficialRoles []model.OfficialRolesRequest `bson:"officialRoles"`
	}
	err := db.Collection("roles").FindOne(ctx,
		bson.M{"officialRoles": bson.M{"$elemMatch": bson.M{"sport": sport}}},
		options.FindOne().SetProjection(bson.M{"officialRoles": bson.M{"$elemMatch": bson.M{"sport": sport}}}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("no official roles configured for sport %q", sport)
	}
	if err != nil {
		return nil, fmt.Errorf("load roles for %q: %w", sport, err)
	}
	if len(doc.OfficialRoles) == 0 {
		return nil, fmt.Errorf("no official roles configured for sport %q", sport)
	}
	return doc.OfficialRoles[0].Roles, nil
}

func prepareGameOfficials(
	ctx context.Context,
	db *mongo.Database,
	doc *model.GameDoc,
	previous *model.GameDoc,
) error {
	roles, err := loadGameRoleDefinitions(ctx, db, doc.Sport)
	if err != nil {
		return err
	}

	existing := make([]model.GameOfficialDoc, 0)

	if previous != nil && previous.Sport == doc.Sport {
		existing = append(existing, previous.Officials...)
	}

	submitted := doc.Officials
	if submitted == nil {
		submitted = append([]model.GameOfficialDoc{}, existing...)
	}

	result, err := normalizeGameRoleAssignments(
		roles,
		submitted,
		existing,
	)
	if err != nil {
		return err
	}

	for i := range result {
		// Preserve unchanged historical snapshots.
		unchanged := false

		for _, old := range existing {
			if old.RoleId == result[i].RoleId &&
				old.OfficialId == result[i].OfficialId &&
				old.Name == result[i].Name {
				unchanged = true
				break
			}
		}

		if unchanged {
			continue
		}

		if err := resolveGameOfficial(
			ctx,
			db,
			doc.TenantId,
			&result[i],
		); err != nil {
			return err
		}
	}

	doc.Officials = result
	return nil
}

// normalizeGameRoleAssignments is pure: validate role membership and snapshots,
// fill missing active slots, and retain disabled/deleted historical assignments.
func normalizeGameRoleAssignments(roles []model.OfficialRole, submitted, existing []model.GameOfficialDoc) ([]model.GameOfficialDoc, error) {
	definitions := make(map[string]model.OfficialRole)
	titles := make(map[string]string)
	oldByRole := make(map[string]model.GameOfficialDoc)
	for _, role := range roles {
		if role.RoleId == "" {
			return nil, fmt.Errorf("configured role %q has no roleId", role.Title)
		}
		if _, duplicate := definitions[role.RoleId]; duplicate {
			return nil, fmt.Errorf("duplicate configured roleId %q", role.RoleId)
		}
		definitions[role.RoleId] = role
		titles[role.Title] = role.RoleId
	}
	for _, old := range existing {
		if old.RoleId != "" {
			oldByRole[old.RoleId] = old
		}
	}
	result := make([]model.GameOfficialDoc, 0)
	seen := make(map[string]bool)
	for _, assignment := range submitted {
		assignment.RoleId = strings.TrimSpace(assignment.RoleId)
		if assignment.RoleId == "" {
			assignment.RoleId = titles[assignment.Title]
		}
		if assignment.RoleId == "" {
			return nil, fmt.Errorf("role %q is not configured for this sport", assignment.Title)
		}
		if seen[assignment.RoleId] {
			return nil, fmt.Errorf("duplicate assignment for roleId %q", assignment.RoleId)
		}
		role, configured := definitions[assignment.RoleId]
		old, existed := oldByRole[assignment.RoleId]
		if !configured || !role.Active {
			if !existed || assignment.OfficialId != old.OfficialId || (assignment.OfficialId == "" && assignment.Name != old.Name) {
				return nil, fmt.Errorf("roleId %q is inactive or does not belong to this sport", assignment.RoleId)
			}
			assignment = old
		} else if existed {
			assignment.Title, assignment.DisplayOrder = old.Title, old.DisplayOrder
			// Client-supplied names are not authoritative when an ID is supplied.
			if assignment.OfficialId != "" && assignment.OfficialId == old.OfficialId {
				assignment.Name = old.Name
			}
		} else {
			assignment.Title, assignment.DisplayOrder = role.Title, role.DisplayOrder
		}
		seen[assignment.RoleId] = true
		result = append(result, assignment)
	}
	for _, role := range roles {
		if role.Active && !seen[role.RoleId] {
			result = append(result, model.GameOfficialDoc{RoleId: role.RoleId, Title: role.Title, DisplayOrder: role.DisplayOrder, Name: "Unassigned"})
			seen[role.RoleId] = true
		}
	}
	for _, old := range existing {
		role, configured := definitions[old.RoleId]
		if old.RoleId != "" && !seen[old.RoleId] && (!configured || !role.Active) {
			result = append(result, old)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].DisplayOrder < result[j].DisplayOrder })
	return result, nil
}

func roleOfficialFilter(title string, values []string) bson.M {
	titles := []string{title}

	if title == "Referee" {
		titles = append(titles, "Plate Umpire")
	}

	return bson.M{
		"officials": bson.M{
			"$elemMatch": bson.M{
				"title": bson.M{"$in": titles},
				"$or": bson.A{
					bson.M{"name": bson.M{"$in": values}},
					bson.M{"officialId": bson.M{"$in": values}},
				},
			},
		},
	}
}
