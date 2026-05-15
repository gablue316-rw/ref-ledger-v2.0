package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"unicode"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/reports"
	"ref-ledger-v2/internal/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"encoding/json"
)

var Client *mongo.Client
var URI string = "mongodb://localhost:27017"

//var tmpl = template.Must(template.ParseFiles("./internal/html/index.html"))

func GetGames(w http.ResponseWriter, r *http.Request) {

	fmt.Println("GetGames has been called")

	var games []model.HtmlResponse
	var gameView []model.GameView
	var gameFilters model.GFilters = model.GFilters{}

	_, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(database.Database)
	coll := db.Collection("games")

	// 1. Read query parameters
	status := r.URL.Query().Get("status")
	association := r.URL.Query().Get("association")
	begindate := r.URL.Query().Get("begindate")
	enddate := r.URL.Query().Get("enddate")
	level := r.URL.Query().Get("level")
	gameId := r.URL.Query().Get("gameId")
	site := r.URL.Query().Get("site")
	official := r.URL.Query().Get("official")

	if len(status) > 0 {
		runes := []rune(status)
		runes[0] = unicode.ToUpper(runes[0])
		status = string(runes)
	}

	var bDate string = ""
	var eDate string = ""
	var err error

	if len(begindate) > 0 {
		bDate, _, err = utils.FormatDateFilter(begindate, "")
		if err != nil {
			fmt.Println(err)
		}
	}

	if len(enddate) > 0 {
		_, eDate, err = utils.FormatDateFilter("", enddate)
		if err != nil {
			fmt.Println(err)
		}
	}
	fmt.Println("status:", status, "association", association, "bDate", bDate, "eDate", eDate)
	gameFilters.Status = status
	gameFilters.Association = association
	gameFilters.Level = level
	gameFilters.FromDate = bDate
	gameFilters.GameId = gameId
	gameFilters.ToDate = eDate
	gameFilters.Site = site
	gameFilters.Official = official

	fmt.Println("gameFilters.Status", gameFilters.Status)

	gfilter, err := utils.ConvertGameFiltersToJsonFile(gameFilters)
	if err != nil {
		fmt.Println(err)
		return
	}

	mongoDbFilter, err := database.BuildMongoGameFilterFromFile(gfilter)

	if err != nil {
		fmt.Println("FILTER BUILD ERROR", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	cursor, err := coll.Aggregate(context.TODO(), pipeline)

	//fmt.Println("gfilter", gfilter, "mongoDbFilter", mongoDbFilter)

	// 2. Query MongoDB
	//cursor, err := coll.Find(context.TODO(), mongoDbFilter)

	if err != nil {
		fmt.Println("MONGO FIND ERROR:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer cursor.Close(context.TODO())

	// 3. Decode results

	err = cursor.All(context.TODO(), &games)

	if err != nil {
		fmt.Println("Decoding failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, game := range games {

		view := model.GameView{
			GameId:      game.GameId,
			Time:        game.Time,
			Sport:       game.Sport,
			Site:        game.Site,
			Field:       game.Field,
			NumOfGames:  game.NumOfGames,
			Level:       game.Level,
			Status:      game.Status,
			Association: game.Association,
		}

		view.GameFee = fmt.Sprintf("$%.2f", float64(game.GameFee)/100)

		abbrev := utils.DayOfWeekAbbreviation(game.Date)
		view.Date = fmt.Sprintf("%s (%s)", game.Date, abbrev)

		view.Officials = reports.FormatOfficialString(game.Referee, game.U1, game.U2)
		gameView = append(gameView, view)
	}

	// 4. Return JSON
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(gameView)

}

func main() {

	fmt.Println("Ref Ledger V2.1 Web Page Server Establing database connection...")
	database.InitDbase("refLedger_v2", "mongodb://localhost:27017")

	err := database.Connect()
	if err != nil {
		fmt.Println("Failed to init database.  Terminating web page server.")
		return
	}

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(URI))

	if err != nil {
		fmt.Println("Failed to connect to database.  Terminating web page server.")
		return
	}

	Client = client

	fmt.Println("Registering routes...")
	mux := http.NewServeMux()

	mux.HandleFunc("/api/games", GetGames)
	fs := http.FileServer(http.Dir("./internal/html"))
	mux.Handle("/", fs)

	fmt.Println("Routes successfully registered")
	fmt.Println("Server running on port 8080")
	err = http.ListenAndServe(":8080", mux)

	if err != nil {
		fmt.Println("HTTP Error", err)
		return
	}

	/*
		http.HandleFunc("/api/games", GetGames)
		fs := http.FileServer(http.Dir("./internal/html"))
		http.Handle("/", fs)



		err = http.ListenAndServe(":8080", nil)
		if err != nil {
			fmt.Println("HTTP Error:", err)
		}
	*/

}
