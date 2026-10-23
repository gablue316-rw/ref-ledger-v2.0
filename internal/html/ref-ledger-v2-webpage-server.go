package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"

	"crypto/rand"
	"encoding/hex"
	"unicode/utf8"

	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"regexp"
	"unicode"

	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/handlers"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/reports"
	"ref-ledger-v2/internal/utils"

	"encoding/json"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

/* Check environment_variables.txt for the environment variables that must be set for Ref Ledger to run properly. */

var Client *mongo.Client
var uri string
var dbName string

type Game struct {
	Association string                  `json:"association"`
	GameId      int                     `json:"gameId"`
	Date        string                  `json:"date"`
	Time        string                  `json:"time"`
	Site        string                  `json:"site"`
	Field       string                  `json:"field"`
	Sport       string                  `json:"sport"`
	Level       string                  `json:"level"`
	Home        string                  `json:"home"`
	Visitor     string                  `json:"visitor"`
	NumOfGames  int                     `json:"numOfGames"`
	GameFee     float64                 `json:"gameFee"`
	TravelPay   float64                 `json:"travelPay"`
	AssignorFee float64                 `json:"assignorFee"`
	Deductions  float64                 `json:"deductions"`
	Status      string                  `json:"status"`
	Assignor    string                  `json:"assignor"`
	Officials   []model.GameOfficialDoc `json:"officials"`
	TaxYear     string                  `json:"taxYear"`
	Notes       string                  `json:"notes"`
}

type Expense struct {
	Date        string  `json:"date"`
	ExpenseType string  `json:"expenseType"`
	Amount      float64 `json:"amount"`
	Mileage     float64 `json:"mileage"`
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
	TaxYear     int     `json:"taxYear"`
}

type GameStatusUpdate struct {
	GameIds json.RawMessage `json:"gameIds"`
	Status  string          `json:"status"`
}

var ac database.AssociationCollection
var sc database.SiteCollection
var gc database.GameCollection
var oc database.OfficialCollection
var ec database.ExpensesCollection
var se database.SessionsCollection
var uc database.UsersCollection
var lc database.LevelsCollection
var spc database.SportsCollection

var AuditLog *log.Logger = nil

func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func getTenantId(r *http.Request) (string, error) {
	cookie, err := r.Cookie("rl_session")
	if err != nil {
		return "", fmt.Errorf("retrieving session cookie: %w", err)
	}

	sessionID := strings.TrimSpace(cookie.Value)
	if sessionID == "" {
		return "", fmt.Errorf("session ID is empty")
	}

	tenantID, err := se.GetTenantID(sessionID)
	if err != nil {
		return "", fmt.Errorf("retrieving tenant ID from session: %w", err)
	}

	if strings.TrimSpace(tenantID) == "" || tenantID == "na" {
		return "", fmt.Errorf("invalid tenant ID in session")
	}

	return tenantID, nil
}

func PodInfoHandler(w http.ResponseWriter, r *http.Request) {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "local-dev"
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"podName":"%s"}`, podName)
}

func LogRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		podName := os.Getenv("POD_NAME")
		if podName == "" {
			podName = "local-dev"
		}

		logPrint := fmt.Sprintf("Pod=%s Method=%s Path=%s RemoteAddr=%s", podName, r.Method, r.URL.Path, r.RemoteAddr)
		log.Println(logPrint)
		fmt.Println(logPrint)

		next.ServeHTTP(w, r)
	})
}

// adminRequired uses the signed-in session, never a client-supplied role.
// Keep authRequired outside this wrapper to preserve login redirects for pages.
func adminRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		session, err := database.GetSession(r)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if session.Role != "admin" {
			http.Error(w, "Administrator access required", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// Catalog reads remain available to authenticated users of other pages.
// The enclosing route must use authRequired.
func sportsAccess(next http.HandlerFunc) http.HandlerFunc {
	restricted := adminRequired(next)
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			next(w, r)
		case http.MethodPost, http.MethodDelete:
			restricted(w, r)
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func officialRolesAccess(next http.HandlerFunc) http.HandlerFunc {
	restricted := adminRequired(next)
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			next(w, r)
		case http.MethodPut, http.MethodDelete:
			restricted(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func readOnlyForbidden(next http.HandlerFunc) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		session, err := database.GetSession(r)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if session.Role == "readonly" {
			http.Error(w, "Permission denied", http.StatusForbidden)
			return
		}

		next(w, r)
	}
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

	var officials []model.GameOfficialDoc
	if g.Officials != nil {
		officials = make([]model.GameOfficialDoc, len(g.Officials))
		copy(officials, g.Officials)
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
		Home:        g.Home,
		Visitor:     g.Visitor,
		NumOfGames:  strconv.Itoa(g.NumOfGames),
		GameFee:     strconv.FormatFloat(g.GameFee, 'f', 2, 64),
		TravelPay:   strconv.FormatFloat(g.TravelPay, 'f', 2, 64),
		AssignorFee: strconv.FormatFloat(g.AssignorFee, 'f', 2, 64),
		Deductions:  strconv.FormatFloat(g.Deductions, 'f', 2, 64),
		Status:      g.Status,
		Assignor:    g.Assignor,
		Officials:   officials,
		TaxYear:     g.TaxYear,
		Notes:       g.Notes,
	}
}

func PaymentDocToPaymentDescr(p Payment) model.PaymentDescriptor {
	t, err := time.Parse("2006-01-02", p.PaymentDate)
	if err != nil {
		fmt.Println("Invalid payment date:", err)
		return model.PaymentDescriptor{}
	}

	taxYear := p.TaxYear
	if taxYear == 0 {
		taxYear = t.Year()
	}

	gameIds := make([]string, 0, len(p.GameID))
	for _, id := range p.GameID {
		gameIds = append(gameIds, strconv.FormatInt(int64(id), 10))
	}

	return model.PaymentDescriptor{
		PaymentDate: t.Format("1/2/2006"),
		PaymentId:   p.PaymentId,
		PaymentAmt:  strconv.FormatFloat(p.PaymentAmt, 'f', 2, 64),
		Association: p.Association,
		TaxYear:     strconv.Itoa(taxYear),
		GameIds:     strings.Join(gameIds, ";"),
	}
}

func ExpenseDocToExpenseDescr(e Expense) model.ExpenseDescriptor {

	t, _ := time.Parse("2006-01-02", e.Date)
	formattedDate := t.Format("1/2/2006")

	return model.ExpenseDescriptor{
		Date:        formattedDate,
		Type:        e.ExpenseType,
		Amount:      strconv.FormatFloat(e.Amount, 'f', 2, 64),
		Mileage:     strconv.FormatFloat(e.Mileage/100, 'f', 0, 64),
		Association: e.Association,
		GameId:      strconv.Itoa(e.GameID),
		Description: e.Description,
	}

}

type UserSessionResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Name     string `json:"name"`
}

func getCurrentSession(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	fmt.Println("getCurrentSession")

	session, err := database.GetSession(r)

	if err != nil {
		http.Error(w, "Session not found", http.StatusUnauthorized)
		return
	}

	name, err := uc.GetName(session.TenantID, session.Username)

	if err != nil {
		http.Error(w, "User  not found", http.StatusUnauthorized)
		return
	}

	response := UserSessionResponse{
		Username: session.Username,
		Role:     session.Role,
		Name:     name,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

}

type AboutInfo struct {
	Application string `json:"application"`
	Release     string `json:"release"`
}

func AboutHandler(w http.ResponseWriter, r *http.Request) {
	release := os.Getenv("APP_RELEASE")

	if release == "" {
		release = "development"
	}

	info := AboutInfo{
		Application: "Ref Ledger",
		Release:     release,
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(info); err != nil {
		http.Error(
			w,
			"Unable to create about response",
			http.StatusInternalServerError,
		)
	}
}

// Serve this file only through the protected /administration route.
func AdministrationPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/administration" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.ServeFile(w, r, "./internal/html/administration.html")
}

func SettingsPage(w http.ResponseWriter, r *http.Request) {
	fmt.Println("SettingsPage called:", r.URL.Path)
	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	http.ServeFile(
		w,
		r,
		"./internal/html/settings.html",
	)
}

func GetEnvironmentHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	environmentInfo := database.GetEnvironment()

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(environmentInfo); err != nil {
		http.Error(
			w,
			"Unable to return environment information",
			http.StatusInternalServerError,
		)
	}
}

func GetAssignorsHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	association := strings.TrimSpace(r.URL.Query().Get("association"))

	assignors, err := ac.GetAssignorNames(tId, association)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assignors)
}

func GetSitesDirectoryHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}
	siteName := strings.TrimSpace(r.URL.Query().Get("siteName"))

	sites, err := sc.GetSitesDirectory(tId, siteName)
	if err != nil {
		fmt.Println("GetSitesDirectory error:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(sites); err != nil {
		fmt.Println("Failed to encode sites:", err)
		http.Error(w, "Unable to encode sites", http.StatusInternalServerError)
	}
}

func GetAssociationsDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}
	associations, err := ac.GetAssociationsDirectory(tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(associations)
}

func GetOfficialsDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}
	firstName := strings.TrimSpace(r.URL.Query().Get("firstname"))
	lastName := strings.TrimSpace(r.URL.Query().Get("lastname"))

	officials, err := oc.GetOfficialsDirectory(firstName, lastName, tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(officials)
}

func GetAssociationsHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	associations, err := ac.GetAssociationIds(tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(associations)
}

func GetLevelsHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("Request path:", r.URL.Path)
	fmt.Println("TenantId:", tId)

	for _, c := range r.Cookies() {
		fmt.Println("Cookie:", c.Name, c.Value)
	}

	/*
		/api/levels returns every level.
		/api/levels?active=true returns only active levels.
	*/
	activeOnly := r.URL.Query().Get("active") == "true"

	levels, err := lc.GetLevels(tId, activeOnly, strings.TrimSpace(r.URL.Query().Get("sport")))
	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println(
		"Number of levels returned",
		len(levels),
		"for tenantId",
		tId,
		"activeOnly",
		activeOnly,
	)

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(levels); err != nil {
		fmt.Println("Unable to encode levels:", err)
	}
}

func GetSportsHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	tId, err := getTenantId(r)

	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("Request path:", r.URL.Path)
	fmt.Println("TenantId:", tId)

	for _, c := range r.Cookies() {
		fmt.Println("Cookie:", c.Name, c.Value)
	}

	/*
		/api/sports returns every sports.
		/api/sports?active=true returns only active sports.
	*/
	activeOnly := r.URL.Query().Get("active") == "true"

	sports, err := spc.GetSports(activeOnly)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Println(
		"Number of sports returned",
		len(sports),
		"for tenantId",
		tId,
		"activeOnly",
		activeOnly,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sports)
}

func GetSitesHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	fmt.Println("Request path:", r.URL.Path)
	fmt.Println("TenantId:", tId)

	for _, c := range r.Cookies() {
		fmt.Println("Cookie:", c.Name, c.Value)
	}

	sites, err := sc.GetSiteNames(tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

func ImportOfficialsPageHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	http.ServeFile(
		w,
		r,
		"./internal/html/importOfficials.html",
	)
}

func ImportAssociationsPageHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	http.ServeFile(
		w,
		r,
		"./internal/html/importAssociations.html",
	)
}

func ImportSitesPageHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	http.ServeFile(
		w,
		r,
		"./internal/html/importSites.html",
	)
}

func ImportGamesPageHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	http.ServeFile(
		w,
		r,
		"./internal/html/importGames.html",
	)
}

func DownloadOfficialsTemplateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Tell the browser this response is a CSV file download.
	w.Header().Set(
		"Content-Type",
		"text/csv; charset=utf-8",
	)

	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="officials-import-template.csv"`,
	)

	// Prevent the browser from caching an old template.
	w.Header().Set(
		"Cache-Control",
		"no-store",
	)

	csvWriter := csv.NewWriter(w)

	// Flush writes any buffered CSV data to the HTTP response.
	defer csvWriter.Flush()

	headers := []string{
		"firstName",
		"lastName",
		"phone",
		"email",
		"address",
	}

	if err := csvWriter.Write(headers); err != nil {
		log.Printf(
			"DownloadOfficialsTemplateHandler: unable to write CSV headers: %v",
			err,
		)

		return
	}

	/*
		Optional example row.

		This also demonstrates how encoding/csv automatically handles
		an address containing a comma by surrounding the field with quotes.

		Remove this section if you want the downloaded template to contain
		only the header row.
	*/
	exampleRow := []string{
		"John",
		"Smith",
		"404-555-1212",
		"john.smith@example.com",
		"123 Main Street, Suite 200",
	}

	if err := csvWriter.Write(exampleRow); err != nil {
		log.Printf(
			"DownloadOfficialsTemplateHandler: unable to write example row: %v",
			err,
		)

		return
	}
}

func DownloadAssociationsTemplateHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Tell the browser this response is a CSV file download.
	w.Header().Set(
		"Content-Type",
		"text/csv; charset=utf-8",
	)

	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="associations-import-template.csv"`,
	)

	// Prevent the browser from caching an old template.
	w.Header().Set(
		"Cache-Control",
		"no-store",
	)

	csvWriter := csv.NewWriter(w)

	// Flush writes any buffered CSV data to the HTTP response.
	defer csvWriter.Flush()

	headers := []string{
		"associationId",
		"associationName",
		"contactName",
		"contactNumber",
		"contactEmail",
		"assignors(comma-separated)",
	}

	if err := csvWriter.Write(headers); err != nil {
		log.Printf(
			"DownloadAssociationsTemplateHandler: unable to write CSV headers: %v",
			err,
		)

		return
	}

	/*
		Optional example row.

		This also demonstrates how encoding/csv automatically handles
		an address containing a comma by surrounding the field with quotes.

		Remove this section if you want the downloaded template to contain
		only the header row.
	*/
	exampleRow := []string{
		"MCBOA",
		"Multi County Basketball Officials Association",
		"John Smith",
		"404-555-1212",
		"john.smith@example.com",
		"John Smith, Jane Doe",
	}

	if err := csvWriter.Write(exampleRow); err != nil {
		log.Printf(
			"DownloadAssociationsTemplateHandler: unable to write example row: %v",
			err,
		)

		return
	}
}

func DownloadGamesTemplateHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Tell the browser this response is a CSV file download.
	w.Header().Set(
		"Content-Type",
		"text/csv; charset=utf-8",
	)

	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="games-import-template.csv"`,
	)

	// Prevent the browser from caching an old template.
	w.Header().Set(
		"Cache-Control",
		"no-store",
	)

	csvWriter := csv.NewWriter(w)

	// Flush writes any buffered CSV data to the HTTP response.
	defer csvWriter.Flush()

	headers := []string{
		"gameId",
		"date",
		"taxyear",
		"time",
		"sport",
		"site",
		"field",
		"numOfGames",
		"level",
		"gameFee",
		"travelPay",
		"assignorFee",
		"deductions",
		"association",
		"status",
		"official1",
		"official2",
		"official3",
		"official4",
		"official5",
		"official6",
		"official7",
		"official8",
		"official9",
		"official10",
		"assignor",
		"home",
		"visitor",
		"notes",
	}

	if err := csvWriter.Write(headers); err != nil {
		log.Printf(
			"DownloadGamesTemplateHandler: unable to write CSV headers: %v",
			err,
		)

		return
	}

	/*
		Optional example row.

		This also demonstrates how encoding/csv automatically handles
		an address containing a comma by surrounding the field with quotes.

		Remove this section if you want the downloaded template to contain
		only the header row.
	*/

	exampleRow := []string{
		"777",
		"10/1/2026",
		"2026",
		"7:00 PM",
		"Softball",
		"Mill Creek High School",
		"Softball Field",
		"1",
		"Varsity",
		"$50.00",
		"$25.00",
		"$10.00",
		"$5.00",
		"MSO",
		"Pending",
		"John Doe",
		"Jane Smith",
		"Bob Johnson",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"Charlie Davis",
		"Mill Creek",
		"Buford",
		"Game Suspended due to Rain",
	}

	if err := csvWriter.Write(exampleRow); err != nil {
		log.Printf(
			"DownloadGamesTemplateHandler: unable to write example row: %v",
			err,
		)

		return
	}

}

func DownloadSitesTemplateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Tell the browser this response is a CSV file download.
	w.Header().Set(
		"Content-Type",
		"text/csv; charset=utf-8",
	)

	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="sites-import-template.csv"`,
	)

	// Prevent the browser from caching an old template.
	w.Header().Set(
		"Cache-Control",
		"no-store",
	)

	csvWriter := csv.NewWriter(w)

	// Flush writes any buffered CSV data to the HTTP response.
	defer csvWriter.Flush()

	headers := []string{
		"siteId",
		"siteName",
		"contactName",
		"contactNumber",
		"contactEmail",
	}

	if err := csvWriter.Write(headers); err != nil {
		log.Printf(
			"DownloadSitesTemplateHandler: unable to write CSV headers: %v",
			err,
		)

		return
	}

	/*
		Optional example row.

		This also demonstrates how encoding/csv automatically handles
		an address containing a comma by surrounding the field with quotes.

		Remove this section if you want the downloaded template to contain
		only the header row.
	*/
	exampleRow := []string{
		"ALBI",
		"Al Bishop Softball Complex",
		"John Smith",
		"404-555-1212",
		"john.smith@example.com",
	}

	if err := csvWriter.Write(exampleRow); err != nil {
		log.Printf(
			"DownloadSitesTemplateHandler: unable to write example row: %v",
			err,
		)

		return
	}
}

const oneMB = 1048576
const fiveMB = 5 * oneMB
const maxCSVSize = fiveMB
const maxCSVRows = 1000
const previewExpiration = 15 * time.Minute

type GameImportData struct {
	GameId      int64                   `json:"gameId"`
	Date        string                  `json:"date"`
	TaxYear     string                  `json:"taxYear"`
	Time        string                  `json:"time"`
	Sport       string                  `json:"sport"`
	Site        string                  `json:"site"`
	Field       string                  `json:"field"`
	NumOfGames  int64                   `json:"numOfGames"`
	Level       string                  `json:"level"`
	Home        string                  `json:"home"`
	Visitor     string                  `json:"visitor"`
	GameFee     string                  `json:"gameFee"`
	TravelPay   string                  `json:"travelPay"`
	AssignorFee string                  `json:"assignorFee"`
	Deductions  string                  `json:"deductions"`
	Association string                  `json:"association"`
	Status      string                  `json:"status"`
	Officials   []model.GameOfficialDoc `json:"officials"`
	Assignor    string                  `json:"assignor"`
	Notes       string                  `json:"notes"`
}

type GamePreviewRow struct {
	RowNumber int            `json:"rowNumber"`
	Valid     bool           `json:"valid"`
	Data      GameImportData `json:"data"`
	Errors    []string       `json:"errors"`
}

type GamesImportPreview struct {
	Token     string
	TenantID  string
	Rows      []GamePreviewRow
	CreatedAt time.Time
	ExpiresAt time.Time
}

type GamesPreviewResponse struct {
	PreviewToken string           `json:"previewToken"`
	TotalRows    int              `json:"totalRows"`
	ValidRows    int              `json:"validRows"`
	InvalidRows  int              `json:"invalidRows"`
	Rows         []GamePreviewRow `json:"rows"`
}

type GamesCommitRowResult struct {
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type GamesCommitResponse struct {
	Added   int                    `json:"added"`
	Skipped int                    `json:"skipped"`
	Failed  int                    `json:"failed"`
	Message string                 `json:"message"`
	Rows    []GamesCommitRowResult `json:"rows,omitempty"`
}

type OfficialImportData struct {
	FirstName string `json:"firstName" bson:"firstName"`
	LastName  string `json:"lastName" bson:"lastName"`
	Phone     string `json:"phone" bson:"phone"`
	Email     string `json:"email" bson:"email"`
	Address   string `json:"address" bson:"address"`
}

type OfficialPreviewRow struct {
	RowNumber int                `json:"rowNumber"`
	Valid     bool               `json:"valid"`
	Data      OfficialImportData `json:"data"`
	Errors    []string           `json:"errors"`
}

type OfficialsImportPreview struct {
	Token     string
	TenantID  string
	Rows      []OfficialPreviewRow
	CreatedAt time.Time
	ExpiresAt time.Time
}

type OfficialsPreviewResponse struct {
	PreviewToken string               `json:"previewToken"`
	TotalRows    int                  `json:"totalRows"`
	ValidRows    int                  `json:"validRows"`
	InvalidRows  int                  `json:"invalidRows"`
	Rows         []OfficialPreviewRow `json:"rows"`
}

type OfficialsCommitRowResult struct {
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type OfficialsCommitResponse struct {
	Added   int                        `json:"added"`
	Skipped int                        `json:"skipped"`
	Failed  int                        `json:"failed"`
	Message string                     `json:"message"`
	Rows    []OfficialsCommitRowResult `json:"rows,omitempty"`
}

type AssociationImportData struct {
	AssociationId   string `json:"associationId" bson:"associationId"`
	AssociationName string `json:"associationName" bson:"associationName"`
	ContactName     string `json:"contactName" bson:"contactName"`
	ContactNumber   string `json:"contactNumber" bson:"contactNumber"`
	ContactEmail    string `json:"contactEmail" bson:"contactEmail"`
	Assignors       string `json:"assignors" bson:"assignors"`
}

type AssociationPreviewRow struct {
	RowNumber int                   `json:"rowNumber"`
	Valid     bool                  `json:"valid"`
	Data      AssociationImportData `json:"data"`
	Errors    []string              `json:"errors"`
}

type AssociationsImportPreview struct {
	Token     string
	TenantID  string
	Rows      []AssociationPreviewRow
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AssociationsPreviewResponse struct {
	PreviewToken string                  `json:"previewToken"`
	TotalRows    int                     `json:"totalRows"`
	ValidRows    int                     `json:"validRows"`
	InvalidRows  int                     `json:"invalidRows"`
	Rows         []AssociationPreviewRow `json:"rows"`
}

type AssociationsCommitRowResult struct {
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type AssociationsCommitResponse struct {
	Added   int                           `json:"added"`
	Skipped int                           `json:"skipped"`
	Failed  int                           `json:"failed"`
	Message string                        `json:"message"`
	Rows    []AssociationsCommitRowResult `json:"rows,omitempty"`
}

type SiteImportData struct {
	SiteId        string `json:"siteId" bson:"siteId"`
	SiteName      string `json:"siteName" bson:"siteName"`
	ContactName   string `json:"contactName" bson:"contactName"`
	ContactNumber string `json:"contactNumber" bson:"contactNumber"`
	ContactEmail  string `json:"contactEmail" bson:"contactEmail"`
}

type SitePreviewRow struct {
	RowNumber int            `json:"rowNumber"`
	Valid     bool           `json:"valid"`
	Data      SiteImportData `json:"data"`
	Errors    []string       `json:"errors"`
}

type SitesImportPreview struct {
	Token     string
	TenantID  string
	Rows      []SitePreviewRow
	CreatedAt time.Time
	ExpiresAt time.Time
}

type SitesPreviewResponse struct {
	PreviewToken string           `json:"previewToken"`
	TotalRows    int              `json:"totalRows"`
	ValidRows    int              `json:"validRows"`
	InvalidRows  int              `json:"invalidRows"`
	Rows         []SitePreviewRow `json:"rows"`
}

type SitesCommitRowResult struct {
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type SitesCommitResponse struct {
	Added   int                    `json:"added"`
	Skipped int                    `json:"skipped"`
	Failed  int                    `json:"failed"`
	Message string                 `json:"message"`
	Rows    []SitesCommitRowResult `json:"rows,omitempty"`
}

/* This temporary in-memory store is sufficient while Ref Ledger is running as one pod. Important:
If Kubernetes runs multiple Ref Ledger pods, the preview request and commit request might reach different pods.
In that case, store previews in MongoDB instead of memory. */

var officialsPreviewStore = struct {
	sync.RWMutex
	items map[string]OfficialsImportPreview
}{
	items: make(map[string]OfficialsImportPreview),
}

var associationsPreviewStore = struct {
	sync.RWMutex
	items map[string]AssociationsImportPreview
}{
	items: make(map[string]AssociationsImportPreview),
}

var sitesPreviewStore = struct {
	sync.RWMutex
	items map[string]SitesImportPreview
}{
	items: make(map[string]SitesImportPreview),
}

var gamesPreviewStore = struct {
	sync.RWMutex
	items map[string]GamesImportPreview
}{
	items: make(map[string]GamesImportPreview),
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("writeJSON: unable to encode response: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func validateCSVFile(fileHeader *multipart.FileHeader) error {

	if fileHeader == nil {
		return errors.New("CSV file information is missing")
	}

	if fileHeader.Size <= 0 {
		return errors.New("the selected CSV file is empty")
	}

	if fileHeader.Size > maxCSVSize {
		return errors.New("the selected CSV file exceeds the 5 MB limit")
	}

	fileName := strings.ToLower(strings.TrimSpace(fileHeader.Filename))

	if !strings.HasSuffix(fileName, ".csv") {
		return errors.New("the selected file must have a .csv extension")
	}

	return nil
}

func normalizeHeaderName(value string) string {

	value = strings.TrimSpace(value)

	/* Remove the UTF-8 byte order mark that Excel may add to the first column heading. */

	value = strings.TrimPrefix(value, "\uFEFF")
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, " ", "")

	return value
}

func validateAssociationsCSVHeader(header []string) error {

	expected := []string{
		"associationId",
		"associationName",
		"contactName",
		"contactNumber",
		"contactEmail",
		"assignors(comma-separated)",
	}

	if len(header) != len(expected) {
		return fmt.Errorf("invalid CSV header: expected %d columns but found %d",
			len(expected),
			len(header))
	}

	for i := range header {
		actualName := normalizeHeaderName(header[i])
		expectedName := normalizeHeaderName(expected[i])
		if actualName != expectedName {
			return fmt.Errorf(
				"invalid CSV header in column %d: expected %q but found %q",
				i+1,
				expectedName,
				actualName,
			)
		}
	}

	return nil
}

func validateSitesCSVHeader(header []string) error {

	expected := []string{
		"siteid",
		"sitename",
		"contactname",
		"contactnumber",
		"contactemail",
	}

	if len(header) != len(expected) {
		return fmt.Errorf("invalid CSV header: expected %d columns but found %d",
			len(expected),
			len(header))
	}

	for i := range header {
		actualName := normalizeHeaderName(header[i])
		expectedName := normalizeHeaderName(expected[i])
		if actualName != expectedName {
			return fmt.Errorf(
				"invalid CSV header in column %d: expected %q but found %q",
				i+1,
				expectedName,
				actualName,
			)
		}
	}

	return nil

}

func validateGamesCSVHeader(header []string) error {

	expected := []string{
		"gameid",
		"date",
		"taxyear",
		"time",
		"sport",
		"site",
		"field",
		"numofgames",
		"level",
		"gamefee",
		"travelpay",
		"assignorfee",
		"deductions",
		"association",
		"status",
		"official1",
		"official2",
		"official3",
		"official4",
		"official5",
		"official6",
		"official7",
		"official8",
		"official9",
		"official10",
		"assignor",
		"home",
		"visitor",
		"notes",
	}

	if len(header) != len(expected) {
		return fmt.Errorf(
			"invalid CSV header: expected %d columns but found %d",
			len(expected),
			len(header),
		)
	}

	for i := range header {
		actualName := normalizeHeaderName(header[i])
		if actualName != expected[i] {
			return fmt.Errorf(
				"invalid CSV header in column %d: expected %q but found %q",
				i+1,
				expected[i],
				strings.TrimSpace(header[i]),
			)
		}
	}

	return nil
}

func validateOfficialsCSVHeader(header []string) error {

	expected := []string{
		"firstname",
		"lastname",
		"phone",
		"email",
		"address",
	}

	if len(header) != len(expected) {
		return fmt.Errorf("invalid CSV header: expected %d columns but found %d",
			len(expected),
			len(header))
	}

	for i := range header {
		actualName := normalizeHeaderName(header[i])
		if actualName != expected[i] {
			return fmt.Errorf(
				"invalid CSV header in column %d: expected %q but found %q",
				i+1,
				expected[i],
				strings.TrimSpace(header[i]),
			)
		}
	}

	return nil
}

func csvColumn(record []string, index int) string {

	if index < 0 || index >= len(record) {
		return ""
	}

	return strings.TrimSpace(record[index])

}

func isBlankCSVRecord(record []string) bool {

	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}

	return true
}

func normalizeAssociationId(associationId string) string {
	return strings.ToLower(strings.TrimSpace(associationId))
}

func normalizeOfficialName(firstName string, lastName string) string {

	return strings.ToLower(strings.TrimSpace(firstName)) + "|" + strings.ToLower(strings.TrimSpace(lastName))
}

func normalizeSiteId(siteId string) string {
	return strings.ToLower(strings.TrimSpace(siteId))
}

func buildGamePreviewRow(
	rowNumber int,
	record []string,
	tId string,
	seenDateTimes map[string]bool,
) GamePreviewRow {

	const expectedColumns = 29
	const firstOfficialColumn = 15
	const maxOfficialColumns = 10
	const assignorColumn = 25
	const homeColumn = 26
	const visitorColumn = 27
	const notesColumn = 28

	var dateValid bool
	var timeValid bool

	row := GamePreviewRow{
		RowNumber: rowNumber,
		Valid:     false,
		Errors:    make([]string, 0),
	}

	gameId, _ := strconv.ParseInt(
		csvColumn(record, 0),
		10,
		64,
	)

	numOfGames, _ := strconv.ParseInt(
		csvColumn(record, 7),
		10,
		64,
	)

	row.Data = GameImportData{
		GameId:      gameId,
		Date:        csvColumn(record, 1),
		TaxYear:     csvColumn(record, 2),
		Time:        csvColumn(record, 3),
		Sport:       csvColumn(record, 4),
		Site:        csvColumn(record, 5),
		Field:       csvColumn(record, 6),
		NumOfGames:  numOfGames,
		Level:       csvColumn(record, 8),
		GameFee:     csvColumn(record, 9),
		TravelPay:   csvColumn(record, 10),
		AssignorFee: csvColumn(record, 11),
		Deductions:  csvColumn(record, 12),
		Association: csvColumn(record, 13),
		Status:      csvColumn(record, 14),
		Officials:   make([]model.GameOfficialDoc, 0),
		Assignor:    csvColumn(record, assignorColumn),
		Home:        csvColumn(record, homeColumn),
		Visitor:     csvColumn(record, visitorColumn),
		Notes:       csvColumn(record, notesColumn),
	}

	sportSupported, err := uc.IsSportSupported(tId, row.Data.Sport)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Failed to check if sport %q is supported%v",
				row.Data.Sport,
				err,
			),
		)
	}

	if !sportSupported {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Sport %q is not supported",
				row.Data.Sport,
			),
		)
	}

	if len(record) != expectedColumns {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Expected %d columns but found %d",
				expectedColumns,
				len(record),
			),
		)
		return row
	}

	row.Data.Date = strings.TrimSpace(row.Data.Date)
	row.Data.TaxYear = strings.TrimSpace(row.Data.TaxYear)

	var gameDate time.Time
	dateValid = false
	timeValid = false

	if database.ValidStatus(row.Data.Status) == false {
		row.Errors = append(row.Errors, fmt.Sprintf("Invalid game status: %q", row.Data.Status))
	}

	if row.Data.Date == "" {
		row.Errors = append(row.Errors, "Game date is required")
	} else {
		var err error
		gameDate, err = time.Parse("1/2/2006", row.Data.Date)
		if err != nil {
			row.Errors = append(
				row.Errors,
				"Invalid game date; expected a valid date in M/D/YYYY format",
			)
		} else {
			dateValid = true
		}
	}

	// Default a blank tax year only when the game date is valid.
	if row.Data.TaxYear == "" && dateValid {
		row.Data.TaxYear = strconv.Itoa(gameDate.Year())
	}

	// A supplied or derived tax year must contain exactly four digits
	// and be between 1000 and 9999.
	if row.Data.TaxYear != "" {
		taxYear, err := strconv.Atoi(row.Data.TaxYear)

		digitsOnly := len(row.Data.TaxYear) == 4
		for _, char := range row.Data.TaxYear {
			if char < '0' || char > '9' {
				digitsOnly = false
				break
			}
		}

		if !digitsOnly || err != nil || taxYear < 1000 || taxYear > 9999 {
			row.Errors = append(
				row.Errors,
				"Invalid tax year; expected a four-digit year between 1000 and 9999",
			)
		}
	}

	var dollarAmountPattern = regexp.MustCompile(`^\$?[0-9]+(\.[0-9]{1,2})?$`)
	var timePattern = regexp.MustCompile(`^(1[0-2]|[1-9]):[0-5][0-9] (AM|PM)$`)

	if !dollarAmountPattern.MatchString(row.Data.GameFee) {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Invalid Game Fee: %q",
				row.Data.GameFee,
			),
		)
	}
	if !dollarAmountPattern.MatchString(row.Data.TravelPay) {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Invalid Travel Pay: %q",
				row.Data.TravelPay,
			),
		)
	}
	if !dollarAmountPattern.MatchString(row.Data.AssignorFee) {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Invalid Assignor Fee: %q",
				row.Data.AssignorFee,
			),
		)
	}
	if !dollarAmountPattern.MatchString(row.Data.Deductions) {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Invalid Deductions: %q",
				row.Data.Deductions,
			),
		)
	}

	if !timePattern.MatchString(row.Data.Time) {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Invalid Time: %q; use h:mm AM or h:mm PM",
				row.Data.Time,
			),
		)
	} else {
		timeValid = true
	}

	if dateValid && timeValid {

		key := row.Data.Date + "|" + row.Data.Time

		if seenDateTimes[key] {
			row.Errors = append(
				row.Errors,
				"Duplicate game in file with date and time",
			)
		} else {
			dupGames, err := gc.DupGamesWithDateAndTime(tId, row.Data.Date, row.Data.Time)
			if err != nil {
				row.Errors = append(
					row.Errors,
					fmt.Sprintf(
						"Error occurred while checking for duplicate games with date %q and time %q: %v",
						row.Data.Date,
						row.Data.Time,
						err,
					),
				)
			} else if dupGames {
				row.Errors = append(
					row.Errors,
					"Game with the same date and time already exists",
				)
			} else {
				seenDateTimes[key] = true
			}
		}
	}

	// Set this after all validation checks.
	row.Valid = len(row.Errors) == 0

	if utf8.RuneCountInString(row.Data.Notes) > 150 {
		row.Errors = append(row.Errors, "Notes cannot exceed 150 characters.")
	}

	/*
		Validate level.
	*/
	levelName := strings.TrimSpace(row.Data.Level)

	if levelName == "" {
		row.Errors = append(
			row.Errors,
			"Level is required",
		)
	} else {
		levelExists, levelErr := lc.LevelExists(
			tId,
			levelName,
		)

		if levelErr != nil {
			row.Errors = append(
				row.Errors,
				fmt.Sprintf(
					"Error occurred while validating level %q: %v",
					levelName,
					levelErr,
				),
			)
		} else if !levelExists {
			row.Errors = append(
				row.Errors,
				fmt.Sprintf(
					"Level %q does not exist",
					levelName,
				),
			)
		}
	}

	/*
		Validate association.
	*/
	exists, err := ac.Exists(
		row.Data.Association,
		tId,
	)

	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Error occurred while fetching association ID: %v",
				err,
			),
		)
	} else if !exists {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Association %q does not exist",
				row.Data.Association,
			),
		)
	}

	/*
		Load the roles configured for this sport.

		database.GetOfficialRoles returns the roles for the requested sport.
		Only active roles participate in a game import. The active roles are
		sorted by DisplayOrder so official1 maps to the first configured role,
		official2 maps to the second configured role, and so on.
	*/
	sportName := strings.TrimSpace(row.Data.Sport)

	if sportName == "" {
		row.Errors = append(
			row.Errors,
			"Sport is required",
		)
	} else {
		roleResponse, roleErr := database.GetOfficialRoles(sportName)

		if roleErr != nil {
			row.Errors = append(
				row.Errors,
				fmt.Sprintf(
					"Error occurred while fetching official roles for sport %q: %v",
					sportName,
					roleErr,
				),
			)
		} else {
			sort.SliceStable(
				roleResponse.Roles,
				func(i, j int) bool {
					return roleResponse.Roles[i].DisplayOrder <
						roleResponse.Roles[j].DisplayOrder
				},
			)

			activeRoleCount := 0
			for _, role := range roleResponse.Roles {
				if role.Active {
					activeRoleCount++
				}
			}

			if activeRoleCount > maxOfficialColumns {
				row.Errors = append(
					row.Errors,
					fmt.Sprintf(
						"Sport %q has %d active official roles, but the Games CSV supports a maximum of %d",
						sportName,
						activeRoleCount,
						maxOfficialColumns,
					),
				)
			} else {
				officialPosition := 0

				for _, role := range roleResponse.Roles {
					if !role.Active {
						continue
					}

					name := strings.TrimSpace(
						csvColumn(
							record,
							firstOfficialColumn+officialPosition,
						),
					)

					if name == "" {
						name = "Unassigned"
					}

					gameOfficial := model.GameOfficialDoc{
						RoleId:       role.RoleId,
						Title:        role.Title,
						DisplayOrder: role.DisplayOrder,
						Name:         name,
					}

					if !strings.EqualFold(name, "Unassigned") {
						officialExists, officialErr := oc.Exists(
							name,
							tId,
						)

						if officialErr != nil {
							row.Errors = append(
								row.Errors,
								fmt.Sprintf(
									"Unable to validate %s %q: %v",
									role.Title,
									name,
									officialErr,
								),
							)
						} else if !officialExists {
							row.Errors = append(
								row.Errors,
								fmt.Sprintf(
									"%s %q does not exist",
									role.Title,
									name,
								),
							)
						}
					}

					row.Data.Officials = append(
						row.Data.Officials,
						gameOfficial,
					)

					officialPosition++
				}

				/*
					Columns beyond the number of active roles for the sport
					must be blank. This catches an official entered in a
					position that does not exist for that sport.
				*/
				for i := activeRoleCount; i < maxOfficialColumns; i++ {
					name := strings.TrimSpace(
						csvColumn(
							record,
							firstOfficialColumn+i,
						),
					)

					if name != "" &&
						!strings.EqualFold(name, "Unassigned") {

						row.Errors = append(
							row.Errors,
							fmt.Sprintf(
								"Sport %q has %d active official roles; official%d must be blank",
								sportName,
								activeRoleCount,
								i+1,
							),
						)
					}
				}
			}
		}
	}

	fmt.Printf(
		"Errors before assignor validation: %#v\n",
		row.Errors,
	)

	fmt.Printf(
		"AssignorExists arguments: name=%q tenantId=%q associationId=%q\n",
		row.Data.Assignor,
		tId,
		row.Data.Association,
	)

	/*
		Validate assignor unless the game is unassigned.
	*/
	if !strings.EqualFold(
		strings.TrimSpace(row.Data.Assignor),
		"Unassigned",
	) {
		assignorExists, assignorErr := ac.AssignorExists(
			row.Data.Assignor,
			tId,
			row.Data.Association,
		)

		fmt.Printf(
			"AssignorExists returned: exists=%t err=%v assignor=%q association=%q\n",
			assignorExists,
			assignorErr,
			row.Data.Assignor,
			row.Data.Association,
		)

		if assignorErr != nil {
			row.Errors = append(
				row.Errors,
				fmt.Sprintf(
					"Error occurred while fetching Assignor: %v",
					assignorErr,
				),
			)
		} else if !assignorExists {
			row.Errors = append(
				row.Errors,
				fmt.Sprintf(
					"Assignor %q does not exist",
					row.Data.Assignor,
				),
			)
		}
	}

	fmt.Printf(
		"Errors after assignor validation: %#v\n",
		row.Errors,
	)

	row.Valid = len(row.Errors) == 0

	return row
}

func buildOfficialPreviewRow(rowNumber int, record []string) OfficialPreviewRow {

	row := OfficialPreviewRow{
		RowNumber: rowNumber,
		Valid:     false,
		Errors:    make([]string, 0),
	}

	if len(record) != 5 {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Expected 5 columns but found %d",
				len(record),
			),
		)

		/* Still copy any available values so the user can see what was read from the CSV. */

		row.Data = OfficialImportData{
			FirstName: csvColumn(record, 0),
			LastName:  csvColumn(record, 1),
			Phone:     csvColumn(record, 2),
			Email:     csvColumn(record, 3),
			Address:   csvColumn(record, 4),
		}
		return row
	}

	row.Data = OfficialImportData{
		FirstName: strings.TrimSpace(record[0]),
		LastName:  strings.TrimSpace(record[1]),
		Phone:     strings.TrimSpace(record[2]),
		Email:     strings.TrimSpace(record[3]),
		Address:   strings.TrimSpace(record[4]),
	}

	if row.Data.FirstName == "" {
		row.Errors = append(
			row.Errors,
			"First name is required",
		)
	}

	if row.Data.LastName == "" {
		row.Errors = append(
			row.Errors,
			"Last name is required",
		)
	}

	if len(row.Data.FirstName) > 100 {
		row.Errors = append(
			row.Errors,
			"First name cannot exceed 100 characters",
		)
	}
	if len(row.Data.LastName) > 100 {
		row.Errors = append(
			row.Errors,
			"Last name cannot exceed 100 characters",
		)
	}
	if len(row.Data.Phone) > 50 {
		row.Errors = append(
			row.Errors,
			"Phone number cannot exceed 50 characters",
		)
	}
	if len(row.Data.Email) > 254 {
		row.Errors = append(
			row.Errors,
			"Email cannot exceed 254 characters",
		)
	}
	if len(row.Data.Address) > 500 {
		row.Errors = append(
			row.Errors,
			"Address cannot exceed 500 characters",
		)
	}
	if row.Data.Email != "" {
		if _, err := mail.ParseAddress(row.Data.Email); err != nil {
			row.Errors = append(
				row.Errors,
				"Email address is invalid",
			)
		}
	}

	row.Valid = len(row.Errors) == 0

	return row

}

func buildSitePreviewRow(rowNumber int, record []string) SitePreviewRow {

	row := SitePreviewRow{
		RowNumber: rowNumber,
		Valid:     false,
		Errors:    make([]string, 0),
	}

	if len(record) != 5 {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Expected 5 columns but found %d",
				len(record),
			),
		)

		/* Still copy any available values so the user can see what was read from the CSV. */

		row.Data = SiteImportData{
			SiteId:        csvColumn(record, 0),
			SiteName:      csvColumn(record, 1),
			ContactName:   csvColumn(record, 2),
			ContactNumber: csvColumn(record, 3),
			ContactEmail:  csvColumn(record, 4),
		}
		return row
	}

	row.Data = SiteImportData{
		SiteId:        strings.TrimSpace(record[0]),
		SiteName:      strings.TrimSpace(record[1]),
		ContactName:   strings.TrimSpace(record[2]),
		ContactNumber: strings.TrimSpace(record[3]),
		ContactEmail:  strings.TrimSpace(record[4]),
	}

	if row.Data.SiteId == "" {
		row.Errors = append(
			row.Errors,
			"Site ID is required",
		)
	}

	if row.Data.SiteName == "" {
		row.Errors = append(
			row.Errors,
			"Site name is required",
		)
	}

	if len(row.Data.SiteName) > 100 {
		row.Errors = append(
			row.Errors,
			"Site name cannot exceed 100 characters",
		)
	}
	if len(row.Data.ContactName) > 100 {
		row.Errors = append(
			row.Errors,
			"Contact name cannot exceed 100 characters",
		)
	}
	if len(row.Data.ContactNumber) > 50 {
		row.Errors = append(
			row.Errors,
			"Contact number cannot exceed 50 characters",
		)
	}
	if len(row.Data.ContactEmail) > 254 {
		row.Errors = append(
			row.Errors,
			"Contact email cannot exceed 254 characters",
		)
	}

	if row.Data.ContactEmail != "" {
		if _, err := mail.ParseAddress(row.Data.ContactEmail); err != nil {
			row.Errors = append(
				row.Errors,
				"Contact email address is invalid",
			)
		}
	}

	row.Valid = len(row.Errors) == 0

	return row

}

func buildAssociationPreviewRow(rowNumber int, record []string) AssociationPreviewRow {

	row := AssociationPreviewRow{
		RowNumber: rowNumber,
		Valid:     false,
		Errors:    make([]string, 0),
	}

	if len(record) != 6 {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Expected 6 columns but found %d",
				len(record),
			),
		)

		/* Still copy any available values so the user can see what was read from the CSV. */

		row.Data = AssociationImportData{
			AssociationId:   csvColumn(record, 0),
			AssociationName: csvColumn(record, 1),
			ContactName:     csvColumn(record, 2),
			ContactNumber:   csvColumn(record, 3),
			ContactEmail:    csvColumn(record, 4),
			Assignors:       csvColumn(record, 5),
		}
		return row
	}

	row.Data = AssociationImportData{
		AssociationId:   csvColumn(record, 0),
		AssociationName: csvColumn(record, 1),
		ContactName:     csvColumn(record, 2),
		ContactNumber:   csvColumn(record, 3),
		ContactEmail:    csvColumn(record, 4),
		Assignors:       csvColumn(record, 5),
	}

	if row.Data.AssociationId == "" {
		row.Errors = append(
			row.Errors,
			"Association ID is required",
		)
	}

	if row.Data.AssociationName == "" {
		row.Errors = append(
			row.Errors,
			"Association name is required",
		)
	}

	if len(row.Data.ContactName) > 100 {
		row.Errors = append(
			row.Errors,
			"Contact name cannot exceed 100 characters",
		)
	}

	if len(row.Data.ContactNumber) > 50 {
		row.Errors = append(
			row.Errors,
			"Contact number cannot exceed 50 characters",
		)
	}

	if row.Data.ContactEmail != "" {
		if _, err := mail.ParseAddress(row.Data.ContactEmail); err != nil {
			row.Errors = append(
				row.Errors,
				"Contact email address is invalid",
			)
		}
	}

	row.Valid = len(row.Errors) == 0

	return row

}

func generatePreviewToken() (string, error) {

	bytes := make([]byte, 32)

	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}

func saveAssociationsPreview(preview AssociationsImportPreview) {

	associationsPreviewStore.Lock()

	defer associationsPreviewStore.Unlock()

	associationsPreviewStore.items[preview.Token] = preview
}

func saveOfficialsPreview(preview OfficialsImportPreview) {

	officialsPreviewStore.Lock()

	defer officialsPreviewStore.Unlock()

	officialsPreviewStore.items[preview.Token] = preview
}

func saveSitesPreview(preview SitesImportPreview) {

	sitesPreviewStore.Lock()

	defer sitesPreviewStore.Unlock()

	sitesPreviewStore.items[preview.Token] = preview
}

func saveGamesPreview(preview GamesImportPreview) {

	fmt.Println("Saving Games Preview", preview, "with Token:", preview.Token)
	gamesPreviewStore.Lock()

	defer gamesPreviewStore.Unlock()

	gamesPreviewStore.items[preview.Token] = preview
}

func deleteOfficialsPreview(token string) {

	officialsPreviewStore.Lock()
	defer officialsPreviewStore.Unlock()

	delete(officialsPreviewStore.items, token)

}

func deleteGamesPreview(token string) {

	gamesPreviewStore.Lock()
	defer gamesPreviewStore.Unlock()

	delete(gamesPreviewStore.items, token)

}

func deleteSitesPreview(token string) {

	sitesPreviewStore.Lock()
	defer sitesPreviewStore.Unlock()

	delete(sitesPreviewStore.items, token)

}

func deleteAssociationsPreview(token string) {

	associationsPreviewStore.Lock()
	defer associationsPreviewStore.Unlock()

	delete(associationsPreviewStore.items, token)

}

func removeExpiredOfficialsPreviews() {
	now := time.Now()
	officialsPreviewStore.Lock()
	defer officialsPreviewStore.Unlock()
	for token, preview := range officialsPreviewStore.items {
		if now.After(preview.ExpiresAt) {
			delete(
				officialsPreviewStore.items,
				token,
			)
		}
	}
}

func removeExpiredGamesPreviews() {
	now := time.Now()
	gamesPreviewStore.Lock()
	defer gamesPreviewStore.Unlock()
	for token, preview := range gamesPreviewStore.items {
		if now.After(preview.ExpiresAt) {
			delete(
				gamesPreviewStore.items,
				token,
			)
		}
	}
}

func removeExpiredSitesPreviews() {
	now := time.Now()
	sitesPreviewStore.Lock()
	defer sitesPreviewStore.Unlock()
	for token, preview := range sitesPreviewStore.items {
		if now.After(preview.ExpiresAt) {
			delete(
				sitesPreviewStore.items,
				token,
			)
		}
	}
}

func removeExpiredAssociationsPreviews() {
	now := time.Now()
	associationsPreviewStore.Lock()
	defer associationsPreviewStore.Unlock()
	for token, preview := range associationsPreviewStore.items {
		if now.After(preview.ExpiresAt) {
			delete(
				associationsPreviewStore.items,
				token,
			)
		}
	}
}

func getAssociationsPreview(token string) (AssociationsImportPreview, bool) {

	associationsPreviewStore.RLock()
	defer associationsPreviewStore.RUnlock()

	preview, found := associationsPreviewStore.items[token]

	if !found {
		return AssociationsImportPreview{}, false
	}

	// Treat expired previews as missing.
	if time.Now().After(preview.ExpiresAt) {
		return AssociationsImportPreview{}, false
	}

	return preview, true
}

func getSitesPreview(token string) (SitesImportPreview, bool) {
	sitesPreviewStore.RLock()
	defer sitesPreviewStore.RUnlock()

	preview, found := sitesPreviewStore.items[token]
	if !found || !time.Now().Before(preview.ExpiresAt) {
		return SitesImportPreview{}, false
	}

	return preview, true
}

func getGamesPreview(token string) (GamesImportPreview, bool) {

	gamesPreviewStore.RLock()
	defer gamesPreviewStore.RUnlock()

	preview, found := gamesPreviewStore.items[token]

	if !found {
		return GamesImportPreview{}, false
	}

	// Treat expired previews as missing.
	if time.Now().After(preview.ExpiresAt) {
		return GamesImportPreview{}, false
	}

	return preview, true
}

func getOfficialsPreview(token string) (OfficialsImportPreview, bool) {

	officialsPreviewStore.RLock()
	defer officialsPreviewStore.RUnlock()

	preview, found := officialsPreviewStore.items[token]

	if !found {
		return OfficialsImportPreview{}, false
	}

	// Treat expired previews as missing.
	if time.Now().After(preview.ExpiresAt) {
		return OfficialsImportPreview{}, false
	}

	return preview, true
}

func CommitAssociationsImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	var request struct {
		PreviewToken    string `json:"previewToken"`
		DuplicateAction string `json:"duplicateAction"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Invalid request body.",
		)
		return
	}

	request.PreviewToken = strings.TrimSpace(request.PreviewToken)
	request.DuplicateAction = strings.ToLower(strings.TrimSpace(request.DuplicateAction))

	if request.PreviewToken == "" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Preview token is required.",
		)
		return
	}

	if request.DuplicateAction == "" {
		request.DuplicateAction = "skip"
	}

	if request.DuplicateAction != "skip" && request.DuplicateAction != "stop" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			`Duplicate action must be "skip" or "stop".`,
		)
		return
	}

	preview, found := getAssociationsPreview(request.PreviewToken)
	if !found {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview was not found or has expired.",
		)
		return
	}

	if preview.TenantID != tId {
		writeJSONError(
			w,
			http.StatusForbidden,
			"The import preview does not belong to the current tenant.",
		)
		return
	}

	if time.Now().After(preview.ExpiresAt) {
		deleteAssociationsPreview(request.PreviewToken)
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview has expired. Preview the CSV file again.",
		)
		return
	}

	if ac.Coll == nil {
		log.Printf("CommitAssociationsImportHandler: associations collection is nil")
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Associations collection is not available.",
		)
		return
	}

	added := 0
	skipped := 0
	failed := 0
	rowResults := make([]AssociationsCommitRowResult, 0)

	for _, previewRow := range preview.Rows {
		if !previewRow.Valid {
			continue
		}
		data := previewRow.Data

		exists, err := ac.Exists(data.AssociationId, tId)

		if err != nil {
			log.Printf(
				"CommitAssociationsImportHandler: "+"duplicate lookup failed: tenant=%s row=%d associationId=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.AssociationId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				AssociationsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to check whether the association already exists.",
				},
			)
			continue
		}

		if exists {
			if request.DuplicateAction == "stop" {
				writeJSON(
					w,
					http.StatusConflict,
					AssociationsCommitResponse{
						Added:   added,
						Skipped: skipped,
						Failed:  failed,
						Message: fmt.Sprintf(
							"Import stopped because %s already exists.",
							data.AssociationId,
						),
						Rows: rowResults,
					},
				)

				return
			}

			skipped++

			rowResults = append(
				rowResults,
				AssociationsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "skipped",
					Message: fmt.Sprintf(
						"%s already exists.",
						data.AssociationId,
					),
				},
			)

			continue

		}

		association := database.Association{
			Id:        data.AssociationId,
			Name:      data.AssociationName,
			Contact:   data.ContactName,
			Phone:     data.ContactNumber,
			Email:     data.ContactEmail,
			Assignors: data.Assignors,
		}

		err = ac.Add(association, tId)

		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				if request.DuplicateAction == "stop" {
					writeJSON(
						w,
						http.StatusConflict,
						AssociationsCommitResponse{
							Added:   added,
							Skipped: skipped,
							Failed:  failed,
							Message: fmt.Sprintf(
								"Import stopped because %s already exists.",
								data.AssociationId,
							),
							Rows: rowResults,
						},
					)
					return
				}

				skipped++

				rowResults = append(
					rowResults,
					AssociationsCommitRowResult{
						RowNumber: previewRow.RowNumber,
						Status:    "skipped",
						Message: fmt.Sprintf(
							"%s already exists.",
							data.AssociationId,
						),
					},
				)

				continue

			}

			log.Printf(
				"CommitAssociationsImportHandler: "+"insert failed: tenant=%s row=%d association=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.AssociationId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				AssociationsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to insert association.",
				},
			)

			continue
		}
		added++
		rowResults = append(
			rowResults,
			AssociationsCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "added",
				Message:   fmt.Sprintf("%s was added.", data.AssociationId),
			},
		)
	}

	deleteAssociationsPreview(request.PreviewToken)

	writeJSON(
		w,
		http.StatusOK,
		AssociationsCommitResponse{
			Added:   added,
			Skipped: skipped,
			Failed:  failed,
			Message: "Associations import completed.",
			Rows:    rowResults,
		},
	)

}

func CommitOfficialsImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	var request struct {
		PreviewToken    string `json:"previewToken"`
		DuplicateAction string `json:"duplicateAction"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Invalid request body.",
		)
		return
	}

	request.PreviewToken = strings.TrimSpace(request.PreviewToken)
	request.DuplicateAction = strings.ToLower(strings.TrimSpace(request.DuplicateAction))

	if request.PreviewToken == "" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Preview token is required.",
		)
		return
	}

	if request.DuplicateAction == "" {
		request.DuplicateAction = "skip"
	}

	if request.DuplicateAction != "skip" && request.DuplicateAction != "stop" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			`Duplicate action must be "skip" or "stop".`,
		)
		return
	}

	preview, found := getOfficialsPreview(request.PreviewToken)
	if !found {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview was not found or has expired.",
		)
		return
	}

	if preview.TenantID != tId {
		writeJSONError(
			w,
			http.StatusForbidden,
			"The import preview does not belong to the current tenant.",
		)
		return
	}

	if time.Now().After(preview.ExpiresAt) {
		deleteOfficialsPreview(request.PreviewToken)
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview has expired. Preview the CSV file again.",
		)
		return
	}

	if oc.Coll == nil {
		log.Printf("CommitOfficialsImportHandler: officials collection is nil")
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Officials collection is not available.",
		)
		return
	}

	added := 0
	skipped := 0
	failed := 0
	rowResults := make([]OfficialsCommitRowResult, 0)

	for _, previewRow := range preview.Rows {
		if !previewRow.Valid {
			continue
		}
		data := previewRow.Data

		name := data.FirstName + " " + data.LastName
		exists, err := oc.Exists(name, tId)

		if err != nil {
			log.Printf(
				"CommitOfficialsImportHandler: "+"duplicate lookup failed: tenant=%s row=%d official=%s %s error=%v",
				tId,
				previewRow.RowNumber,
				data.FirstName,
				data.LastName,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				OfficialsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to check whether the official already exists.",
				},
			)
			continue
		}

		if exists {
			if request.DuplicateAction == "stop" {
				writeJSON(
					w,
					http.StatusConflict,
					OfficialsCommitResponse{
						Added:   added,
						Skipped: skipped,
						Failed:  failed,
						Message: fmt.Sprintf(
							"Import stopped because %s %s already exists.",
							data.FirstName,
							data.LastName,
						),
						Rows: rowResults,
					},
				)

				return
			}

			skipped++

			rowResults = append(
				rowResults,
				OfficialsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "skipped",
					Message: fmt.Sprintf(
						"%s %s already exists.",
						data.FirstName,
						data.LastName,
					),
				},
			)

			continue

		}

		official := database.Official{
			FirstName: data.FirstName,
			LastName:  data.LastName,
			Phone:     data.Phone,
			Email:     data.Email,
			Address:   data.Address,
		}

		err = oc.Add(official, tId)

		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				if request.DuplicateAction == "stop" {
					writeJSON(
						w,
						http.StatusConflict,
						OfficialsCommitResponse{
							Added:   added,
							Skipped: skipped,
							Failed:  failed,
							Message: fmt.Sprintf(
								"Import stopped because %s %s already exists.",
								data.FirstName,
								data.LastName,
							),
							Rows: rowResults,
						},
					)
					return
				}

				skipped++

				rowResults = append(
					rowResults,
					OfficialsCommitRowResult{
						RowNumber: previewRow.RowNumber,
						Status:    "skipped",
						Message: fmt.Sprintf(
							"%s %s already exists.",
							data.FirstName,
							data.LastName,
						),
					},
				)

				continue

			}

			log.Printf(
				"CommitOfficialsImportHandler: "+"insert failed: tenant=%s row=%d official=%s %s error=%v",
				tId,
				previewRow.RowNumber,
				data.FirstName,
				data.LastName,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				OfficialsCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to insert official.",
				},
			)

			continue
		}
		added++
		rowResults = append(
			rowResults,
			OfficialsCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "added",
				Message:   fmt.Sprintf("%s %s was added.", data.FirstName, data.LastName),
			},
		)
	}

	deleteOfficialsPreview(request.PreviewToken)

	writeJSON(
		w,
		http.StatusOK,
		OfficialsCommitResponse{
			Added:   added,
			Skipped: skipped,
			Failed:  failed,
			Message: "Officials import completed.",
			Rows:    rowResults,
		},
	)
}

func CommitGamesImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	var request struct {
		PreviewToken    string `json:"previewToken"`
		DuplicateAction string `json:"duplicateAction"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Invalid request body.",
		)
		return
	}

	request.PreviewToken =
		strings.TrimSpace(request.PreviewToken)

	request.DuplicateAction = strings.ToLower(
		strings.TrimSpace(request.DuplicateAction),
	)

	if request.PreviewToken == "" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Preview token is required.",
		)
		return
	}

	if request.DuplicateAction == "" {
		request.DuplicateAction = "skip"
	}

	if request.DuplicateAction != "skip" &&
		request.DuplicateAction != "stop" {

		writeJSONError(
			w,
			http.StatusBadRequest,
			`Duplicate action must be "skip" or "stop".`,
		)
		return
	}

	preview, found :=
		getGamesPreview(request.PreviewToken)

	if !found {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview was not found or has expired.",
		)
		return
	}

	if preview.TenantID != tId {
		writeJSONError(
			w,
			http.StatusForbidden,
			"The import preview does not belong to the current tenant.",
		)
		return
	}

	if time.Now().After(preview.ExpiresAt) {
		deleteGamesPreview(request.PreviewToken)

		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview has expired. Preview the CSV file again.",
		)
		return
	}

	// This must check the games collection, not the officials collection.
	if gc.Coll == nil {
		log.Printf(
			"CommitGamesImportHandler: games collection is nil",
		)

		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Games collection is not available.",
		)
		return
	}

	added := 0
	skipped := 0
	failed := 0

	rowResults := make(
		[]GamesCommitRowResult,
		0,
	)

	for _, previewRow := range preview.Rows {

		if !previewRow.Valid {
			continue
		}

		data := previewRow.Data
		gameIDText := utils.ConvertInt64ToStr(data.GameId)

		exists, err := gc.Exists(
			data.Association,
			gameIDText,
			tId,
		)

		if err != nil {
			log.Printf(
				"CommitGamesImportHandler: duplicate lookup failed: "+
					"tenant=%s row=%d game=%d error=%v",
				tId,
				previewRow.RowNumber,
				data.GameId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				GamesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message: "Unable to check whether " +
						"the game already exists.",
				},
			)

			continue
		}

		if exists {
			if request.DuplicateAction == "stop" {
				writeJSON(
					w,
					http.StatusConflict,
					GamesCommitResponse{
						Added:   added,
						Skipped: skipped,
						Failed:  failed,
						Message: fmt.Sprintf(
							"Import stopped because game %d already exists.",
							data.GameId,
						),
						Rows: rowResults,
					},
				)

				return
			}

			skipped++

			rowResults = append(
				rowResults,
				GamesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "skipped",
					Message: fmt.Sprintf(
						"Game %d already exists.",
						data.GameId,
					),
				},
			)

			continue
		}

		if strings.TrimSpace(data.TaxYear) == "" {
			gameDate, err := time.Parse("1/2/2006", strings.TrimSpace(data.Date))
			if err == nil {
				data.TaxYear = strconv.Itoa(gameDate.Year())
			}
		}

		game := model.GameDescriptor{
			GameId:  gameIDText,
			Date:    data.Date,
			TaxYear: data.TaxYear,
			Time:    data.Time,
			Sport:   data.Sport,
			Site:    data.Site,
			Field:   data.Field,
			NumOfGames: utils.ConvertInt64ToStr(
				data.NumOfGames,
			),
			Level:       data.Level,
			Home:        data.Home,
			Visitor:     data.Visitor,
			GameFee:     data.GameFee,
			TravelPay:   data.TravelPay,
			AssignorFee: data.AssignorFee,
			Deductions:  data.Deductions,
			Association: data.Association,
			Status:      data.Status,
			Officials:   data.Officials,
			Assignor:    data.Assignor,
			Notes:       data.Notes,
		}

		err = gc.Add(tId, game)
		if err != nil {

			if mongo.IsDuplicateKeyError(err) {

				if request.DuplicateAction == "stop" {
					writeJSON(
						w,
						http.StatusConflict,
						GamesCommitResponse{
							Added:   added,
							Skipped: skipped,
							Failed:  failed,
							Message: fmt.Sprintf(
								"Import stopped because game %d already exists.",
								data.GameId,
							),
							Rows: rowResults,
						},
					)

					return
				}

				skipped++

				rowResults = append(
					rowResults,
					GamesCommitRowResult{
						RowNumber: previewRow.RowNumber,
						Status:    "skipped",
						Message: fmt.Sprintf(
							"Game %d already exists.",
							data.GameId,
						),
					},
				)

				continue
			}

			log.Printf(
				"CommitGamesImportHandler: insert failed: "+
					"tenant=%s row=%d game=%d error=%v",
				tId,
				previewRow.RowNumber,
				data.GameId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				GamesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message: fmt.Sprintf(
						"Unable to insert game %d: %v",
						data.GameId,
						err,
					),
				},
			)

			continue
		}

		added++

		rowResults = append(
			rowResults,
			GamesCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "added",
				Message: fmt.Sprintf(
					"Game %d was added.",
					data.GameId,
				),
			},
		)
	}

	deleteGamesPreview(request.PreviewToken)

	writeJSON(
		w,
		http.StatusOK,
		GamesCommitResponse{
			Added:   added,
			Skipped: skipped,
			Failed:  failed,
			Message: "Games import completed.",
			Rows:    rowResults,
		},
	)
}

func CommitSitesImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	tId, err := getTenantId(r)
	if err != nil || tId == "" || tId == "na" {
		writeJSONError(w, http.StatusBadRequest, "Invalid tenant ID.")
		return
	}

	var request struct {
		PreviewToken    string `json:"previewToken"`
		DuplicateAction string `json:"duplicateAction"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body.")
		return
	}

	request.PreviewToken = strings.TrimSpace(request.PreviewToken)
	request.DuplicateAction = strings.ToLower(
		strings.TrimSpace(request.DuplicateAction),
	)

	if request.PreviewToken == "" {
		writeJSONError(w, http.StatusBadRequest, "Preview token is required.")
		return
	}

	if request.DuplicateAction == "" {
		request.DuplicateAction = "skip"
	}

	if request.DuplicateAction != "skip" &&
		request.DuplicateAction != "stop" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			`Duplicate action must be "skip" or "stop".`,
		)
		return
	}

	preview, found := getSitesPreview(request.PreviewToken)
	if !found {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview was not found or has expired.",
		)
		return
	}

	if preview.TenantID != tId {
		writeJSONError(
			w,
			http.StatusForbidden,
			"The import preview does not belong to the current tenant.",
		)
		return
	}

	if !time.Now().Before(preview.ExpiresAt) {
		deleteSitesPreview(request.PreviewToken)
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The import preview has expired. Preview the CSV file again.",
		)
		return
	}

	if sc.Coll == nil {
		log.Printf("CommitSitesImportHandler: sites collection is nil")
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Sites collection is not available.",
		)
		return
	}

	response := SitesCommitResponse{
		Message: "Sites import completed.",
		Rows:    make([]SitesCommitRowResult, 0),
	}

	// Returns true when the import must stop.
	handleDuplicate := func(rowNumber int, siteId string) bool {
		if request.DuplicateAction == "stop" {
			response.Message = fmt.Sprintf(
				"Import stopped because %s already exists.",
				siteId,
			)
			writeJSON(w, http.StatusConflict, response)
			return true
		}

		response.Skipped++
		response.Rows = append(response.Rows, SitesCommitRowResult{
			RowNumber: rowNumber,
			Status:    "skipped",
			Message:   fmt.Sprintf("%s already exists.", siteId),
		})
		return false
	}

	for _, previewRow := range preview.Rows {
		if !previewRow.Valid {
			continue
		}

		data := previewRow.Data

		exists, err := sc.Exists(data.SiteId, tId)
		if err != nil {
			log.Printf(
				"CommitSitesImportHandler: duplicate lookup failed: "+
					"tenant=%s row=%d site=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.SiteId,
				err,
			)

			response.Failed++
			response.Rows = append(response.Rows, SitesCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "failed",
				Message:   "Unable to check whether the site already exists.",
			})
			continue
		}

		if exists {
			if handleDuplicate(previewRow.RowNumber, data.SiteId) {
				return
			}
			continue
		}

		site := database.Site{
			Name:    data.SiteName,
			Contact: data.ContactName,
			Phone:   data.ContactNumber,
			Email:   data.ContactEmail,
		}

		if err := sc.Add(site, tId); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				if handleDuplicate(previewRow.RowNumber, data.SiteId) {
					return
				}
				continue
			}

			log.Printf(
				"CommitSitesImportHandler: insert failed: "+
					"tenant=%s row=%d site=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.SiteId,
				err,
			)

			response.Failed++
			response.Rows = append(response.Rows, SitesCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "failed",
				Message:   "Unable to insert site.",
			})
			continue
		}

		response.Added++
		response.Rows = append(response.Rows, SitesCommitRowResult{
			RowNumber: previewRow.RowNumber,
			Status:    "added",
			Message: fmt.Sprintf(
				"%s %s was added.",
				data.SiteId,
				data.SiteName,
			),
		})
	}

	deleteSitesPreview(request.PreviewToken)
	writeJSON(w, http.StatusOK, response)
}

func PreviewAssociationsImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	/* Limit the complete request body. The additional 1 MB allows room for the multipart boundary and HTTP form metadata. */

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+(oneMB))

	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The CSV upload is invalid or exceeds the 5 MB limit.")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "An Officials CSV file is required.")
		return
	}

	defer file.Close()

	if err := validateCSVFile(fileHeader); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	csvReader := csv.NewReader(file)

	/* Allow variable-length records so we can report a useful row-level error instead of stopping immediately. */
	csvReader.FieldsPerRecord = -1

	/* This trims spaces that appear outside quoted CSV values. For example: John, Smith is treated similarly to: John,Smith */
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err == io.EOF {
		writeJSONError(w, http.StatusBadRequest, "The CSV file is empty.")
		return
	}

	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Unable to read the CSV header: "+err.Error())
		return
	}

	if err := validateAssociationsCSVHeader(header); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows := make([]AssociationPreviewRow, 0)

	/* Used to detect duplicates inside the uploaded CSV file. */
	namesInFile := make(map[string]int)
	csvRowNumber := 1

	for {
		record, readErr := csvReader.Read()

		if readErr == io.EOF {
			break
		}

		csvRowNumber++
		if len(rows) >= maxCSVRows {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("The CSV file contains more than %d data rows.", maxCSVRows))
			return
		}

		if readErr != nil {
			rows = append(
				rows,
				AssociationPreviewRow{
					RowNumber: csvRowNumber,
					Valid:     false,
					Data:      AssociationImportData{},
					Errors: []string{
						"Unable to parse CSV row: " + readErr.Error(),
					},
				},
			)
			/* Some CSV syntax errors leave the reader unable to continue reliably. Stop after recording the error. */
			break
		}

		if isBlankCSVRecord(record) {
			continue
		}

		previewRow := buildAssociationPreviewRow(
			csvRowNumber,
			record,
		)

		nameKey := normalizeAssociationId(previewRow.Data.AssociationId)

		if nameKey != "" {
			if previousRow, found := namesInFile[nameKey]; found {
				previewRow.Errors = append(
					previewRow.Errors,
					fmt.Sprintf(
						"Duplicate association in CSV file; first appeared on row %d",
						previousRow,
					),
				)
			} else {
				namesInFile[nameKey] = csvRowNumber
			}
		}

		previewRow.Valid = len(previewRow.Errors) == 0

		rows = append(rows, previewRow)
	}

	if len(rows) == 0 {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The CSV file does not contain any associations.",
		)
		return
	}
	/* Check MongoDB for associations that already exist. This only runs for rows that passed the basic CSV validation. */

	for i := range rows {
		if !rows[i].Valid {
			continue
		}

		associationId := normalizeAssociationId(rows[i].Data.AssociationId)

		exists, err := ac.Exists(associationId, tId)

		if err != nil {
			log.Printf(
				"PreviewAssociationsImportHandler: duplicate lookup failed for tenant %s, association %s: %v",
				tId,
				rows[i].Data.AssociationId,
				err,
			)
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"Unable to validate associations against the database.",
			)
			return
		}

		if exists {
			rows[i].Errors = append(
				rows[i].Errors,
				"Association already exists",
			)
			rows[i].Valid = false
		}
	}

	validRows := 0
	invalidRows := 0

	for _, row := range rows {
		if row.Valid {
			validRows++
		} else {
			invalidRows++
		}
	}

	previewToken, err := generatePreviewToken()
	if err != nil {
		log.Printf(
			"PreviewAssociationsImportHandler: unable to generate preview token: %v",
			err,
		)
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Unable to create the import preview.",
		)
		return
	}

	now := time.Now()
	preview := AssociationsImportPreview{
		Token:     previewToken,
		TenantID:  tId,
		Rows:      rows,
		CreatedAt: now,
		ExpiresAt: now.Add(previewExpiration),
	}

	saveAssociationsPreview(preview)
	removeExpiredAssociationsPreviews()

	response := AssociationsPreviewResponse{
		PreviewToken: previewToken,
		TotalRows:    len(rows),
		ValidRows:    validRows,
		InvalidRows:  invalidRows,
		Rows:         rows,
	}

	writeJSON(w, http.StatusOK, response)
}

func PreviewSitesImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	/* Limit the complete request body. The additional 1 MB allows room for the multipart boundary and HTTP form metadata. */

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+(oneMB))

	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The CSV upload is invalid or exceeds the 5 MB limit.")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "An Officials CSV file is required.")
		return
	}

	defer file.Close()

	if err := validateCSVFile(fileHeader); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	csvReader := csv.NewReader(file)

	/* Allow variable-length records so we can report a useful row-level error instead of stopping immediately. */
	csvReader.FieldsPerRecord = -1

	/* This trims spaces that appear outside quoted CSV values. For example: John, Smith is treated similarly to: John,Smith */
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err == io.EOF {
		writeJSONError(w, http.StatusBadRequest, "The CSV file is empty.")
		return
	}

	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Unable to read the CSV header: "+err.Error())
		return
	}

	if err := validateSitesCSVHeader(header); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows := make([]SitePreviewRow, 0)

	/* Used to detect duplicates inside the uploaded CSV file. */
	namesInFile := make(map[string]int)
	csvRowNumber := 1

	for {
		record, readErr := csvReader.Read()

		if readErr == io.EOF {
			break
		}

		csvRowNumber++
		if len(rows) >= maxCSVRows {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("The CSV file contains more than %d data rows.", maxCSVRows))
			return
		}

		if readErr != nil {
			rows = append(
				rows,
				SitePreviewRow{
					RowNumber: csvRowNumber,
					Valid:     false,
					Data:      SiteImportData{},
					Errors: []string{
						"Unable to parse CSV row: " + readErr.Error(),
					},
				},
			)
			/* Some CSV syntax errors leave the reader unable to continue reliably. Stop after recording the error. */
			break
		}

		if isBlankCSVRecord(record) {
			continue
		}

		previewRow := buildSitePreviewRow(
			csvRowNumber,
			record,
		)

		nameKey := normalizeSiteId(previewRow.Data.SiteId)

		if nameKey != "|" {
			if previousRow, found := namesInFile[nameKey]; found {
				previewRow.Errors = append(
					previewRow.Errors,
					fmt.Sprintf(
						"Duplicate site in CSV file; first appeared on row %d",
						previousRow,
					),
				)
			} else {
				namesInFile[nameKey] = csvRowNumber
			}
		}

		previewRow.Valid = len(previewRow.Errors) == 0

		rows = append(rows, previewRow)
	}

	if len(rows) == 0 {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The CSV file does not contain any sites.",
		)
		return
	}
	/* Check MongoDB for sites that already exist. This only runs for rows that passed the basic CSV validation. */

	for i := range rows {
		if !rows[i].Valid {
			continue
		}

		siteId := rows[i].Data.SiteId

		exists, err := sc.Exists(siteId, tId)

		if err != nil {
			log.Printf(
				"PreviewSitesImportHandler: duplicate lookup failed for tenant %s, site %s: %v",
				tId,
				rows[i].Data.SiteId,
				err,
			)
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"Unable to validate sites against the database.",
			)
			return
		}

		if exists {
			rows[i].Errors = append(
				rows[i].Errors,
				"Site already exists",
			)
			rows[i].Valid = false
		}
	}

	validRows := 0
	invalidRows := 0

	for _, row := range rows {
		if row.Valid {
			validRows++
		} else {
			invalidRows++
		}
	}

	previewToken, err := generatePreviewToken()
	if err != nil {
		log.Printf(
			"PreviewSitesImportHandler: unable to generate preview token: %v",
			err,
		)
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Unable to create the import preview.",
		)
		return
	}

	now := time.Now()
	preview := SitesImportPreview{
		Token:     previewToken,
		TenantID:  tId,
		Rows:      rows,
		CreatedAt: now,
		ExpiresAt: now.Add(previewExpiration),
	}

	saveSitesPreview(preview)
	removeExpiredSitesPreviews()

	response := SitesPreviewResponse{
		PreviewToken: previewToken,
		TotalRows:    len(rows),
		ValidRows:    validRows,
		InvalidRows:  invalidRows,
		Rows:         rows,
	}

	writeJSON(w, http.StatusOK, response)

}

func PreviewGamesImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("Previewing games import...")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Allow additional space for multipart boundaries and form metadata.
	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+oneMB)

	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The CSV upload is invalid or exceeds the 5 MB limit.",
		)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"A Games CSV file is required.",
		)
		return
	}
	defer file.Close()

	if err := validateCSVFile(fileHeader); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	csvReader := csv.NewReader(file)
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err == io.EOF {
		writeJSONError(w, http.StatusBadRequest, "The CSV file is empty.")
		return
	}
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"Unable to read the CSV header: "+err.Error(),
		)
		return
	}

	if err := validateGamesCSVHeader(header); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows := make([]GamePreviewRow, 0)
	gamesInFile := make(map[int64]int)
	csvRowNumber := 1
	seenDateTimes := make(map[string]bool)

	for {
		record, readErr := csvReader.Read()
		if readErr == io.EOF {
			break
		}

		csvRowNumber++

		if readErr == nil && isBlankCSVRecord(record) {
			continue
		}

		if len(rows) >= maxCSVRows {
			writeJSONError(
				w,
				http.StatusBadRequest,
				fmt.Sprintf(
					"The CSV file contains more than %d data rows.",
					maxCSVRows,
				),
			)
			return
		}

		if readErr != nil {
			rows = append(rows, GamePreviewRow{
				RowNumber: csvRowNumber,
				Valid:     false,
				Data:      GameImportData{},
				Errors: []string{
					"Unable to parse CSV row: " + readErr.Error(),
				},
			})

			// Stop because malformed CSV may prevent reliable parsing.
			break
		}

		// Map official1..official10 to the sport's active roles.
		previewRow := buildGamePreviewRow(
			csvRowNumber,
			record,
			tId,
			seenDateTimes,
		)

		gameId := previewRow.Data.GameId

		if gameId != 0 {
			if previousRow, found := gamesInFile[gameId]; found {
				previewRow.Errors = append(
					previewRow.Errors,
					fmt.Sprintf(
						"Duplicate game in CSV file; first appeared on row %d",
						previousRow,
					),
				)
			} else {
				gamesInFile[gameId] = csvRowNumber
			}
		}

		previewRow.Valid = len(previewRow.Errors) == 0
		rows = append(rows, previewRow)
	}

	if len(rows) == 0 {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The CSV file does not contain any games.",
		)
		return
	}

	// Check database duplicates for rows that passed initial validation.
	// Lookup failures become row errors instead of ending the preview.
	for i := range rows {
		if !rows[i].Valid {
			continue
		}

		exists, err := gc.Exists(
			rows[i].Data.Association,
			utils.ConvertInt64ToStr(rows[i].Data.GameId),
			tId,
		)
		if err != nil {
			log.Printf(
				"PreviewGamesImportHandler: duplicate lookup failed "+
					"for tenant %s, game %d: %v",
				tId,
				rows[i].Data.GameId,
				err,
			)

			rows[i].Errors = append(
				rows[i].Errors,
				"Unable to check whether the game already exists.",
			)
			rows[i].Valid = false
			continue
		}

		if exists {
			rows[i].Errors = append(
				rows[i].Errors,
				"Game already exists",
			)
			rows[i].Valid = false
		}
	}

	validRows := 0
	invalidRows := 0

	for _, row := range rows {
		if row.Valid {
			validRows++
		} else {
			invalidRows++
		}
	}

	previewToken, err := generatePreviewToken()
	if err != nil {
		log.Printf(
			"PreviewGamesImportHandler: unable to generate preview token: %v",
			err,
		)
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Unable to create the import preview.",
		)
		return
	}

	now := time.Now()

	preview := GamesImportPreview{
		Token:     previewToken,
		TenantID:  tId,
		Rows:      rows,
		CreatedAt: now,
		ExpiresAt: now.Add(previewExpiration),
	}

	saveGamesPreview(preview)
	removeExpiredGamesPreviews()

	response := GamesPreviewResponse{
		PreviewToken: previewToken,
		TotalRows:    len(rows),
		ValidRows:    validRows,
		InvalidRows:  invalidRows,
		Rows:         rows,
	}

	writeJSON(w, http.StatusOK, response)
}

func PreviewOfficialsImportHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	/* Limit the complete request body. The additional 1 MB allows room for the multipart boundary and HTTP form metadata. */

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+(oneMB))

	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The CSV upload is invalid or exceeds the 5 MB limit.")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "An Officials CSV file is required.")
		return
	}

	defer file.Close()

	if err := validateCSVFile(fileHeader); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	csvReader := csv.NewReader(file)

	/* Allow variable-length records so we can report a useful row-level error instead of stopping immediately. */
	csvReader.FieldsPerRecord = -1

	/* This trims spaces that appear outside quoted CSV values. For example: John, Smith is treated similarly to: John,Smith */
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err == io.EOF {
		writeJSONError(w, http.StatusBadRequest, "The CSV file is empty.")
		return
	}

	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Unable to read the CSV header: "+err.Error())
		return
	}

	if err := validateOfficialsCSVHeader(header); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows := make([]OfficialPreviewRow, 0)

	/* Used to detect duplicates inside the uploaded CSV file. */
	namesInFile := make(map[string]int)
	csvRowNumber := 1

	for {
		record, readErr := csvReader.Read()

		if readErr == io.EOF {
			break
		}

		csvRowNumber++
		if len(rows) >= maxCSVRows {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("The CSV file contains more than %d data rows.", maxCSVRows))
			return
		}

		if readErr != nil {
			rows = append(
				rows,
				OfficialPreviewRow{
					RowNumber: csvRowNumber,
					Valid:     false,
					Data:      OfficialImportData{},
					Errors: []string{
						"Unable to parse CSV row: " + readErr.Error(),
					},
				},
			)
			/* Some CSV syntax errors leave the reader unable to continue reliably. Stop after recording the error. */
			break
		}

		if isBlankCSVRecord(record) {
			continue
		}

		previewRow := buildOfficialPreviewRow(
			csvRowNumber,
			record,
		)

		nameKey := normalizeOfficialName(
			previewRow.Data.FirstName,
			previewRow.Data.LastName,
		)

		if nameKey != "|" {
			if previousRow, found := namesInFile[nameKey]; found {
				previewRow.Errors = append(
					previewRow.Errors,
					fmt.Sprintf(
						"Duplicate official in CSV file; first appeared on row %d",
						previousRow,
					),
				)
			} else {
				namesInFile[nameKey] = csvRowNumber
			}
		}

		previewRow.Valid = len(previewRow.Errors) == 0

		rows = append(rows, previewRow)
	}

	if len(rows) == 0 {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"The CSV file does not contain any officials.",
		)
		return
	}
	/* Check MongoDB for officials that already exist. This only runs for rows that passed the basic CSV validation. */

	for i := range rows {
		if !rows[i].Valid {
			continue
		}

		name := rows[i].Data.FirstName + " " + rows[i].Data.LastName

		exists, err := oc.Exists(name, tId)

		if err != nil {
			log.Printf(
				"PreviewOfficialsImportHandler: duplicate lookup failed for tenant %s, official %s %s: %v",
				tId,
				rows[i].Data.FirstName,
				rows[i].Data.LastName,
				err,
			)
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"Unable to validate officials against the database.",
			)
			return
		}

		if exists {
			rows[i].Errors = append(
				rows[i].Errors,
				"Official already exists",
			)
			rows[i].Valid = false
		}
	}

	validRows := 0
	invalidRows := 0

	for _, row := range rows {
		if row.Valid {
			validRows++
		} else {
			invalidRows++
		}
	}

	previewToken, err := generatePreviewToken()
	if err != nil {
		log.Printf(
			"PreviewOfficialsImportHandler: unable to generate preview token: %v",
			err,
		)
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Unable to create the import preview.",
		)
		return
	}

	now := time.Now()
	preview := OfficialsImportPreview{
		Token:     previewToken,
		TenantID:  tId,
		Rows:      rows,
		CreatedAt: now,
		ExpiresAt: now.Add(previewExpiration),
	}

	saveOfficialsPreview(preview)
	removeExpiredOfficialsPreviews()

	response := OfficialsPreviewResponse{
		PreviewToken: previewToken,
		TotalRows:    len(rows),
		ValidRows:    validRows,
		InvalidRows:  invalidRows,
		Rows:         rows,
	}

	writeJSON(w, http.StatusOK, response)
}

func GetExpenseTypes(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	expenseTypes := database.GetExpenseTypes()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(expenseTypes)

}

func GetStatusTypes(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	expenseTypes := database.GetStatusTypes()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(expenseTypes)

}
func GetOfficialsHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	officials, err := oc.GetOfficialsNames(tId)
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

func LogVisitor(r *http.Request) {

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
		protocol = r.Header.Get("X-Forwarded-Proto")
	}

	username := "anonymous"

	session, err := database.GetSession(r)
	if err == nil {
		username = session.Username
	}

	log.Printf(
		"User=%s IP=%s Method=%s Path=%s URL=%s Host=%s Protocol=%s UserAgent=%q Referer=%q",
		username,
		remoteIpAddr,
		method,
		path,
		url,
		host,
		protocol,
		userAgent,
		referer,
	)
}

//
// Obsolete:  Use the new Generate Report Functions instead.
//

// func generatePaymentsReport(assoc []string, tid string) []string {

// 	var rept []string = []string{}
// 	paymentRecords, err := database.QueryPayments(context.TODO(), database.GetMongoDbName(), "payments", assoc)
// 	if err != nil {
// 		rept = append(rept, "Error generating payment report.  Failed to retrieve payment records.")
// 		return rept
// 	}
// 	rept = reports.GeneratePaymentReport(paymentRecords, tid)
// 	return rept
// }

// func generateReconciliationReport(assoc []string, tid string) []string {

// 	var rept []string = []string{}

// 	paymentRecords, err := database.QueryPayments(context.TODO(), database.GetMongoDbName(), "payments", assoc)
// 	if err != nil {
// 		rept = append(rept, "Error generating reconciliation report.  Failed to retrieve payment records.")
// 		return rept
// 	}
// 	rept = reports.GenerateReconciliationReport(paymentRecords, tid)

// 	return rept
// }

// func generateAccountsReceivableReport(assoc []string, tid string) []string {

// 	var rept []string = []string{}
// 	rept = reports.GenerateAcctsRecvReport(context.TODO(), assoc, tid)
// 	return rept
// }

// func generateIncomeReport(assoc []string, tid string) []string {

// 	var rept []string = []string{}
// 	rept = reports.GenerateIncomeReport(assoc, tid)
// 	return rept
// }

// func generateExpenseReport(filter bson.M) []string {

// 	var rept []string = []string{}

// 	expenseRecords, err := database.QueryExpenses(context.TODO(), database.GetMongoDbName(), "expenses", filter)
// 	if err != nil {
// 		fmt.Println(err)
// 		return []string{}
// 	}
// 	rept = reports.GenerateExpenseReport(expenseRecords)

// 	return rept
// }

// func generateGamesReport(gameFilter bson.M) []string {
// 	// Implementation for generating games report

// 	gameRecords, err := database.QueryAggregatedGames(context.TODO(), database.GetMongoDbName(), "games", gameFilter)
// 	if err != nil {
// 		fmt.Println("Failed to query aggregated games")
// 		return []string{}
// 	}
// 	rept := reports.GenerateGameReport(gameRecords)

// 	return rept

// }

func GenerateGameReport(
	w http.ResponseWriter,
	r *http.Request,
) {
	fmt.Println("GenerateGameReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	tId, err := getTenantId(r)
	if err != nil {
		http.Error(
			w,
			"Invalid tenant ID",
			http.StatusBadRequest,
		)
		return
	}

	/*
		A request such as:

		/api/game-report?
			association=MSO&
			association=GOLLC&
			status=Pending&
			status=Completed

		produces:

		associations = []string{"MSO", "GOLLC"}
		statuses     = []string{"Pending", "Completed"}
	*/
	query := r.URL.Query()

	associations := query["association"]
	statuses := query["status"]
	home := strings.TrimSpace(r.URL.Query().Get("home"))
	visitor := strings.TrimSpace(r.URL.Query().Get("visitor"))
	sites := r.URL.Query()["site"]
	levels := r.URL.Query()["level"]
	sports := r.URL.Query()["sport"]
	assignors := r.URL.Query()["assignor"]
	officials := r.URL.Query()["official"]
	ecos := r.URL.Query()["eco"]
	taxyear := r.URL.Query().Get("taxYear")

	bDate, eDate, err := utils.FormatDateFilter(r.URL.Query().Get("begindate"), r.URL.Query().Get("enddate"))
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Begin Date:", bDate, "End Date:", eDate)

	filter := model.GameFilter{
		TenantId:    tId,
		Association: associations,
		Status:      statuses,
		Home:        home,
		Visitor:     visitor,
		Site:        sites,
		Level:       levels,
		Sport:       sports,
		Assignor:    assignors,
		Official:    officials,
		ECO:         ecos,
		BeginDate:   bDate,
		EndDate:     eDate,
		TaxYear:     taxyear,
	}

	fmt.Printf(
		"Game report filters — Associations: %v, Statuses: %v\n",
		associations,
		statuses,
	)

	gameReports, err := database.GetGameReport(filter)
	if err != nil {
		fmt.Printf(
			"GenerateGameReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate game report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println(
		"===== Game Report Returned",
		gameReports,
		"=====",
	)

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	if err := json.NewEncoder(w).Encode(gameReports); err != nil {
		fmt.Printf(
			"GenerateGameReport JSON encoding failed: %v\n",
			err,
		)
	}
}

func GenerateAccountsReceivableReport(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GenerateAccountsReceivableReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]

	acctsRecvReports, err :=
		database.GetAccountsReceivableReport(tId, associations)
	if err != nil {
		fmt.Printf(
			"GenerateAccountsReceivableReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate financial report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== Accounts Receivable Reports Returned", acctsRecvReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(acctsRecvReports); err != nil {
		fmt.Printf(
			"GenerateAccountsReceivableReport JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func GenerateRevenueReport(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GenerateReconciliationReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]
	taxYear := r.URL.Query().Get("taxYear")

	revenueReports, err :=
		database.GetRevenueReports(tId, associations, taxYear)
	if err != nil {
		fmt.Printf(
			"GenerateRevenueReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate revenue report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== Revenue Reports Returned", revenueReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(revenueReports); err != nil {
		fmt.Printf(
			"GenerateRevenueReport JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func GenerateReconciliationReport(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GenerateReconciliationReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]
	taxYear := r.URL.Query().Get("taxYear")

	acctsRecvReports, err :=
		database.GetReconciliationReports(tId, associations, taxYear)
	if err != nil {
		fmt.Printf(
			"GenerateReconciliationReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate reconciliation report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== Reconciliation Reports Returned", acctsRecvReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(acctsRecvReports); err != nil {
		fmt.Printf(
			"GenerateReconciliationReport JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func GenerateFinancialReport(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GenerateFinancialReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]
	taxyear := r.URL.Query().Get("taxYear")

	financialReports, err :=
		database.GetFinancialReports(tId, associations, taxyear)
	if err != nil {
		fmt.Printf(
			"GenerateFinancialReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate financial report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== Financial Reports Returned", financialReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(financialReports); err != nil {
		fmt.Printf(
			"GenerateFinancialReport JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func Generate1099Report(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("Generate1099Report is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]
	taxyear := r.URL.Query().Get("taxYear")

	expenseReports, err :=
		database.Get1099Reports(tId, associations, taxyear)
	if err != nil {
		fmt.Printf(
			"Generate1099Report failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate 1099 report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== 1099 Reports Returned", expenseReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(expenseReports); err != nil {
		fmt.Printf(
			"Generate1099Report JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func GenerateExpenseReport(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GenerateExpenseReport is called")
	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Supports:
	// ?association=MSO
	// ?association=MSO&association=GOLLC
	associations := r.URL.Query()["association"]
	taxyear := r.URL.Query().Get("taxYear")

	expenseReports, err :=
		database.GetExpenseReports(tId, associations, taxyear)
	if err != nil {
		fmt.Printf(
			"GenerateExpenseReport failed: %v\n",
			err,
		)

		http.Error(
			w,
			"Unable to generate expense report",
			http.StatusInternalServerError,
		)
		return
	}

	fmt.Println("===== Expense Reports Returned", expenseReports, "=====")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(expenseReports); err != nil {
		fmt.Printf(
			"GenerateExpenseReport JSON encoding failed: %v\n",
			err,
		)
		return
	}
}

func GenerateReport(w http.ResponseWriter, r *http.Request) {

	//
	// Obsolete
	// This function is no longer used.  It is replaced by the GenerateGameReport, GenerateExpenseReport,
	// GenerateIncomeReport, GeneratePaymentsReport, GenerateReconciliationReport, and GenerateAccountsReceivableReport functions.
	//

	// fmt.Println("GenerateReport is called")
	// LogVisitor(r)

	// var tId string
	// var err error
	// //var siteId string

	// gameFilters := model.GFilters{}
	// //expenseFilters := model.EFilters{}

	// rType := r.URL.Query().Get("type")
	// rEmail := r.URL.Query().Get("emailaddr")
	// rFile := r.URL.Query().Get("filename")
	// rGameIds := r.URL.Query().Get("gameids")

	// rSites := r.URL.Query()["site"]
	// rStatuses := r.URL.Query()["status"]
	// rAssociations := r.URL.Query()["association"]

	// rept := []string{}

	// if tId == "na" {
	// 	tId, err = getTenantId(r)

	// 	if err != nil {
	// 		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
	// 		return
	// 	}
	// }

	// // The HTML uses the site name, so we need to convert it to an ID
	// // if rSite != "" {
	// // 	siteId, err = sc.GetSiteId(rSite, tId)
	// // 	if err != nil {
	// // 		http.Error(w, "Invalid site ID", http.StatusBadRequest)
	// // 		return
	// // 	}
	// // }

	// if len(rGameIds) > 0 {
	// 	ids, err := utils.ConvertGameIdStrToInt(rGameIds)
	// 	if err != nil {
	// 		fmt.Println("Error:", err)
	// 		return
	// 	}
	// 	gameFilters.GameId, err = utils.ConvertGameIdIntToStr(ids)
	// 	if err != nil {
	// 		fmt.Println("Error:", err)
	// 		return
	// 	}
	// } else {
	// 	gameFilters.GameId = rGameIds
	// }

	// gameFilters.Association = ""
	// gameFilters.Status = ""
	// gameFilters.Site = ""
	// gameFilters.TenantId = tId
	// fmt.Println("Tenant ID:", tId, "Game Filters Tenant ID:", gameFilters.TenantId)

	// gameFilter := bson.M{}
	// expenseFilter := bson.M{}

	// if len(rStatuses) > 0 {
	// 	gameFilter["status"] = bson.M{
	// 		"$in": rStatuses,
	// 	}
	// }

	// if len(rAssociations) > 0 {
	// 	gameFilter["association"] = bson.M{
	// 		"$in": rAssociations,
	// 	}

	// 	expenseFilter["association"] = bson.M{
	// 		"$in": rAssociations,
	// 	}
	// }

	// if len(rSites) > 0 {
	// 	gameFilter["site"] = bson.M{
	// 		"$in": rSites,
	// 	}
	// }

	// switch rType {
	// case "Games":
	// 	rept = generateGamesReport(gameFilter)
	// case "Expenses":
	// 	rept = generateExpenseReport(expenseFilter)
	// case "Income":
	// 	rept = generateIncomeReport(rAssociations, tId)
	// case "Payments":
	// 	rept = generatePaymentsReport(rAssociations, tId)
	// case "Reconciliation":
	// 	rept = generateReconciliationReport(rAssociations, tId)
	// case "Accounts Receivable":
	// 	rept = generateAccountsReceivableReport(rAssociations, tId)
	// default:
	// 	w.Header().Set("Content-Type", "text/plain")
	// 	fmt.Fprint(w, "Invalid Report Type")
	// 	return
	// }

	// if rEmail != "" {
	// 	var email email.Email
	// 	email.Initialize()
	// 	if rFile == "" {
	// 		rFile = rType + "_report.pdf"
	// 	}
	// 	reports.WriteReportToFile(rept, rFile)
	// 	// Send report via email
	// 	email.SetSubject("Ref Ledger V2 Report")
	// 	email.SetBody("Please see the attached report.\n\nThanks!\n\nGenerated and Sent by Ref Ledger V2.0")
	// 	email.AddAttachment(rFile)
	// 	email.SetTo(strings.Split(rEmail, ","))
	// 	email.Send()
	// 	w.Header().Set("Content-Type", "text/plain")
	// 	fmt.Fprint(w, "Email sent")
	// } else {
	// 	w.Header().Set("Content-Type", "text/plain")
	// 	output := strings.Join(rept, "\n")

	// 	_, err := w.Write([]byte(output))
	// 	if err != nil {
	// 		fmt.Println(err)
	// 		fmt.Fprint(w, "Error generating report")
	// 	}
	// }

	http.Error(w, "Game not found", http.StatusNotFound)
}

func UpdateGame(w http.ResponseWriter, r *http.Request) {

	tId, err := getTenantId(r)
	if err != nil || tId == "" || tId == "na" {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var game Game
	var singleGameDesc model.GameDescriptor = model.GameDescriptor{}

	fmt.Println("##### UpdateGame Enpoint Called #####")

	err = json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("Updating game with game id", utils.ConvertIntToStr(game.GameId), "for association", game.Association)

	if utf8.RuneCountInString(game.Notes) > 150 {
		http.Error(w, "Notes cannot exceed 150 characters.", http.StatusBadRequest)
		return
	}

	fmt.Printf(
		"UpdateGame lookup: database=%q gameId=%d association=%q tenantId=%q\n",
		database.Database,
		game.GameId,
		game.Association,
		tId,
	)

	singleGameDesc, err = gc.Get(game.Association, utils.ConvertIntToStr(game.GameId), tId)
	if err != nil {
		fmt.Println(err)
		http.Error(w, "Game Not Found", http.StatusBadRequest)
		return
	}

	fmt.Println("UpdateGame: Game Fee:", game.GameFee)

	if game.Status == "Cancelled" {
		fmt.Println("UpdateGame: Game Status Cancelled.  Setting fees to zero.")
		game.GameFee = float64(0)
		game.TravelPay = float64(0)
		game.AssignorFee = float64(0)
		game.Deductions = float64(0)
	}

	singleGameDesc = GameDocToGameDescr(game)
	if game.Status == "Delete" {
		fmt.Println("UpdateGame: Game Status Delete.  Deleting game.")
		api.DelGame(context.TODO(), singleGameDesc.GameId, tId)
		return
	}

	fmt.Println("SaveGame: Single Game Descriptor:", singleGameDesc)

	err = api.ValidateGameDescriptor(
		r.Context(),
		singleGameDesc,
		false,
		tId,
	)

	if err != nil {
		fmt.Printf(
			"UpdateGame validation failed: gameId=%d sport=%q level=%q error=%v\n",
			game.GameId,
			game.Sport,
			game.Level,
			err,
		)
		http.Error(
			w,
			"Invalid Game: "+err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	err = database.UpdateOneGameDoc(
		r.Context(),
		singleGameDesc,
		database.Database,
		"games",
		tId,
	)
	if err != nil {
		fmt.Printf(
			"UpdateGame failed: database=%q gameId=%q association=%q tenantId=%q error=%v\n",
			database.Database,
			singleGameDesc.GameId,
			singleGameDesc.Association,
			tId,
			err,
		)
		http.Error(
			w,
			"Unable to update game",
			http.StatusInternalServerError,
		)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func SaveGame(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var game Game
	var gameDesc []model.GameDescriptor
	var singleGameDesc model.GameDescriptor = model.GameDescriptor{}

	fmt.Println("##### SaveGame Enpoint Called #####")

	err = json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if utf8.RuneCountInString(game.Notes) > 150 {
		http.Error(w, "Notes cannot exceed 150 characters.", http.StatusBadRequest)
		return
	}

	fmt.Println("UpdateGame: Game Fee:", game.GameFee)

	if game.Status == "Cancelled" {
		fmt.Println("UpdateGame: Game Status Cancelled.  Setting fees to zero.")
		game.GameFee = float64(0)
		game.TravelPay = float64(0)
		game.AssignorFee = float64(0)
		game.Deductions = float64(0)
	}

	singleGameDesc = GameDocToGameDescr(game)

	if game.Status == "Delete" {
		fmt.Println("UpdateGame: Game Status Delete.  Deleting game.")
		api.DelGame(context.TODO(), singleGameDesc.GameId, tId)
		return
	}

	fmt.Println("SaveGame: Single Game Descriptor:", singleGameDesc)
	var gDoc model.GameDoc = model.GameDoc{}

	gDoc.GameId = int64(game.GameId)
	gDoc.Association = game.Association

	checkForDup := true
	err = api.ValidateGameDescriptor(context.TODO(), singleGameDesc, checkForDup, tId)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	gameExists, err := database.GameExists(gDoc)

	if err != nil {
		fmt.Println(err)
		return
	}

	//
	// We shouldn't update game if it already exists.  We should return an error to the user.
	//

	if gameExists {
		http.Error(w, "Game already exists", http.StatusBadRequest)
		return
	}

	gameDesc = append(gameDesc, singleGameDesc)
	database.InsertGameDocs(context.TODO(), gameDesc, database.Database, "games", tId)

}

func UpdateGameStatus(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("##### Updating Game Status #####")

	LogVisitor(r)

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var gameUpdate GameStatusUpdate

	err = json.NewDecoder(r.Body).Decode(&gameUpdate)
	if err != nil {
		fmt.Println("err:", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if gameUpdate.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	fmt.Println("##### Game Status to be set to:", gameUpdate.Status, "#####")
	var gameIds []int64

	// Supports dashboard JSON:
	// { "gameIds": [101, 102], "status": "Completed" }
	err = json.Unmarshal(gameUpdate.GameIds, &gameIds)

	if err != nil {
		// Supports existing gameStatus.html JSON:
		// { "gameIds": "101,102", "status": "Completed" }
		var gameIdString string

		err = json.Unmarshal(gameUpdate.GameIds, &gameIdString)
		if err != nil {
			http.Error(w, "invalid gameIds", http.StatusBadRequest)
			return
		}

		gameIds, err = utils.ConvertGameIdStrToInt(gameIdString)
		if err != nil {
			fmt.Println("err:", err)
			http.Error(w, "invalid gameIds", http.StatusBadRequest)
			return
		}
	}

	if len(gameIds) == 0 {
		http.Error(w, "no game IDs supplied", http.StatusBadRequest)
		return
	}

	fmt.Println("##### Game Ids selected", gameIds, "#####")

	gc.UpdateGameStatus(gameIds, gameUpdate.Status, tId)

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Game status updated successfully"))
}

func CreatePayment(
	w http.ResponseWriter,
	r *http.Request,
) {
	var payment Payment

	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	err = json.NewDecoder(r.Body).Decode(&payment)
	if err != nil {
		fmt.Println("Invalid JSON. Error:", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if payment.TaxYear < 1000 || payment.TaxYear > 9999 {
		http.Error(
			w,
			"Tax Year must be between 1000 and 9999.",
			http.StatusBadRequest,
		)
		return
	}

	singlePayment := PaymentDocToPaymentDescr(payment)
	singlePayment.TaxYear = strconv.Itoa(payment.TaxYear)

	paymentDescr := []model.PaymentDescriptor{
		singlePayment,
	}

	fmt.Println("Payment Descr:", singlePayment)

	totalAdded, totalErrors, totalUpdatedToPaid, insertErrors :=
		database.InsertPaymentDocs(
			r.Context(),
			paymentDescr,
			database.Database,
			"payments",
			tId,
		)

	errorMessages := make([]string, 0, len(insertErrors))

	for _, insertError := range insertErrors {
		errorMessages = append(
			errorMessages,
			insertError.Error(),
		)
	}

	response := map[string]interface{}{
		"totalAdded":         totalAdded,
		"totalErrors":        totalErrors,
		"totalUpdatedToPaid": totalUpdatedToPaid,
		"errors":             errorMessages,
	}

	w.Header().Set("Content-Type", "application/json")

	if totalErrors > 0 {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusCreated)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Println(
			"Unable to encode payment response. Error:",
			err,
		)
	}
}

func UpdatePayment(
	w http.ResponseWriter,
	r *http.Request,
) {
	var payment Payment

	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	err = json.NewDecoder(r.Body).Decode(&payment)
	if err != nil {
		fmt.Println("Invalid JSON. Error:", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if payment.TaxYear < 1000 || payment.TaxYear > 9999 {
		http.Error(
			w,
			"Tax Year must be between 1000 and 9999.",
			http.StatusBadRequest,
		)
		return
	}

	singlePayment := PaymentDocToPaymentDescr(payment)
	singlePayment.TaxYear = strconv.Itoa(payment.TaxYear)

	paymentDescr := []model.PaymentDescriptor{
		singlePayment,
	}

	fmt.Println("Payment Descr:", singlePayment)

	totalUpdated, totalErrors, totalUpdatedToPaid, updateErrors :=
		database.UpdatePaymentDocs(
			r.Context(),
			paymentDescr,
			database.Database,
			"payments",
			tId,
		)

	errorMessages := make([]string, 0, len(updateErrors))

	for _, updateError := range updateErrors {
		errorMessages = append(
			errorMessages,
			updateError.Error(),
		)
	}

	response := map[string]interface{}{
		"totalUpdated":       totalUpdated,
		"totalErrors":        totalErrors,
		"totalUpdatedToPaid": totalUpdatedToPaid,
		"errors":             errorMessages,
	}

	w.Header().Set("Content-Type", "application/json")

	if totalErrors > 0 {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Println(
			"Unable to encode payment response. Error:",
			err,
		)
	}
}

func DeletePayment(w http.ResponseWriter, r *http.Request) {

	paymentID := strings.TrimSpace(
		r.URL.Query().Get("paymentId"),
	)

	if paymentID == "" {
		http.Error(
			w,
			"paymentId is required",
			http.StatusBadRequest,
		)
		return
	}

	association := strings.TrimSpace(
		r.URL.Query().Get("association"),
	)

	if association == "" {
		http.Error(
			w,
			"association is required",
			http.StatusBadRequest,
		)
		return
	}

	tid, err := getTenantId(r)
	if err != nil {
		http.Error(
			w,
			"Failed to retrieve tenant ID",
			http.StatusInternalServerError,
		)
		return
	}

	err = database.DeletePayment(paymentID, association, tid)
	if err != nil {
		http.Error(
			w,
			"Failed to delete payment",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Payment deleted successfully"))
}

func ParseGameIds(value string) ([]int64, error) {
	value = strings.TrimSpace(value)

	if value == "" {
		return nil, nil
	}

	gameIds := make([]int64, 0)
	seen := make(map[int64]struct{})

	parts := strings.Split(value, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)

		if part == "" {
			return nil, fmt.Errorf(
				"game IDs cannot contain empty values",
			)
		}

		/*
			A hyphen indicates a range, such as 1992-1996.
		*/
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")

			if len(rangeParts) != 2 {
				return nil, fmt.Errorf(
					"invalid game ID range %q",
					part,
				)
			}

			firstId, err := strconv.ParseInt(
				strings.TrimSpace(rangeParts[0]),
				10,
				64,
			)
			if err != nil || firstId <= 0 {
				return nil, fmt.Errorf(
					"invalid starting game ID in range %q",
					part,
				)
			}

			lastId, err := strconv.ParseInt(
				strings.TrimSpace(rangeParts[1]),
				10,
				64,
			)
			if err != nil || lastId <= 0 {
				return nil, fmt.Errorf(
					"invalid ending game ID in range %q",
					part,
				)
			}

			if firstId > lastId {
				return nil, fmt.Errorf(
					"game ID range %q must be in ascending order",
					part,
				)
			}

			for gameId := firstId; gameId <= lastId; gameId++ {
				if _, exists := seen[gameId]; exists {
					continue
				}

				seen[gameId] = struct{}{}
				gameIds = append(gameIds, gameId)
			}

			continue
		}

		gameId, err := strconv.ParseInt(
			part,
			10,
			64,
		)
		if err != nil || gameId <= 0 {
			return nil, fmt.Errorf(
				"invalid game ID %q",
				part,
			)
		}

		if _, exists := seen[gameId]; exists {
			continue
		}

		seen[gameId] = struct{}{}
		gameIds = append(gameIds, gameId)
	}

	return gameIds, nil
}

func LoadPaymentRegistry(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	paymentId := strings.TrimSpace(r.URL.Query().Get("paymentId"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	taxYear := strings.TrimSpace(r.URL.Query().Get("taxYear"))

	// A blank tax year includes all years.
	if taxYear != "" {
		year, err := strconv.Atoi(taxYear)
		if err != nil || year < 1000 || year > 9999 {
			http.Error(
				w,
				"Tax Year must be between 1000 and 9999, or blank for all years.",
				http.StatusBadRequest,
			)
			return
		}
	}

	if date != "" {
		parsedDate, err := time.Parse("2006-01-02", date)
		if err != nil {
			http.Error(
				w,
				"Date must be a valid date.",
				http.StatusBadRequest,
			)
			return
		}

		date = fmt.Sprintf(
			"%d/%d/%d",
			int(parsedDate.Month()),
			parsedDate.Day(),
			parsedDate.Year(),
		)
	}

	association := strings.TrimSpace(r.URL.Query().Get("association"))
	amountText := strings.TrimSpace(r.URL.Query().Get("amount"))
	gameIdsText := strings.TrimSpace(r.URL.Query().Get("gameIdFilter"))

	gameIds, err := ParseGameIds(gameIdsText)
	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	var amount int64

	if amountText != "" {
		parsedAmount, err := strconv.ParseFloat(amountText, 64)
		if err != nil {
			http.Error(
				w,
				"Amount must be a valid dollar amount.",
				http.StatusBadRequest,
			)
			return
		}

		if parsedAmount < 0 {
			http.Error(
				w,
				"Amount cannot be negative.",
				http.StatusBadRequest,
			)
			return
		}

		amount = int64(math.Round(parsedAmount * 100))
	}

	registryFilter := model.PaymentRegistryFilter{
		PaymentId:   paymentId,
		Date:        date,
		Association: association,
		Amount:      amount,
		GameIds:     gameIds,
		TenantId:    tId,
		TaxYear:     taxYear,
	}

	fmt.Println("Payment Filter:", registryFilter)
	payments, err := database.GetPaymentRegistry(registryFilter)
	fmt.Println("Payments:", payments, " Error:", err)
	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(payments); err != nil {
		fmt.Println(
			"Failed to encode payment registry response:",
			err,
		)
	}
}

func LoadExpenseRegistry(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	expenseId := strings.TrimSpace(
		r.URL.Query().Get("expenseId"),
	)

	expenseType := strings.TrimSpace(
		r.URL.Query().Get("expenseType"),
	)

	date := strings.TrimSpace(
		r.URL.Query().Get("date"),
	)

	if date != "" {
		parsedDate, err := time.Parse(
			"2006-01-02",
			date,
		)
		if err != nil {
			http.Error(
				w,
				"Date must be a valid date.",
				http.StatusBadRequest,
			)
			return
		}

		date = fmt.Sprintf(
			"%d/%d/%d",
			int(parsedDate.Month()),
			parsedDate.Day(),
			parsedDate.Year(),
		)
	}

	association := strings.TrimSpace(
		r.URL.Query().Get("association"),
	)

	taxYear := strings.TrimSpace(
		r.URL.Query().Get("taxYear"),
	)

	description := strings.TrimSpace(
		r.URL.Query().Get("description"),
	)

	amountText := strings.TrimSpace(
		r.URL.Query().Get("amount"),
	)

	var amount int64

	if amountText != "" {
		parsedAmount, err := strconv.ParseFloat(
			amountText,
			64,
		)

		if err != nil {
			http.Error(
				w,
				"Amount must be a valid dollar amount.",
				http.StatusBadRequest,
			)
			return
		}

		if parsedAmount < 0 {
			http.Error(
				w,
				"Amount cannot be negative.",
				http.StatusBadRequest,
			)
			return
		}

		amount = int64(math.Round(parsedAmount * 100))
	}

	expenseFilter := model.ExpenseRegistryFilter{
		ExpenseId:   expenseId,
		Date:        date,
		Association: association,
		Type:        expenseType,
		Amount:      amount,
		Description: description,
		TenantId:    tId,
		TaxYear:     taxYear,
	}

	fmt.Println("Expense Filter:", expenseFilter)
	expenses, err := database.GetExpenseRegistry(expenseFilter)
	fmt.Println("Expenses:", expenses, " Error:", err)
	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(expenses); err != nil {
		fmt.Println(
			"Failed to encode expense registry response:",
			err,
		)
	}

}

func ValidateLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var user model.User
	sessionDuration := 15 * time.Minute

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")

	usersCollection := database.Client.
		Database(database.GetMongoDbName()).
		Collection("users")

	err := usersCollection.FindOne(
		r.Context(),
		bson.M{"username": username},
	).Decode(&user)

	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	err = bcrypt.CompareHashAndPassword(
		[]byte(user.PasswordHash),
		[]byte(password),
	)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	sessionID := uuid.New().String()

	session := model.Session{
		SessionID: sessionID,
		Username:  user.Username,
		TenantID:  user.TenantID,
		ExpiresAt: time.Now().Add(sessionDuration),
		Role:      user.Role,
	}

	_, err = database.Client.
		Database(database.GetMongoDbName()).
		Collection("sessions").
		InsertOne(r.Context(), session)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "rl_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionDuration.Seconds()),
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusOK)
}

func UpdateAssociation(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var assocJson database.AssociationJson

	err = json.NewDecoder(r.Body).Decode(&assocJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)

		return
	}

	err = ac.Update(ac.ConvAssocJsonToAssoc(assocJson), tId)
	if err != nil {
		fmt.Println("Failed to update association")
		http.Error(w, "Failed to update association", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Association updated successfully"))

}

func CreateAssociation(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var assocJson database.AssociationJson

	err = json.NewDecoder(r.Body).Decode(&assocJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)

		return
	}

	err = ac.Add(ac.ConvAssocJsonToAssoc(assocJson), tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, "Association added successfully.")
}

func CreateSite(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var siteJson database.SiteJson

	err = json.NewDecoder(r.Body).Decode(&siteJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	err = sc.Add(sc.ConvJsonToSite(siteJson), tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, "Site saved successfully.")
}

func UpdateSite(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var siteJson database.SiteJson

	err = json.NewDecoder(r.Body).Decode(&siteJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	err = sc.Update(sc.ConvJsonToSite(siteJson), tId)
	if err != nil {
		fmt.Println("Failed to update site")
		http.Error(w, "Failed to update site", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Site updated successfully"))
}

func UpdateOfficial(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	fmt.Println("Updating Official")
	var officialJson database.OfficialJson
	err = json.NewDecoder(r.Body).Decode(&officialJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("Official JSON:", officialJson)

	err = oc.Update(tId, oc.ConvJsonToOfficial(officialJson))
	if err != nil {
		fmt.Println("Failed to update official")
		http.Error(w, "Failed to update official", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Official updated successfully"))
}

func CreateOfficial(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	fmt.Println("Adding Official")

	var officialJson database.OfficialJson
	err = json.NewDecoder(r.Body).Decode(&officialJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	err = oc.Add(oc.ConvJsonToOfficial(officialJson), tId)
	if err != nil {
		fmt.Println("Failed to create official:", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, "Official created successfully")
}

type DeleteExpenseRequest struct {
	ExpenseId   string `json:"expenseId"`
	Association string `json:"association"`
}

func DeleteExpenseHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	if r.Method != http.MethodDelete {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var request DeleteExpenseRequest

	err = json.NewDecoder(r.Body).Decode(&request)
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(
			w,
			"Invalid delete expense request",
			http.StatusBadRequest,
		)
		return
	}

	// Fall back to URL parameters when no JSON value was provided.
	if strings.TrimSpace(request.ExpenseId) == "" {
		request.ExpenseId = r.URL.Query().Get("expenseId")
	}

	if strings.TrimSpace(request.Association) == "" {
		request.Association = r.URL.Query().Get("association")
	}

	request.ExpenseId = strings.TrimSpace(request.ExpenseId)
	request.Association = strings.TrimSpace(request.Association)
	tId = strings.TrimSpace(tId)

	fmt.Printf(
		"===== Attempting to delete Expense Id %s "+
			"for Association %s and tenant id %s =====\n",
		request.ExpenseId,
		request.Association,
		tId,
	)

	if request.ExpenseId == "" {
		http.Error(
			w,
			"Delete failed: expense ID is required",
			http.StatusBadRequest,
		)
		return
	}

	if request.Association == "" {
		http.Error(
			w,
			"Delete failed: association is required",
			http.StatusBadRequest,
		)
		return
	}

	err = ec.Delete(
		request.ExpenseId,
		request.Association,
		tId,
	)
	if err != nil {
		http.Error(
			w,
			"Delete failed: "+err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(
		w,
		"Expense %s deleted successfully.",
		request.ExpenseId,
	)
}

func UpdateExpense(w http.ResponseWriter, r *http.Request) {
	tenantId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	if r.Method != http.MethodPut {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	expenseId := strings.TrimSpace(
		r.URL.Query().Get("expenseId"),
	)

	association := strings.TrimSpace(
		r.URL.Query().Get("association"),
	)

	if expenseId == "" {
		http.Error(
			w,
			"Expense ID is required",
			http.StatusBadRequest,
		)
		return
	}

	if association == "" {
		http.Error(
			w,
			"Association is required",
			http.StatusBadRequest,
		)
		return
	}

	var expenseJSON database.ExpenseJson

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&expenseJSON); err != nil {
		fmt.Println("JSON error:", err)

		http.Error(
			w,
			"Invalid JSON: "+err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	fmt.Printf(
		"Updating Expense ID %s for Association %s and Tenant ID %s\n",
		expenseId,
		association,
		tenantId,
	)

	expense := ec.ConvJsonToExpense(expenseJSON)

	err = ec.Update(
		expense,
		expenseId,
		association,
		tenantId,
	)
	if err != nil {
		fmt.Println("Failed to update expense:", err)

		// If Update returns a typed or sentinel not-found error,
		// this can instead return http.StatusNotFound.
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(
		[]byte("Expense updated successfully"),
	)
}

func CreateExpense(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	var expenseJson database.ExpenseJson

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return
	}

	fmt.Println("Request Body:")
	fmt.Println(string(body))

	if err := json.Unmarshal(body, &expenseJson); err != nil {
		fmt.Println("JSON error:", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	err = ec.Add(ec.ConvJsonToExpense(expenseJson), tId)
	if err != nil {
		fmt.Println("Failed to create expense")
		http.Error(w, "Failed to create expense", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Expense added successfully"))
}

func DeleteAssociation(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	fmt.Println("DeleteAssociation called")

	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	assocId := r.PathValue("assocId")

	err = ac.Delete(assocId, tId)

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

	var err error

	LogVisitor(r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	association := r.PathValue("association")
	gameId := r.PathValue("gameId")

	fmt.Println("Deleting Game", gameId, "for association", association)

	err = gc.Delete(association, gameId, tId)

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

func DeleteOfficial(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	firstname := r.PathValue("firstName")
	lastname := r.PathValue("lastName")

	fmt.Println("Deleting Official", firstname, lastname)

	err = oc.Delete(firstname, lastname, tId)

	if err != nil {
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func OfficialsDetailHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	officialId := r.PathValue("officialId")

	fmt.Println("Retrieving Official Details for official id", officialId)
	id, err := utils.ConvertStrToInt64(officialId)

	if err != nil {
		http.Error(w, "Invalid request data", http.StatusBadRequest)
		return
	}
	official, err := oc.GetById(id, tId)

	if err != nil {
		http.Error(w, "Invalid request data", http.StatusBadRequest)
		return
	}

	officialDetails := model.OfficialDetailsView{
		OfficialId: official.Id,
		Name:       official.FirstName + " " + official.LastName,
		Phone:      official.Phone,
		Email:      official.Email,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(officialDetails)
	if err != nil {
		http.Error(
			w,
			"Unable to encode official details",
			http.StatusInternalServerError,
		)
		return
	}

}

func DeleteSite(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	siteId := r.PathValue("siteId")

	fmt.Println("Deleting Site", siteId)

	err = sc.Delete(siteId, tId)

	if err != nil {
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func GetOfficial(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	official, err := oc.Get(r.PathValue("firstName"), r.PathValue("lastName"), tId)
	if err != nil {
		http.Error(w, "Official not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(official)
}

func GetSingleAssociation(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	assoc, err := ac.Get(r.PathValue("assocId"), tId)
	if err != nil {
		http.Error(w, "Association not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assoc)
}

func LoadSportsSupportedHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sports, err := uc.GetSportsSupported(tId)
	if err != nil {
		http.Error(w, "Failed to retrieve sports supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sports)
}

func SaveSportsSupportedHandler(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var sports []string

	if err := json.NewDecoder(r.Body).Decode(&sports); err != nil {
		http.Error(w, "Invalid sports array", http.StatusBadRequest)
		return
	}

	if sports == nil {
		http.Error(w, "A sports array is required", http.StatusBadRequest)
		return
	}

	if err := uc.SetSportsSupported(tId, sports); err != nil {
		http.Error(
			w,
			"Failed to save supported sports",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Supported sports saved.",
	})
}

func GetSingleSite(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	site, err := sc.Get(r.PathValue("siteId"), tId)
	if err != nil {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(site)
}

func GetSingleGame(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tId, err := getTenantId(r)
	if err != nil {
		http.Error(
			w,
			"Invalid tenant ID",
			http.StatusBadRequest,
		)
		return
	}

	association := r.PathValue("association")
	gameID := r.PathValue("gameid")

	game, err := database.GetGameByGameIdAndAssoc(association, gameID, tId)
	if err != nil {
		http.Error(w, "Game not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(game)

}

func GetPendingGamesCount(w http.ResponseWriter, r *http.Request) {
	tId, err := getTenantId(r)
	if err != nil {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	fmt.Println("GetPendingGamesCount has been called")

	w.Header().Set("Content-Type", "application/json")

	// Use your application's tenant ID.

	_, todaysCount, err := gc.GetTodaysPendingGames(tId)

	if err != nil {
		http.Error(
			w,
			`{"success":false,"message":"Failed to retrieve today's pending games"}`,
			http.StatusInternalServerError,
		)
		return
	}

	_, tomorrowsCount, err := gc.GetTomorrowsPendingGames(tId)

	if err != nil {
		http.Error(
			w,
			`{"success":false,"message":"Failed to retrieve tomorrow's pending games"}`,
			http.StatusInternalServerError,
		)
		return
	}

	_, sevenDayCount, err := gc.Get7DayPendingGames(tId)

	if err != nil {
		http.Error(
			w,
			`{"success":false,"message":"Failed to retrieve next 7 day's pending games"}`,
			http.StatusInternalServerError,
		)
		return
	}

	response := struct {
		Success        bool `json:"success"`
		TodaysCount    int  `json:"todaysCount"`
		TomorrowsCount int  `json:"tomorrowsCount"`
		SevenDayCount  int  `json:"sevenDayCount"`
	}{
		Success:        true,
		TodaysCount:    todaysCount,
		TomorrowsCount: tomorrowsCount,
		SevenDayCount:  sevenDayCount,
	}

	fmt.Println("Response Success:", response.Success, "Todays Count:", response.TodaysCount, "Tomorrows Count:", response.TomorrowsCount, "Seven Day Count:", response.SevenDayCount)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode pending game count: %v\n", err)
	}
}

func SortGamesByDateTime(games []model.HtmlResponse) ([]model.HtmlResponse, error) {

	const dateTimeLayout = "1/2/2006 3:04 PM"

	// Copy the slice so the original order is unchanged.
	sortedGames := append([]model.HtmlResponse(nil), games...)

	dateTimes := make(map[int64]time.Time, len(sortedGames))

	for _, game := range sortedGames {
		dateTimeValue := game.Date + " " + game.Time

		parsedDateTime, err := time.Parse(
			dateTimeLayout,
			dateTimeValue,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid date/time for game %d (%q): %w",
				game.GameId,
				dateTimeValue,
				err,
			)
		}

		dateTimes[game.GameId] = parsedDateTime
	}

	sort.SliceStable(
		sortedGames,
		func(i, j int) bool {
			firstDateTime := dateTimes[sortedGames[i].GameId]
			secondDateTime := dateTimes[sortedGames[j].GameId]

			if firstDateTime.Equal(secondDateTime) {
				return sortedGames[i].GameId < sortedGames[j].GameId
			}

			return firstDateTime.Before(secondDateTime)
		},
	)

	return sortedGames, nil
}

func GetGames(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type dashboardGameView struct {
		model.GameView
		Officials []model.GameOfficialDoc `json:"Officials"`
	}

	var games []model.HtmlResponse
	gameViews := make([]dashboardGameView, 0)
	gameFilters := model.GFilters{}

	var totals reports.AssocGameTotalsMap
	totals.Init()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tId, err := getTenantId(r)
	if err != nil || tId == "" || tId == "na" {
		http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
		return
	}

	query := r.URL.Query()

	cleanValues := func(values []string) []string {
		result := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				result = append(result, value)
			}
		}
		return result
	}

	statuses := cleanValues(query["status"])
	associations := cleanValues(query["association"])
	sports := cleanValues(query["sport"])
	levels := cleanValues(query["level"])
	sites := cleanValues(query["site"])
	ecos := cleanValues(query["eco"])
	assignors := cleanValues(query["assignor"])
	officials := cleanValues(query["official"])

	begindate := strings.TrimSpace(query.Get("begindate"))
	enddate := strings.TrimSpace(query.Get("enddate"))
	gameId := strings.TrimSpace(query.Get("gameId"))
	home := query.Get("home")
	visitor := query.Get("visitor")

	taxYear := strings.TrimSpace(query.Get("taxYear"))

	if taxYear != "" {
		valid := len(taxYear) == 4

		for _, digit := range taxYear {
			if digit < '0' || digit > '9' {
				valid = false
				break
			}
		}

		if !valid || taxYear < "1000" || taxYear > "9999" {
			http.Error(
				w,
				"Tax Year must be a four-digit year between 1000 and 9999.",
				http.StatusBadRequest,
			)
			return
		}
	}

	// Convert "today" to the date format expected by FormatDateFilter.
	if begindate == "today" || enddate == "today" {
		location, err := time.LoadLocation("America/New_York")
		if err != nil {
			http.Error(w, "Unable to determine today's date", http.StatusInternalServerError)
			return
		}

		today := time.Now().In(location).Format("2006-01-02")

		if begindate == "today" {
			begindate = today
			if enddate == "" {
				enddate = today
			}
		}

		if enddate == "today" {
			enddate = today
		}
	}

	for i, status := range statuses {
		runes := []rune(status)
		runes[0] = unicode.ToUpper(runes[0])
		statuses[i] = string(runes)
	}

	bDate, eDate, err := utils.FormatDateFilter(begindate, enddate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if gameId != "" {
		ids, err := utils.ConvertGameIdStrToInt(gameId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		gameFilters.GameId, err = utils.ConvertGameIdIntToStr(ids)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	gameFilters.FromDate = bDate
	gameFilters.ToDate = eDate
	gameFilters.TenantId = tId
	gameFilters.Home = home
	gameFilters.Visitor = visitor

	gfilter, err := utils.ConvertGameFiltersToJsonFile(gameFilters)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mongoDbFilter, err := database.BuildMongoGameFilterFromFile(gfilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if mongoDbFilter == nil {
		mongoDbFilter = bson.M{}
	}

	mongoDbFilter["tenantId"] = tId

	if taxYear != "" {
		mongoDbFilter["taxYear"] = taxYear
	}

	if len(statuses) > 0 {
		mongoDbFilter["status"] = bson.M{"$in": statuses}
	}

	if len(associations) > 0 {
		mongoDbFilter["association"] = bson.M{"$in": associations}
	}

	if len(sports) > 0 {
		mongoDbFilter["sport"] = bson.M{"$in": sports}
	}

	if len(levels) > 0 {
		mongoDbFilter["level"] = bson.M{"$in": levels}
	}

	extraConditions := bson.A{}

	if len(officials) > 0 {
		extraConditions = append(extraConditions, bson.M{
			"officials": bson.M{
				"$elemMatch": bson.M{
					"name":  bson.M{"$in": officials},
					"title": bson.M{"$ne": "ECO"},
				},
			},
		})
	}

	if len(ecos) > 0 {
		extraConditions = append(extraConditions, bson.M{
			"officials": bson.M{
				"$elemMatch": bson.M{
					"title": "ECO",
					"name":  bson.M{"$in": ecos},
				},
			},
		})
	}

	if len(assignors) > 0 {
		mongoDbFilter["assignor"] = bson.M{"$in": assignors}
	}

	if len(sites) > 0 {
		mongoDbFilter["site"] = bson.M{"$in": sites}
	}

	if len(extraConditions) > 0 {
		conditions := bson.A{mongoDbFilter}
		conditions = append(conditions, extraConditions...)
		mongoDbFilter = bson.M{"$and": conditions}
	}

	coll := database.Client.
		Database(database.Database).
		Collection("games")

	opts := options.Find().SetSort(bson.D{
		{Key: "gameDateTime", Value: 1},
	})

	cursor, err := coll.Find(ctx, mongoDbFilter, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	if err := cursor.All(ctx, &games); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(games) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)

		if err := json.NewEncoder(w).Encode(map[string]string{
			"message": "No games were found matching your search criteria.",
		}); err != nil {
			log.Printf("Unable to return empty dashboard response: %v", err)
		}
		return
	}

	sortedGames, err := SortGamesByDateTime(games)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, game := range sortedGames {
		gameRec := model.GameDescriptor{
			GameFee:     utils.ConvertInt64ToAmtStr(game.GameFee),
			NumOfGames:  utils.ConvertInt64ToStr(game.NumOfGames),
			TravelPay:   utils.ConvertInt64ToAmtStr(game.TravelPay),
			Deductions:  utils.ConvertInt64ToAmtStr(game.Deductions),
			AssignorFee: utils.ConvertInt64ToAmtStr(game.AssignorFee),
		}

		gameFee := utils.CalculateGameFee(gameRec)

		totals.Update(
			game.Association,
			game.Status,
			game.NumOfGames,
			gameFee,
		)

		view := model.GameView{
			GameId:      game.GameId,
			Time:        game.Time,
			Sport:       game.Sport,
			Site:        game.Site,
			Field:       game.Field,
			NumOfGames:  game.NumOfGames,
			Level:       game.Level,
			Home:        game.Home,
			Visitor:     game.Visitor,
			Status:      game.Status,
			Association: game.Association,
			Assignor:    game.Assignor,
			TaxYear:     game.TaxYear,
		}

		view.GameFee = fmt.Sprintf("$%.2f", float64(gameFee)/100)
		view.Date = fmt.Sprintf(
			"%s (%s)",
			game.Date,
			utils.DayOfWeekAbbreviation(game.Date),
		)

		assignments := make(
			[]model.GameOfficialDoc,
			len(game.Officials),
		)
		copy(assignments, game.Officials)

		for i := range assignments {
			assignment := &assignments[i]
			assignment.Name = strings.TrimSpace(assignment.Name)

			if assignment.Name == "" {
				if assignment.OfficialId != "" {
					assignment.Name = "Official " + assignment.OfficialId
				} else {
					assignment.Name = "Unassigned"
				}
			}
		}

		sort.SliceStable(assignments, func(i, j int) bool {
			return assignments[i].DisplayOrder < assignments[j].DisplayOrder
		})

		gameViews = append(gameViews, dashboardGameView{
			GameView:  view,
			Officials: assignments,
		})
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(gameViews); err != nil {
		log.Printf("Unable to return dashboard games: %v", err)
	}
}

func isAuthenticated(r *http.Request) bool {

	cookie, err := r.Cookie("rl_session")
	if err != nil {
		return false
	}

	sessionID := cookie.Value

	collection := database.Client.
		Database(database.GetMongoDbName()).
		Collection("sessions")

	var session model.Session

	err = collection.
		FindOne(r.Context(), bson.M{
			"sessionId": sessionID,
			"expiresAt": bson.M{"$gt": time.Now()},
		}).
		Decode(&session)

	return err == nil
}

func authRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path == "/login" || r.URL.Path == "/api/login" {
			next(w, r)
			return
		}

		if !isAuthenticated(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next(w, r)
	}
}

func Logout(w http.ResponseWriter, r *http.Request) {

	cookie, err := r.Cookie("rl_session")
	if err == nil {

		database.Client.
			Database(database.GetMongoDbName()).
			Collection("sessions").
			DeleteOne(r.Context(),
				bson.M{"sessionId": cookie.Value})
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "rl_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	w.WriteHeader(http.StatusOK)
}

func EmailReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		fail(http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	// Read the tenant from this request's session, never the global development tenant.
	session, err := database.GetSession(r)
	if err != nil || session == nil || session.TenantID == "" || session.TenantID == "na" {
		fail(http.StatusUnauthorized, "Please sign in again")
		return
	}
	if session.Role == "readonly" {
		fail(http.StatusForbidden, "Readonly users cannot email reports")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request database.EmailReportRequest
	if err := decoder.Decode(&request); err != nil {
		fail(http.StatusBadRequest, "Invalid report email request")
		return
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(http.StatusBadRequest, "Request must contain one JSON object")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	emailID, err := database.EmailReport(ctx, session.TenantID, request)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrInvalidEmailReport):
			fail(http.StatusBadRequest, err.Error())
		case errors.Is(err, database.ErrNoEmailReportData):
			fail(http.StatusBadRequest, "No records matched the selected report filters")
		default:
			log.Printf("EmailReport failed: %v", err)
			fail(http.StatusBadGateway, "Unable to email the report. Check the server log for details. A timeout can occur after acceptance; check Resend before retrying.")
		}
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Report submitted to Resend successfully. Delivery may take a moment.",
		"emailId": emailID,
	})
}

func OfficialRolesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		sport := r.URL.Query().Get("sport")

		// tid comes from the authenticated session.
		response, err := database.GetOfficialRoles(sport)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			fmt.Printf("Unable to return official roles: %v\n", err)
		}

	case http.MethodPut:
		var request model.OfficialRolesRequest

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		fmt.Printf("PUT official roles: sport=%q\n", request.Sport)

		for _, role := range request.Roles {
			fmt.Printf(
				"RoleId=%s Title=%q DisplayOrder=%d Active=%t\n",
				role.RoleId,
				role.Title,
				role.DisplayOrder,
				role.Active,
			)
		}

		// Save this sport's roles using the session's tenantId.
		// Return success only after the database update succeeds.
		// tid comes from your authenticated session.
		if err := database.AddOfficialRoles(request); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"message": "Official roles saved successfully.",
		})

	case http.MethodDelete:
		sport := r.URL.Query().Get("sport")

		// tid comes from the authenticated session.
		if err := database.DeleteOfficialRoles(sport); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"message": "Official roles deleted successfully.",
		})

	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

	}
}

func CreateAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "method not allowed",
		})
		return
	}

	var req model.UserRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "invalid request",
		})
		return
	}

	maxUserAccountsString := strings.TrimSpace(
		os.Getenv("MAX_USER_ACCOUNTS"),
	)

	if maxUserAccountsString == "" {
		maxUserAccountsString = "10"
	}

	maxUserAccounts, err := strconv.ParseInt(
		maxUserAccountsString,
		10,
		64,
	)
	if err != nil || maxUserAccounts < 0 {
		log.Printf(
			"Invalid MAX_USER_ACCOUNTS value: %q",
			maxUserAccountsString,
		)

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "account creation is not configured correctly",
		})
		return
	}

	currentUserAccounts, err := uc.GetNumOfUserAccounts()
	if err != nil {
		log.Printf("Could not count user accounts: %v", err)

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "could not create account",
		})
		return
	}

	if maxUserAccounts > 0 { // 0 means unlimited
		if currentUserAccounts >= maxUserAccounts {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "the maximum number of user accounts has been reached",
			})
			return
		}
	}

	tenantID, err := uc.Add(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrUserAlreadyExists):
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "username already exists",
			})

		case errors.Is(err, database.ErrInvalidEmail):
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "username must be a valid email address",
			})

		default:
			log.Printf("Could not create user account: %v", err)

			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "could not create account",
			})
		}

		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"message":  "account created",
		"tenantId": tenantID,
	})
}

func AddSport(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var sport database.Sport

	if err := json.NewDecoder(r.Body).Decode(&sport); err != nil {
		http.Error(
			w,
			"Invalid sport data",
			http.StatusBadRequest,
		)
		return
	}

	if err := spc.Add(sport); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Sport added successfully",
	})
}

func DelSport(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)

		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	sportID := strings.TrimSpace(
		r.URL.Query().Get("id"),
	)

	if sportID == "" {
		http.Error(
			w,
			"Sport ID is required",
			http.StatusBadRequest,
		)
		return
	}

	if err := spc.Delete(sportID); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(
		map[string]string{
			"message": fmt.Sprintf(
				"Sport %q deleted successfully",
				sportID,
			),
		},
	); err != nil {
		fmt.Printf(
			"Unable to encode delete sport response: %v\n",
			err,
		)
	}
}

func SportsHandler(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		GetSportsHandler(w, r)

	case http.MethodPost:
		AddSport(w, r)

	case http.MethodDelete:
		DelSport(w, r)

	default:
		w.Header().Set(
			"Allow",
			"GET, POST, DELETE",
		)

		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
	}
}

func DelLevel(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)

		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	tenantID, err := getTenantId(r)

	if err != nil {
		http.Error(
			w,
			"Unable to determine tenant ID",
			http.StatusUnauthorized,
		)
		return
	}

	levelID := strings.TrimSpace(
		r.URL.Query().Get("id"),
	)

	if levelID == "" {
		http.Error(
			w,
			"Level ID is required",
			http.StatusBadRequest,
		)
		return
	}

	if err := lc.Delete(levelID, tenantID); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(
		map[string]string{
			"message": fmt.Sprintf(
				"Level %q deleted successfully",
				levelID,
			),
		},
	); err != nil {
		fmt.Printf(
			"Unable to encode delete level response: %v\n",
			err,
		)
	}
}

func AddLevel(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	tenantId, err := getTenantId(r)

	if err != nil {
		http.Error(
			w,
			"Unable to determine tenant ID",
			http.StatusUnauthorized,
		)
		return
	}

	var level database.Level

	if err := json.NewDecoder(r.Body).Decode(&level); err != nil {
		http.Error(
			w,
			"Invalid level data",
			http.StatusBadRequest,
		)
		return
	}

	if err := lc.Add(level, tenantId); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Level added successfully",
	})

}

func ActivateSport(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var sport database.Sport

	if err := json.NewDecoder(r.Body).Decode(&sport); err != nil {
		http.Error(
			w,
			"Invalid level data",
			http.StatusBadRequest,
		)
		return
	}

	if err := spc.Activate(sport); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Sport activated successfully.",
	})
}

func DeactivateSport(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var sport database.Sport

	if err := json.NewDecoder(r.Body).Decode(&sport); err != nil {
		http.Error(
			w,
			"Invalid level data",
			http.StatusBadRequest,
		)
		return
	}

	if err := spc.Deactivate(sport); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Sport deactivated successfully.",
	})
}

func ActivateLevel(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	tenantId, err := getTenantId(r)

	if err != nil {
		http.Error(
			w,
			"Unable to determine tenant ID",
			http.StatusUnauthorized,
		)
		return
	}

	var level database.Level

	if err := json.NewDecoder(r.Body).Decode(&level); err != nil {
		http.Error(
			w,
			"Invalid level data",
			http.StatusBadRequest,
		)
		return
	}

	if err := lc.Activate(level, tenantId); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Level activated successfully.",
	})
}

func DeactivateLevel(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	tenantId, err := getTenantId(r)

	if err != nil {
		http.Error(
			w,
			"Unable to determine tenant ID",
			http.StatusUnauthorized,
		)
		return
	}

	var level database.Level

	if err := json.NewDecoder(r.Body).Decode(&level); err != nil {
		http.Error(
			w,
			"Invalid level data",
			http.StatusBadRequest,
		)
		return
	}

	if err := lc.Deactivate(level, tenantId); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Level deactivated successfully.",
	})
}

func UpdateLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantId, err := getTenantId(r)
	if err != nil || strings.TrimSpace(tenantId) == "" || tenantId == "na" {
		http.Error(w, "Unable to determine tenant ID", http.StatusUnauthorized)
		return
	}
	var level database.Level
	if err := json.NewDecoder(r.Body).Decode(&level); err != nil {
		http.Error(w, "Invalid level data", http.StatusBadRequest)
		return
	}
	if err := lc.Update(level, tenantId); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, mongo.ErrNoDocuments) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Level sport and game fee saved successfully"})
}

func LevelsHandler(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		GetLevelsHandler(w, r)

	case http.MethodPost:
		AddLevel(w, r)

	case http.MethodPut:
		UpdateLevel(w, r)

	case http.MethodDelete:
		DelLevel(w, r)

	default:
		w.Header().Set(
			"Allow",
			"GET, POST, PUT, DELETE",
		)

		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
	}
}

func GetAdminRolesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	collection := database.Client.
		Database(database.GetMongoDbName()).
		Collection("userroles")

	cursor, err := collection.Find(
		ctx,
		bson.M{"active": true},
		options.Find().SetSort(
			bson.D{{Key: "name", Value: 1}},
		),
	)
	if err != nil {
		http.Error(w, "Unable to load roles", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	roles := make([]string, 0)
	seen := make(map[string]bool)

	for cursor.Next(ctx) {
		var record struct {
			Name string `bson:"name"`
		}

		if err := cursor.Decode(&record); err != nil {
			http.Error(w, "Unable to read role", http.StatusInternalServerError)
			return
		}

		if record.Name == "" ||
			record.Name != strings.TrimSpace(record.Name) ||
			seen[record.Name] {
			continue
		}

		seen[record.Name] = true
		roles = append(roles, record.Name)
	}

	if err := cursor.Err(); err != nil {
		http.Error(w, "Unable to read roles", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"roles": roles,
	})
}

func UpdateAdminUserRoleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	writeError := func(status int, message string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{
			"message": message,
		})
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if r.Header.Get("X-Ref-Ledger-Admin") != "1" {
		writeError(http.StatusForbidden, "Invalid administration request")
		return
	}

	session, err := database.GetSession(r)
	if err != nil {
		writeError(http.StatusUnauthorized, "Sign in again")
		return
	}

	var request struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeError(http.StatusBadRequest, "Invalid role request")
		return
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(http.StatusBadRequest, "Provide one JSON object")
		return
	}

	userID, err := primitive.ObjectIDFromHex(request.UserID)
	if err != nil {
		writeError(http.StatusBadRequest, "Invalid user ID")
		return
	}

	if request.Role == "" ||
		request.Role != strings.TrimSpace(request.Role) {
		writeError(http.StatusBadRequest, "Select a valid role")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	db := database.Client.Database(database.GetMongoDbName())

	transaction, err := database.Client.StartSession()
	if err != nil {
		writeError(http.StatusInternalServerError, "Unable to start role update")
		return
	}
	defer transaction.EndSession(ctx)

	invalidRole := errors.New("invalid role")
	userNotFound := errors.New("user not found")
	adminRequiredError := errors.New("administrator access required")
	selfDemotion := errors.New("self demotion")

	currentSessionCancelled := false

	_, err = transaction.WithTransaction(
		ctx,
		func(tx mongo.SessionContext) (interface{}, error) {
			// Check the administrator's current database role.
			var administrator struct {
				Role string `bson:"role"`
			}

			err := db.Collection("users").FindOne(
				tx,
				bson.M{
					"username": session.Username,
					"tenantId": session.TenantID,
				},
			).Decode(&administrator)

			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, adminRequiredError
			}
			if err != nil {
				return nil, err
			}
			if administrator.Role != "admin" {
				return nil, adminRequiredError
			}

			// Only allow roles configured in the backend collection.
			err = db.Collection("userroles").FindOne(
				tx,
				bson.M{
					"name":   request.Role,
					"active": true,
				},
			).Err()

			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, invalidRole
			}
			if err != nil {
				return nil, err
			}

			var user struct {
				Username string `bson:"username"`
				TenantID string `bson:"tenantId"`
			}

			err = db.Collection("users").FindOne(
				tx,
				bson.M{"_id": userID},
			).Decode(&user)

			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, userNotFound
			}
			if err != nil {
				return nil, err
			}

			isCurrentUser := user.Username == session.Username &&
				user.TenantID == session.TenantID

			if isCurrentUser && request.Role != "admin" {
				return nil, selfDemotion
			}

			_, err = db.Collection("users").UpdateOne(
				tx,
				bson.M{"_id": userID},
				bson.M{
					"$set": bson.M{
						"role": request.Role,
					},
				},
			)
			if err != nil {
				return nil, err
			}

			// Existing sessions contain the old role.
			_, err = db.Collection("sessions").DeleteMany(
				tx,
				bson.M{
					"username": user.Username,
					"tenantId": user.TenantID,
				},
			)
			if err != nil {
				return nil, err
			}

			currentSessionCancelled = isCurrentUser
			return nil, nil
		},
	)

	switch {
	case errors.Is(err, adminRequiredError):
		writeError(http.StatusForbidden, "Administrator access required")
		return

	case errors.Is(err, invalidRole):
		writeError(http.StatusBadRequest, "Selected role is not active or does not exist")
		return

	case errors.Is(err, userNotFound):
		writeError(http.StatusNotFound, "User not found")
		return

	case errors.Is(err, selfDemotion):
		writeError(
			http.StatusConflict,
			"Another administrator must change your role",
		)
		return

	case err != nil:
		log.Printf("Update user role failed: %v", err)
		writeError(
			http.StatusInternalServerError,
			"Unable to update role. No changes were committed.",
		)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                 true,
		"message":                 "Role updated. The user must sign in again.",
		"currentSessionCancelled": currentSessionCancelled,
	})
}

func UpdateAdminUserPasswordHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Set("Cache-Control", "no-store")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Also verify authorization here in case this handler
	// is accidentally registered without adminRequired.
	session, err := database.GetSession(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if session.Role != "admin" {
		http.Error(
			w,
			"Administrator access required",
			http.StatusForbidden,
		)
		return
	}

	writeError := func(status int, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": message,
		})
	}

	// Matches the custom header sent by administration.html.
	if r.Header.Get("X-Ref-Ledger-Admin") != "1" {
		writeError(http.StatusForbidden, "Invalid admin request.")
		return
	}

	var payload struct {
		UserID      string `json:"userId"`
		NewPassword string `json:"newPassword"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&payload); err != nil {
		writeError(
			http.StatusBadRequest,
			"Invalid password update request.",
		)
		return
	}

	// Reject additional JSON values after the request object.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(
			http.StatusBadRequest,
			"Request must contain a single JSON object.",
		)
		return
	}

	userID, err := primitive.ObjectIDFromHex(
		strings.TrimSpace(payload.UserID),
	)
	if err != nil {
		writeError(http.StatusBadRequest, "Invalid user ID.")
		return
	}

	// Validate without trimming the actual password:
	// leading and trailing spaces may be intentional.
	if strings.TrimSpace(payload.NewPassword) == "" {
		writeError(
			http.StatusBadRequest,
			"New password cannot be blank.",
		)
		return
	}

	// bcrypt accepts a maximum of 72 bytes.
	if len([]byte(payload.NewPassword)) > 72 {
		writeError(
			http.StatusBadRequest,
			"New password cannot exceed 72 bytes.",
		)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(payload.NewPassword),
		bcrypt.DefaultCost,
	)
	if err != nil {
		writeError(
			http.StatusInternalServerError,
			"Unable to secure the new password.",
		)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(),
		10*time.Second,
	)
	defer cancel()

	result, err := uc.Coll.UpdateOne(
		ctx,
		bson.M{
			"_id": userID,
		},
		bson.M{
			"$set": bson.M{
				"passwordHash": string(passwordHash),
			},
		},
	)
	if err != nil {
		// Never log the request body, password, or hash.
		writeError(
			http.StatusInternalServerError,
			"Unable to update the user's password.",
		)
		return
	}

	if result.MatchedCount == 0 {
		writeError(http.StatusNotFound, "User not found.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message":                 "Password changed successfully.",
		"currentSessionCancelled": false,
	})
}

func main() {

	var err error

	if err = utils.InitLogging(); err != nil {
		panic(err)
	}

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "local-dev"
	}

	log.Printf("Ref Ledger running on pod: %s", podName)

	environMentInfo := database.GetEnvironment()

	fmt.Println("Ref Ledger V2.1 Web Page Server Environment:", environMentInfo.Environment)
	fmt.Println("Ref Ledger V2.1 Web Page Server Establing database connection...")
	utils.AuditLog.Println("Ref Ledger V2.1 Web Page Server Establing database connection...")

	err = database.Connect()

	if err != nil {
		fmt.Println(
			"Failed to init database. Terminating web page server. Reason:",
			err,
		)

		utils.AuditLog.Println(
			"Failed to init database. Terminating web page server. Reason:",
			err,
		)

		return
	}

	message, err := database.VerifyMongoConnection(database.Client)
	if err == nil {
		fmt.Println(message)
		utils.AuditLog.Println(message)
		utils.AuditLog.Println("Database connection established successfully")
	} else {
		return
	}

	err = lc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize levels collection.")
		utils.AuditLog.Println("Failed to initialize levels collection.")
		return
	}

	err = spc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize sports collection.")
		utils.AuditLog.Println("Failed to initialize sports collection.")
		return
	}

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

	database.CreateSettingsIndexes()

	result, err, numOfIndices := sc.IsIndexed()

	if result == false && numOfIndices != 3 {
		err = sc.CreateIndices()
	}

	if err != nil {
		fmt.Println("Failed to create indices for Sites Collection.  Reason:", err)
	} else {
		fmt.Println("Sites Collection successfully indexed")
	}

	err = gc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize game collection.")
		utils.AuditLog.Println("Failed to initialize game collection.")
		return
	}

	err = oc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize official collection.")
		utils.AuditLog.Println("Failed to initialize official collection.")
		return
	}

	err = oc.CreateIndices()

	if err != nil {
		fmt.Println("Failed to create indices for Officials Collection.  Reason:", err)
	} else {
		fmt.Println("Officials Collection successfully indexed")
	}
	if database.IsSessionIndexed() {
		database.CreateSessionIndices()
	}

	err = ec.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize expenses collection.")
		utils.AuditLog.Println("Failed to initialize expenses collection.")
		return
	}

	err = se.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize sessions collection.")
		utils.AuditLog.Println("Failed to initialize sessions collection.")
		return
	}

	err = uc.Init(database.Client)
	if err != nil {
		fmt.Println("Failed to initialize users collection.")
		utils.AuditLog.Println("Failed to initialize users collection.")
		return
	}
	utils.AuditLog.Println("All collections initialized successfully.")

	fmt.Println("Registering routes...")
	utils.AuditLog.Println("Registering routes...")
	mux := http.NewServeMux()
	registerAdminUserRoutes(mux)

	// User role management (administrator access required).
	mux.HandleFunc("/api/admin/roles", authRequired(adminRequired(GetAdminRolesHandler)))
	mux.HandleFunc("/api/admin/users/role", authRequired(adminRequired(UpdateAdminUserRoleHandler)))

	mux.Handle("/images/", http.StripPrefix("/images/",
		http.FileServer(http.Dir("./internal/html/images"))))

	mux.HandleFunc("/api/environment", GetEnvironmentHandler)
	mux.HandleFunc("/api/about", AboutHandler)
	mux.HandleFunc("/settings", authRequired(SettingsPage))
	mux.HandleFunc("/administration", authRequired(adminRequired(AdministrationPage)))

	mux.HandleFunc("/expenses", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/expenses.html")
	}))

	mux.HandleFunc("/about", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/about.html")
	}))

	mux.HandleFunc("/gameStatus", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/gameStatus.html")
	}))

	mux.HandleFunc("/reports_v2", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/reports_v2.html")
	}))

	mux.HandleFunc("/games", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/games.html")
	}))

	mux.HandleFunc("/dashboard", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/dashboard.html")
	}))

	mux.HandleFunc("/payments", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/payments.html")
	}))

	mux.HandleFunc("/associations", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/associations.html")
	}))

	mux.HandleFunc("/sites", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/sites.html")
	}))

	mux.HandleFunc("/officials", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/officials.html")
	}))

	mux.HandleFunc("/contact", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/contact.html")
	}))

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/login.html")
	})

	mux.HandleFunc("/api/games/pending-games/count", GetPendingGamesCount)

	mux.HandleFunc("/api/session", getCurrentSession)
	mux.HandleFunc("/api/pod", PodInfoHandler)

	mux.HandleFunc("/api/import/officials/preview", authRequired(readOnlyForbidden(PreviewOfficialsImportHandler)))
	mux.HandleFunc("/api/import/officials/commit", authRequired(readOnlyForbidden(CommitOfficialsImportHandler)))

	mux.HandleFunc("/api/import/associations/preview", authRequired(readOnlyForbidden(PreviewAssociationsImportHandler)))
	mux.HandleFunc("/api/import/associations/commit", authRequired(readOnlyForbidden(CommitAssociationsImportHandler)))

	mux.HandleFunc("/api/import/sites/preview", authRequired(readOnlyForbidden(PreviewSitesImportHandler)))
	mux.HandleFunc("/api/import/sites/commit", authRequired(readOnlyForbidden(CommitSitesImportHandler)))

	mux.HandleFunc("/api/import/games/preview", authRequired(readOnlyForbidden(PreviewGamesImportHandler)))
	mux.HandleFunc("/api/import/games/commit", authRequired(readOnlyForbidden(CommitGamesImportHandler)))

	mux.HandleFunc("/api/loadOfficials", GetOfficialsHandler)
	mux.HandleFunc("/api/loadExpenseTypes", GetExpenseTypes)
	mux.HandleFunc("/api/loadStatusTypes", GetStatusTypes)
	mux.HandleFunc("/api/loadSites", GetSitesHandler)
	mux.HandleFunc("/api/loadSportsSupported", LoadSportsSupportedHandler)
	mux.HandleFunc("/api/loadLevels", GetLevelsHandler)
	mux.HandleFunc("/api/loadSports", authRequired(GetSportsHandler))
	mux.HandleFunc("/api/sports", authRequired(sportsAccess(SportsHandler)))
	mux.HandleFunc("/api/levels", authRequired(readOnlyForbidden(LevelsHandler)))
	mux.HandleFunc("/api/loadAssociations", GetAssociationsHandler)
	mux.HandleFunc("/api/officialsDirectory", GetOfficialsDirectoryHandler)
	mux.HandleFunc("/api/associationsDirectory", GetAssociationsDirectoryHandler)
	mux.HandleFunc("/api/sitesDirectory", GetSitesDirectoryHandler)
	mux.HandleFunc("/api/loadAssignors", GetAssignorsHandler)
	mux.HandleFunc("/api/game/{association}/{gameid}", GetSingleGame)
	mux.HandleFunc("/api/association/{assocId}", GetSingleAssociation)
	mux.HandleFunc("/api/officials/{firstName}/{lastName}", GetOfficial)
	mux.HandleFunc("/api/sport-active", authRequired(adminRequired(ActivateSport)))
	mux.HandleFunc("/api/sport-inactive", authRequired(adminRequired(DeactivateSport)))
	mux.HandleFunc("/api/level-active", ActivateLevel)
	mux.HandleFunc("/api/level-inactive", DeactivateLevel)
	mux.HandleFunc("/api/updateExpense", authRequired(readOnlyForbidden(UpdateExpense)))

	mux.HandleFunc("/api/import/officials/template", authRequired(readOnlyForbidden(DownloadOfficialsTemplateHandler)))
	mux.HandleFunc("/api/import/associations/template", authRequired(readOnlyForbidden(DownloadAssociationsTemplateHandler)))
	mux.HandleFunc("/api/import/sites/template", authRequired(readOnlyForbidden(DownloadSitesTemplateHandler)))
	mux.HandleFunc("/api/import/games/template", authRequired(readOnlyForbidden(DownloadGamesTemplateHandler)))
	mux.HandleFunc("/api/deleteExpense", authRequired(readOnlyForbidden(DeleteExpenseHandler)))

	mux.HandleFunc("/api/sports_supported", SaveSportsSupportedHandler)
	mux.HandleFunc("/api/deleteAssociation/{assocId}",
		authRequired(readOnlyForbidden(DeleteAssociation)))

	mux.HandleFunc("/api/site/{siteId}", GetSingleSite)
	mux.HandleFunc("/api/deleteSite/{siteId}", authRequired(readOnlyForbidden(DeleteSite)))
	mux.HandleFunc("/api/deleteGame/{association}/{gameId}", authRequired(readOnlyForbidden(DeleteGame)))
	mux.HandleFunc("/api/deleteOfficial/{firstName}/{lastName}", authRequired(readOnlyForbidden(DeleteOfficial)))
	mux.HandleFunc("/api/officials/{officialId}", authRequired(readOnlyForbidden(OfficialsDetailHandler)))

	mux.HandleFunc("/importOfficials", authRequired(readOnlyForbidden(ImportOfficialsPageHandler)))
	mux.HandleFunc("/importAssociations", authRequired(readOnlyForbidden(ImportAssociationsPageHandler)))
	mux.HandleFunc("/importGames", authRequired(readOnlyForbidden(ImportGamesPageHandler)))
	mux.HandleFunc("/importSites", authRequired(readOnlyForbidden(ImportSitesPageHandler)))

	mux.HandleFunc("/api/officials", authRequired(readOnlyForbidden(CreateOfficial)))
	mux.HandleFunc("/api/officials-update", authRequired(readOnlyForbidden(UpdateOfficial)))
	mux.HandleFunc("/api/expenses", authRequired(readOnlyForbidden(CreateExpense)))

	mux.HandleFunc("/api/admin/users/password", authRequired(adminRequired(UpdateAdminUserPasswordHandler)))

	mux.HandleFunc("/api/associations",
		authRequired(readOnlyForbidden(CreateAssociation)))

	mux.HandleFunc("/api/associations-update",
		authRequired(readOnlyForbidden(UpdateAssociation)))

	mux.HandleFunc("/createAccount", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/createAccount.html")
	})

	mux.HandleFunc("/forgotPassword", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/forgotPassword.html")
	})

	mux.HandleFunc("/resetPassword", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/resetPassword.html")
	})

	mux.HandleFunc("/api/createAccount", CreateAccount)
	mux.HandleFunc("/api/sites", authRequired(readOnlyForbidden(CreateSite)))
	mux.HandleFunc("/api/sites-update", authRequired(readOnlyForbidden(UpdateSite)))
	mux.HandleFunc("/api/games/status", authRequired(readOnlyForbidden(UpdateGameStatus)))
	mux.HandleFunc("/api/reports", GenerateReport)
	mux.HandleFunc("/api/financial-report", GenerateFinancialReport)
	mux.HandleFunc("/api/expense-report", GenerateExpenseReport)
	mux.HandleFunc("/api/1099-report", Generate1099Report)
	mux.HandleFunc("/api/revenue-report", GenerateRevenueReport)
	mux.HandleFunc("/api/accounts-receivable-report", GenerateAccountsReceivableReport)
	mux.HandleFunc("/api/game-report", GenerateGameReport)
	mux.HandleFunc("/api/reconciliation-report", GenerateReconciliationReport)
	mux.HandleFunc("/api/game-save", authRequired(readOnlyForbidden(SaveGame)))
	mux.HandleFunc("/api/game-update", authRequired(readOnlyForbidden(UpdateGame)))
	mux.HandleFunc("/api/dashboard", GetGames)
	mux.HandleFunc("/api/payments", authRequired(readOnlyForbidden(CreatePayment)))
	mux.HandleFunc("/api/update-payment", authRequired(readOnlyForbidden(UpdatePayment)))
	mux.HandleFunc("/api/payment-registry", authRequired(readOnlyForbidden(LoadPaymentRegistry)))
	mux.HandleFunc("/api/expense-registry", authRequired(readOnlyForbidden(LoadExpenseRegistry)))
	mux.HandleFunc("/api/deletePayment", authRequired(readOnlyForbidden(DeletePayment)))
	mux.HandleFunc("/api/login", ValidateLogin)
	mux.HandleFunc("/api/logout", Logout)
	mux.HandleFunc("/api/forgotPassword", handlers.ForgotPasswordHandler)
	mux.HandleFunc("/api/resetPassword", handlers.ResetPasswordHandler)
	mux.HandleFunc("/api/settings/official-roles", authRequired(officialRolesAccess(OfficialRolesHandler)))
	mux.HandleFunc("/api/email-report", authRequired(readOnlyForbidden(EmailReport)))

	mux.HandleFunc("/components-test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "NEW BINARY IS RUNNING")
	})

	mux.HandleFunc("/debug/components", func(w http.ResponseWriter, r *http.Request) {

		fmt.Fprintln(w, "components route is active")

	})

	mux.Handle(
		"/components/",
		http.StripPrefix(
			"/components/",
			http.FileServer(
				http.Dir("./internal/html/components"),
			),
		),
	)

	mux.Handle(
		"/css/",
		http.StripPrefix(
			"/css/",
			http.FileServer(
				http.Dir("./internal/html/css"),
			),
		),
	)

	mux.HandleFunc("/home", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(
			w,
			r,
			"./internal/html/index.html",
		)
	}))

	mux.Handle("/", authRequired(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		http.Redirect(
			w,
			r,
			"/dashboard",
			http.StatusSeeOther,
		)
	}))

	fmt.Println("Routes successfully registered")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	address := ":" + port

	fmt.Printf("Ref Ledger listening on %s\n", address)
	loggedMux := LogRequest(mux)

	if err := http.ListenAndServe(address, loggedMux); err != nil {
		log.Fatal(err)
	}

}
