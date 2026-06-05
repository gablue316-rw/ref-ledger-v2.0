package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"unicode"

	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/email"
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
var URI_CONTAINER string = "mongodb://host.docker.internal:27017"
var logFile = "refLedgerV2_0-webserver.log"

type Game struct {
	Association string  `json:"association"`
	GameId      int     `json:"gameId"`
	Date        string  `json:"date"`
	Time        string  `json:"time"`
	Site        string  `json:"site"`
	Field       string  `json:"field"`
	Sport       string  `json:"sport"`
	Level       string  `json:"level"`
	NumOfGames  int     `json:"numOfGames"`
	GameFee     float64 `json:"gameFee"`
	TravelPay   float64 `json:"travelPay"`
	AssignorFee float64 `json:"assignorFee"`
	Deductions  float64 `json:"deductions"`
	Status      string  `json:"status"`
	Referee     string  `json:"referee"`
	U1          string  `json:"u1"`
	U2          string  `json:"u2"`
	ECO         string  `json:"eco"`
	Assignor    string  `json:"assignor"`
}

type Expense struct {
	Date        string  `json:"date"`
	ExpenseType string  `json:"expenseType"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
	Association string  `json:"association"`
	GameID      int     `json:"gameId"`
}

type GameStatusUpdate struct {
	GameIds string `json:"gameIds"`
	Status  string `json:"status"`
}

func GameDocToGameDescr(g Game) model.GameDescriptor {

	t, err := time.Parse("2006-01-02", g.Date)
	if err != nil {
		log.Println("Error parsing date:", err)
	}
	formattedDate := t.Format("1/2/2006")

	t, err = time.Parse("15:04", g.Time)
	if err != nil {
		log.Println("Error parsing time:", err)
	}
	formattedTime := t.Format("3:04 PM")

	return model.GameDescriptor{
		Association: g.Association,
		GameId:      strconv.Itoa(g.GameId),
		Date:        formattedDate,
		Time:        formattedTime,
		Site:        g.Site,
		Field:       g.Field,
		Sport:       g.Sport,
		Level:       g.Level,
		NumOfGames:  strconv.Itoa(g.NumOfGames),
		GameFee:     strconv.FormatFloat(g.GameFee, 'f', 2, 64),
		TravelPay:   strconv.FormatFloat(g.TravelPay, 'f', 2, 64),
		AssignorFee: strconv.FormatFloat(g.AssignorFee, 'f', 2, 64),
		Deductions:  strconv.FormatFloat(g.Deductions, 'f', 2, 64),
		Status:      g.Status,
		Referee:     g.Referee,
		U1:          g.U1,
		U2:          g.U2,
		ECO:         g.ECO,
		Assignor:    g.Assignor,
	}
}

func ExpenseDocToExpenseDesr(e Expense) model.ExpenseDescriptor {

	t, _ := time.Parse("2006-01-02", e.Date)
	formattedDate := t.Format("1/2/2006")

	return model.ExpenseDescriptor{
		Date:        formattedDate,
		Type:        e.ExpenseType,
		Amount:      strconv.FormatFloat(e.Amount, 'f', 2, 64),
		Association: e.Association,
		GameId:      strconv.Itoa(e.GameID),
		Description: e.Description,
	}

}

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
	url := r.URL.String()
	userAgent := r.UserAgent()
	referer := r.Referer()
	host := r.Host
	protocol := "HTTP"
	if r.TLS != nil {
		protocol = "HTTPS"
	} else {
		protocol = r.Header.Get("X-Forwared-Proto")
	}

	log.Printf("IP=%s Method=%s Path=%s URL=%s Agent=%s Referer=%s Host=%s Protocol=%s", remoteIpAddr, method, path, url, userAgent, referer, host, protocol)

}

func generateGamesReport(w http.ResponseWriter, gameFilters model.GFilters) []string {
	// Implementation for generating games report

	gFilter, err := utils.ConvertGameFiltersToJsonFile(gameFilters)
	if err != nil {
		fmt.Println(err)
		return []string{}
	}

	gameRecords, err := database.QueryAggregatedGames(context.TODO(), "refLedger_v2", "games", gFilter)
	if err != nil {
		fmt.Println("Failed to query aggregated games")
		return []string{}
	}
	rept := reports.GenerateGameReport(gameRecords)

	return rept

}

func GenerateReport(w http.ResponseWriter, r *http.Request) {

	gameFilters := model.GFilters{}
	rType := r.URL.Query().Get("type")
	rEmail := r.URL.Query().Get("email")
	rFile := r.URL.Query().Get("file")
	rept := []string{}

	association := r.URL.Query().Get("association")

	gameFilters.Association = association

	if rType == "Games" {
		rept = generateGamesReport(w, gameFilters)
	} else {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Not Implemented Yet")
		return
	}

	if rEmail != "" {
		var email email.Email
		email.Initialize()
		if rFile == "" {
			rFile = rType + "_report.txt"
		}
		reports.WriteReportToFile(rept, rFile)
		// Send report via email
		email.SetSubject("Ref Ledger V2 Report")
		email.SetBody("Please see the attached report.\n\nThanks!\n\nGenerated and Sent by Ref Ledger V2.0")
		email.AddAttachment(rFile)
		email.SetTo(strings.Split(rEmail, ","))
		email.Send()
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Email sent")
	} else {
		w.Header().Set("Content-Type", "text/plain")
		output := strings.Join(rept, "\n")

		_, err := w.Write([]byte(output))
		if err != nil {
			fmt.Println(err)
			fmt.Fprint(w, "Error generating report")
		}
	}

}

func UpdateGame(w http.ResponseWriter, r *http.Request) {

	var game Game
	var gameDesc []model.GameDescriptor
	var singGameDesc model.GameDescriptor = model.GameDescriptor{}

	err := json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	singGameDesc = GameDocToGameDescr(game)

	if singGameDesc.Status == "Delete" {
		api.DelGame(context.TODO(), singGameDesc)
		return
	}

	err = api.ValidateGameDescriptor(context.TODO(), singGameDesc)
	if err != nil {
		fmt.Println(err)
		return
	}

	gameDesc = append(gameDesc, singGameDesc)
	database.InsertGameDocs(context.TODO(), gameDesc, database.Database, "games")

}

func UpdateGameStatus(w http.ResponseWriter, r *http.Request) {

	var gameUpdate GameStatusUpdate
	var gameIds []int64 = []int64{}

	err := json.NewDecoder(r.Body).Decode(&gameUpdate)
	if err != nil {
		fmt.Println("err:", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	gameIds, err = utils.ConvertGameIdStrToInt(gameUpdate.GameIds)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	database.UpdateGameStatus(gameIds, gameUpdate.Status)

}

func CreateExpense(w http.ResponseWriter, r *http.Request) {

	var expense Expense
	var singleExpense model.ExpenseDescriptor

	var expDesc []model.ExpenseDescriptor

	err := json.NewDecoder(r.Body).Decode(&expense)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("Expense in json: ", expense)
	singleExpense = ExpenseDocToExpenseDesr(expense)
	singleExpense.ExpenseId = api.GenerateExpenseId(singleExpense)
	fmt.Println("Expense Descr: ", singleExpense)
	expDesc = append(expDesc, singleExpense)

	fmt.Println("Expenses:", expDesc)

	database.InsertExpenseDocs(context.TODO(), expDesc, database.Database, "expenses")

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

	bDate, eDate, err = utils.FormatDateFilter(begindate, enddate)
	if err != nil {
		fmt.Println(err)
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

		gameRec := model.GameDescriptor{
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
	//database.InitDbase("refLedger_v2", "mongodb://localhost:27017")
	database.InitDbase("refLedger_v2", "mongodb://host.docker.internal:27017")

	err := database.Connect()
	if err != nil {
		fmt.Println("Failed to init database.  Terminating web page server.")
		return
	}

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(URI_CONTAINER))

	if err != nil {
		fmt.Println("Failed to connect to database.  Terminating web page server.")
		return
	}

	f := OpenLog(logFile)

	if f != nil {
		fmt.Println("Failed to open", logFile)
	}

	Client = client

	fmt.Println("Registering routes...")
	mux := http.NewServeMux()

	mux.HandleFunc("/api/games", GetGames)
	mux.HandleFunc("/expenses", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/expenses.html")
	})

	mux.HandleFunc("/game-status", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/gameStatus.html")
	})

	mux.HandleFunc("/reports", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/reports.html")
	})

	mux.HandleFunc("/game-update", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/update_game.html")
	})

	mux.HandleFunc("/api/expenses", CreateExpense)
	mux.HandleFunc("/api/games/status", UpdateGameStatus)
	mux.HandleFunc("/api/reports", GenerateReport)
	mux.HandleFunc("/api/game-update", UpdateGame)
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
