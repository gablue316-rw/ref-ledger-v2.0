package main

import (
	"context"
	"fmt"
	"net"
	"log"
	"os"
	"io"
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
var logFile = "refLedgerV2_0-webserver.log"

//var tmpl = template.Must(template.ParseFiles("./internal/html/index.html"))

func GetIpAddress(r *http.Request) string {

	//
	// Get Cloudfare Connecting IP Address
	//
	ip := r.Header.Get("CF-Connecting-IP")
	if ip != "" {
		return "CF-Connecting-IP " + ip
	}

	//
	// Get the real IP Address that was proxied 
	//
	realIpAddr := r.Header.Get("X-Forwarded-For")

	if realIpAddr != "" {
		return "X-Forwarded-For " + realIpAddr
	}

	//
	// Get the original IP Address
	//
	originalIpAddr := r.Header.Get("X-Real-IP")

	if originalIpAddr != "" {
		return "X-Real-IP " + originalIpAddr
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return "Host " + host
	}

	return "RemoteAddr " + r.RemoteAddr

}

func OpenLog(f string) *os.File {
	
	// Open or create log file
	file, err := os.OpenFile(
		f,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0666,
	)

	if err != nil {
		log.Fatal(err)
		return nil
	}
	
	// Send logs to file (and optionally terminal)
	log.SetOutput(file)
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetOutput(io.MultiWriter(os.Stdout, file))

   
	return file
}

func LogVisitor(w http.ResponseWriter, r *http.Request) {

	remoteIpAddr := GetIpAddress(r)
	method := r.Method
	path := r.URL.Path
	url  := r.URL.String()
	userAgent := r.UserAgent()
	referer := r.Referer()
	host := r.Host
	protocol := "HTTP"
	if r.TLS != nil {
       protocol = "HTTPS"
	} else {
		protocol = r.Header.Get("X-Forwared-Proto")
	}

	log.Printf("IP=%s Method=%s Path=%s URL=%s Agent=%s Referer=%s Host=%s Protocol=%s",remoteIpAddr, method, path, url, userAgent, referer, host, protocol)

}

func GetGames(w http.ResponseWriter, r *http.Request) {

	fmt.Println("GetGames has been called")

	LogVisitor(w, r)

	var games []model.HtmlResponse
	var gameView []model.GameView
	var gameFilters model.GFilters = model.GFilters{}

	var HtmlAssocGameTotals reports.AssocGameTotalsMap
    HtmlAssocGameTotals.Init()

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

	// 2. Query MongoDB

	cursor, err := coll.Aggregate(context.TODO(), pipeline)
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
	
		gameRec := model.GameDescriptor {
			GameFee:     utils.ConvertInt64ToAmtStr(game.GameFee),
			NumOfGames:  utils.ConvertInt64ToStr(game.NumOfGames),
			TravelPay:   utils.ConvertInt64ToAmtStr(game.TravelPay),
			Deductions:  utils.ConvertInt64ToAmtStr(game.Deductions),
			AssignorFee: utils.ConvertInt64ToAmtStr(game.AssignorFee),
		}

		gameFee := reports.CalculateGameFee(gameRec)
		HtmlAssocGameTotals.Update(game.Association, game.Status, game.NumOfGames, gameFee)	

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

/*
	reptLines := HtmlAssocGameTotals.FormatTotalLine()

	if len(reptLines) > 0 {
		fmt.Println(reptLines)
	}
*/

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

	f := OpenLog (logFile)

	if f != nil {
		fmt.Println("Failed to open",logFile)
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

}
