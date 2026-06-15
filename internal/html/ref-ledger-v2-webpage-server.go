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

	"encoding/json"
)

var Client *mongo.Client
var URI string = "mongodb://localhost:27017"
var URI_CONTAINER string = "mongodb://host.docker.internal:27017"

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

type Payment struct {
	PaymentDate string  `json:"date"`
	PaymentId   string  `json:"paymentid"`
	PaymentAmt  float64 `json:"amount"`
	Association string  `json:"association"`
	GameID      []int64 `json:"gameids"`
}

type GameStatusUpdate struct {
	GameIds string `json:"gameIds"`
	Status  string `json:"status"`
}

var ac database.AssociationCollection
var sc database.SiteCollection
var gc database.GameCollection

var AuditLog *log.Logger = nil

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

	if g.Referee == "" {
		g.Referee = "Unassigned"
	}

	if g.U1 == "" {
		g.U1 = "Unassigned"
	}

	if g.U2 == "" {
		g.U2 = "Unassigned"
	}

	if g.ECO == "" {
		g.ECO = "Unassigned"
	}

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

func PaymentDocToPaymentDescr(p Payment) model.PaymentDescriptor {

	t, _ := time.Parse("2006-01-02", p.PaymentDate)
	formattedDate := t.Format("1/2/2006")

	return model.PaymentDescriptor{
		PaymentDate: formattedDate,
		PaymentId:   p.PaymentId,
		PaymentAmt:  strconv.FormatFloat(p.PaymentAmt, 'f', 2, 64),
		Association: p.Association,
		GameIds: strings.Trim(strings.Join(func() []string {
			var gameIds []string
			for _, id := range p.GameID {
				gameIds = append(gameIds, strconv.Itoa(int(id)))
			}
			return gameIds
		}(), ";"), ","),
	}
}

func ExpenseDocToExpenseDescr(e Expense) model.ExpenseDescriptor {

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

func GetAssignorsHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	assignors, err := ac.GetAssignorNames()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assignors)
}

func GetOfficialsHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(w, r)
	officials, err := database.GetOfficialNames()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(officials)
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

	utils.AuditLog.Printf("IP=%s Method=%s Path=%s URL=%s Agent=%s Referer=%s Host=%s Protocol=%s", remoteIpAddr, method, path, url, userAgent, referer, host, protocol)

}

func generatePaymentsReport(assoc string) []string {

	var rept []string = []string{}
	paymentRecords, err := database.QueryPayments(context.TODO(), "refLedger_v2", "payments", assoc)
	if err != nil {
		rept = append(rept, "Error generating payment report.  Failed to retrieve payment records.")
		return rept
	}
	rept = reports.GeneratePaymentReport(paymentRecords)
	return rept
}

func generateReconciliationReport(assoc string) []string {

	var rept []string = []string{}

	paymentRecords, err := database.QueryPayments(context.TODO(), "refLedger_v2", "payments", assoc)
	if err != nil {
		rept = append(rept, "Error generating reconciliation report.  Failed to retrieve payment records.")
		return rept
	}
	rept = reports.GenerateReconciliationReport(paymentRecords)

	return rept
}

func generateAccountsReceivableReport(assoc string) []string {

	var rept []string = []string{}
	rept = reports.GenerateAcctsRecvReport(context.TODO(), assoc)
	return rept
}

func generateIncomeReport(assoc string) []string {

	var rept []string = []string{}
	rept = reports.GenerateIncomeReport(assoc)
	return rept
}

func generateExpenseReport(expenseFilters model.EFilters) []string {

	var rept []string = []string{}

	efilter, err := utils.ConvertExpenseFilterToJsonFile(expenseFilters)
	if err != nil {
		fmt.Println(err)
		return []string{}
	}

	expenseRecords, err := database.QueryExpenses(context.TODO(), "refLedger_v2", "expenses", efilter)
	if err != nil {
		fmt.Println(err)
		return []string{}
	}
	rept = reports.GenerateExpenseReport(expenseRecords)

	return rept
}

func generateGamesReport(gameFilters model.GFilters) []string {
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

	fmt.Println("GenerateReport is called")
	LogVisitor(w, r)
	gameFilters := model.GFilters{}
	expenseFilters := model.EFilters{}
	rType := r.URL.Query().Get("type")
	rEmail := r.URL.Query().Get("emailaddr")
	rFile := r.URL.Query().Get("filename")
	rStatus := r.URL.Query().Get("status")
	rAssoc := r.URL.Query().Get("association")
	rGameIds := r.URL.Query().Get("gameids")

	rept := []string{}

	if len(rGameIds) > 0 {
		ids, err := utils.ConvertGameIdStrToInt(rGameIds)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		gameFilters.GameId, err = utils.ConvertGameIdIntToStr(ids)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
	} else {
		gameFilters.GameId = rGameIds
	}

	gameFilters.Association = rAssoc
	gameFilters.Status = rStatus

	switch rType {
	case "Games":
		rept = generateGamesReport(gameFilters)
	case "Expenses":
		rept = generateExpenseReport(expenseFilters)
	case "Income":
		rept = generateIncomeReport(rAssoc)
	case "Payments":
		rept = generatePaymentsReport(rAssoc)
	case "Reconciliation":
		rept = generateReconciliationReport(rAssoc)
	case "Accounts Receivable":
		rept = generateAccountsReceivableReport(rAssoc)
	default:
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Invalid Report Type")
		return
	}

	if rEmail != "" {
		var email email.Email
		email.Initialize()
		if rFile == "" {
			rFile = rType + "_report.pdf"
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

	LogVisitor(w, r)
	var game Game
	var gameDesc []model.GameDescriptor
	var singleGameDesc model.GameDescriptor = model.GameDescriptor{}

	err := json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	singleGameDesc = GameDocToGameDescr(game)

	if singleGameDesc.Status == "Delete" {
		api.DelGame(context.TODO(), singleGameDesc)
		return
	}

	var gDoc model.GameDoc = model.GameDoc{}

	gDoc.GameId = int64(game.GameId)
	gDoc.Association = game.Association

	err = api.ValidateGameDescriptor(context.TODO(), singleGameDesc)
	if err != nil {
		fmt.Println(err)
		return
	}

	gameExists, err := database.GameExists(gDoc)

	if err != nil {
		fmt.Println(err)
		return
	}

	if gameExists {
		err = database.UpdateOneGameDoc(context.TODO(), singleGameDesc, database.Database, "games")
		if err != nil {
			fmt.Println(err)
			return
		}
		return
	}

	gameDesc = append(gameDesc, singleGameDesc)
	database.InsertGameDocs(context.TODO(), gameDesc, database.Database, "games")

}

func UpdateGameStatus(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
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

func CreatePayment(w http.ResponseWriter, r *http.Request) {

	var payment Payment
	var singlePayment model.PaymentDescriptor
	var paymentDescr []model.PaymentDescriptor

	err := json.NewDecoder(r.Body).Decode(&payment)
	if err != nil {
		fmt.Println("Invalid JSON.  Error:", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	singlePayment = PaymentDocToPaymentDescr(payment)
	paymentDescr = append(paymentDescr, singlePayment)

	//fmt.Println("Payment in json: ", payment)
	fmt.Println("Payment Descr: ", singlePayment)
	//fmt.Println("Payments:", paymentDescr)

	database.InsertPaymentDocs(context.TODO(), paymentDescr, database.Database, "payments")
}

func CreateAssociation(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	var assocJson database.AssociationJson

	err := json.NewDecoder(r.Body).Decode(&assocJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	err = ac.Add(ac.ConvAssocJsonToAssoc(assocJson))
	if err != nil {
		fmt.Println("Failed to create association")
		http.Error(w, "Failed to create association", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Association updated successfully"))
}

func CreateSite(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	var siteJson database.SiteJson

	err := json.NewDecoder(r.Body).Decode(&siteJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	err = sc.Add(sc.ConvJsonToSite(siteJson))
	if err != nil {
		fmt.Println("Failed to create site")
		http.Error(w, "Failed to create site", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Site updated successfully"))
}

func CreateExpense(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)

	var expense Expense
	var singleExpense model.ExpenseDescriptor

	var expDesc []model.ExpenseDescriptor

	err := json.NewDecoder(r.Body).Decode(&expense)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("Expense in json: ", expense)
	singleExpense = ExpenseDocToExpenseDescr(expense)
	singleExpense.ExpenseId = api.GenerateExpenseId(singleExpense)
	fmt.Println("Expense Descr: ", singleExpense)
	expDesc = append(expDesc, singleExpense)

	fmt.Println("Expenses:", expDesc)

	database.InsertExpenseDocs(context.TODO(), expDesc, database.Database, "expenses")

}

func DeleteAssociation(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	fmt.Println("DeleteAssociation called")

	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	assocId := r.PathValue("assocId")

	err := ac.Delete(assocId)

	if err != nil {
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func DeleteGame(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	association := r.PathValue("association")
	gameId := r.PathValue("gameId")

	fmt.Println("Deleting Game", gameId, "for association", association)

	err := gc.Delete(association, gameId)

	if err != nil {
		fmt.Println("Delete failed", err)
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func DeleteSite(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	siteId := r.PathValue("siteId")

	fmt.Println("Deleting Site", siteId)

	err := sc.Delete(siteId)

	if err != nil {
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func GetSingleAssociation(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	assoc, err := ac.Get(r.PathValue("assocId"))
	if err != nil {
		http.Error(w, "Association not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assoc)
}

func GetSingleSite(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	site, err := sc.Get(r.PathValue("siteId"))
	if err != nil {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(site)
}

func GetSingleGame(w http.ResponseWriter, r *http.Request) {

	LogVisitor(w, r)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	association := r.PathValue("association")
	gameID := r.PathValue("gameid")

	game, err := database.GetGameByGameIdAndOrAssoc(association, gameID)
	if err != nil {
		http.Error(w, "Game not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(game)

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

	db := database.Client.Database(database.Database)
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

	if len(gameId) > 0 {
		ids, err := utils.ConvertGameIdStrToInt(gameId)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		gameFilters.GameId, err = utils.ConvertGameIdIntToStr(ids)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
	} else {
		gameFilters.GameId = gameId
	}

	gameFilters.Status = status
	gameFilters.Association = association
	gameFilters.Level = level
	gameFilters.FromDate = bDate
	gameFilters.ToDate = eDate
	gameFilters.Site = site
	gameFilters.Official = official

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

	var err error

	if err = utils.InitLogging(); err != nil {
		panic(err)
	}

	utils.AuditLog.Println("Ref Ledger started")
	fmt.Println("Ref Ledger V2.1 Web Page Server Establing database connection...")
	utils.AuditLog.Println("Ref Ledger V2.1 Web Page Server Establing database connection...")
	database.InitDbase("refLedger_v2", "mongodb://host.docker.internal:27017")

	err = database.Connect()
	if err != nil {
		fmt.Println("Failed to init database.  Terminating web page server.")
		utils.AuditLog.Println("Failed to init database.  Terminating web page server.")
		return
	}

	utils.AuditLog.Println("Database connection established successfully")

	err = ac.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize associations collection.")
		utils.AuditLog.Println("Failed to initialize associations collection.")
		return
	}

	err = sc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize site collection.")
		utils.AuditLog.Println("Failed to initialize site collection.")
		return
	}

	err = gc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize game collection.")
		utils.AuditLog.Println("Failed to initialize game collection.")
		return
	}

	utils.AuditLog.Println("All collections initialized successfully.")

	fmt.Println("Registering routes...")
	utils.AuditLog.Println("Registering routes...")
	mux := http.NewServeMux()

	mux.HandleFunc("/expenses", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/expenses.html")
	})

	mux.HandleFunc("/game-status", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/gameStatus.html")
	})

	mux.HandleFunc("/reports", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/reports.html")
	})

	mux.HandleFunc("/games", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/games.html")
	})

	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/dashboard.html")
	})

	mux.HandleFunc("/payments", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/payments.html")
	})

	mux.HandleFunc("/associations", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/associations.html")
	})

	mux.HandleFunc("/sites", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/sites.html")
	})

	mux.HandleFunc("/api/officials", GetOfficialsHandler)
	mux.HandleFunc("/api/assignors", GetAssignorsHandler)
	mux.HandleFunc("/api/game/{association}/{gameid}", GetSingleGame)
	mux.HandleFunc("/api/association/{assocId}", GetSingleAssociation)
	mux.HandleFunc("/api/deleteAssociation/{assocId}", DeleteAssociation)

	mux.HandleFunc("/api/site/{siteId}", GetSingleSite)
	mux.HandleFunc("/api/deleteSite/{siteId}", DeleteSite)
	mux.HandleFunc("/api/deleteGame/{association}/{gameId}", DeleteGame)

	mux.HandleFunc("/api/expenses", CreateExpense)
	mux.HandleFunc("/api/associations", CreateAssociation)
	mux.HandleFunc("/api/sites", CreateSite)
	mux.HandleFunc("/api/games/status", UpdateGameStatus)
	mux.HandleFunc("/api/reports", GenerateReport)
	mux.HandleFunc("/api/game-update", UpdateGame)
	mux.HandleFunc("/api/dashboard", GetGames)
	mux.HandleFunc("/api/payments", CreatePayment)
	fs := http.FileServer(http.Dir("./internal/html"))
	mux.Handle("/", fs)

	fmt.Println("Routes successfully registered")
	utils.AuditLog.Println("Server running on port 8080")

	/*
		var Association database.Association = database.Association{
			Id:        "MSO",
			Name:      "Multi Spors Officials",
			Contact:   "Scott Henry",
			Phone:     "(678) 778-4546",
			Email:     "test@example.com",
			Assignors: []string{"Scott Henry", "Euvonda Harrison", "Barry Sullivan"},
		}

		err = associations.Add(Association)
		if err != nil {
			fmt.Println(err)
		}

		Association.Email = "scott.henry@example.com"
		err = associations.Update("MSO", Association)
		if err != nil {
			fmt.Println(err)
		}

		associations.Dump("MSO")

		err = associations.Delete("MSO")
		if err != nil {
			fmt.Println(err)
		}
	*/

	err = http.ListenAndServe(":8080", mux)

	if err != nil {
		fmt.Println("HTTP Error", err)
		return
	}

}
