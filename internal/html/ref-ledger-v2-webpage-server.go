package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"unicode"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"encoding/json"
)

var Client *mongo.Client
var URI string = "mongodb://localhost:27017"

func GetGames(w http.ResponseWriter, r *http.Request) {

	fmt.Println("GetGames has been called")

	var games []model.HtmlResponse
	var gameFilters model.GFilters = model.GFilters{}

	_, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	db := Client.Database(database.Database)
	coll := db.Collection("games")

	// 1. Read query parameters
	status := r.URL.Query().Get("status")
	association := r.URL.Query().Get("association")

	if len(status) > 0 {
		runes := []rune(status)
		runes[0] = unicode.ToUpper(runes[0])
		status = string(runes)
	}

	fmt.Println("status:", status, "association", association)
	gameFilters.Status = status
	gameFilters.Association = association

	fmt.Println("gameFilters.Status", gameFilters.Status)

	gfilter, err := utils.ConvertGameFiltersToJsonFile(gameFilters)
	if err != nil {
		fmt.Println(err)
		return
	}

	mongoDbFilter, err := database.BuildMongoGameFilterFromFile(gfilter)

	fmt.Println("gfilter", gfilter, "mongoDbFilter", mongoDbFilter)

	// 2. Query MongoDB
	cursor, err := coll.Find(context.TODO(), mongoDbFilter)

	if err != nil {
		fmt.Println("find failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer cursor.Close(context.TODO())

	// 3. Decode results

	err = cursor.All(context.TODO(), &games)

	//fmt.Println(games)

	if err != nil {
		fmt.Println("Decoding failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 4. Return JSON
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(games)

}

func main() {

	fmt.Println("Ref Ledger V2.0 Web Page Server Establing database connection...")
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

	err = client.Ping(context.TODO(), nil)
	if err != nil {
		fmt.Println("Failed to verity connection to database.  Terminating web page server.")
		return
	}

	Client = client
	fmt.Println("Connected to MongoDb")

	http.HandleFunc("/api/games", GetGames)
	fs := http.FileServer(http.Dir("./internal/html"))
	http.Handle("/", fs)

	fmt.Println("Server running on port 8080")

	http.ListenAndServe(":8080", nil)
}
