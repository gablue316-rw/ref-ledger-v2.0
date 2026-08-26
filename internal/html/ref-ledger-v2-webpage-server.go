package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"

	"crypto/rand"
	"encoding/hex"

	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"unicode"

	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/email"
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

/* July 18, 2026 */
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

var AuditLog *log.Logger = nil

func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func getTenantId(r *http.Request) (string, error) {

	tId := database.TenantId

	fmt.Println("Validating tenant ID")
	if tId == "na" {
		fmt.Println("Retrieving session id")
		cookie, err := r.Cookie("rl_session")
		if err != nil {
			fmt.Println("Error retrieving session id.  Reason:", err)
			return "", err
		}

		sessionId := cookie.Value
		fmt.Println("Session ID:", sessionId)

		fmt.Println("Retrieving tenant id for session:", sessionId)
		tId, err = se.GetTenantID(sessionId)

		if err != nil {
			return "", err
		}

		fmt.Println("Updating tenant ID with:", tId)
		database.UpdateTenantId(tId)
	}

	fmt.Println("Returning tenant ID:", tId)
	return tId, nil
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

		getTenantId(r)
		logPrint := fmt.Sprintf("Pod=%s Method=%s Path=%s RemoteAddr=%s", podName, r.Method, r.URL.Path, r.RemoteAddr)
		log.Println(logPrint)
		fmt.Println(logPrint)

		next.ServeHTTP(w, r)
	})
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

type UserSessionResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Name     string `json:"name"`
}

func getCurrentSession(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	fmt.Println("getCurrentSession")
	var tId string = database.TenantId
	var err error

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

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

func GetAssignorsHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	var tId string = database.TenantId
	var err error

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	assignors, err := ac.GetAssignorNames(tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assignors)
}

func GetSitesDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)
	sites, err := sc.GetSitesDirectory(database.TenantId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

func GetAssociationsDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	associations, err := ac.GetAssociationsDirectory(database.TenantId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(associations)
}

func GetOfficialsDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)

	firstName := strings.TrimSpace(r.URL.Query().Get("firstname"))
	lastName := strings.TrimSpace(r.URL.Query().Get("lastname"))

	officials, err := oc.GetOfficialsDirectory(firstName, lastName, database.TenantId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(officials)
}

func GetAssociationsHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	var tId string = database.TenantId
	var err error

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	associations, err := ac.GetAssociationIds(tId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(associations)
}

func GetSitesHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	var tId string = database.TenantId
	var err error

	fmt.Println("Request path:", r.URL.Path)
	fmt.Println("TenantId global:", database.TenantId)

	for _, c := range r.Cookies() {
		fmt.Println("Cookie:", c.Name, c.Value)
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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
		"referee",
		"u1",
		"u2",
		"eco",
		"assignor",
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
		"Alice Brown",
		"Charlie Davis",
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
	GameId      int64  `json:"gameId"`
	Date        string `json:"date"`
	Time        string `json:"time"`
	Sport       string `json:"sport"`
	Site        string `json:"site"`
	Field       string `json:"field"`
	NumOfGames  int64  `json:"numOfGames"`
	Level       string `json:"level"`
	GameFee     string `json:"gameFee"`
	TravelPay   string `json:"travelPay"`
	AssignorFee string `json:"assignorFee"`
	Deductions  string `json:"deductions"`
	Association string `json:"association"`
	Status      string `json:"status"`
	Referee     string `json:"referee"`
	U1          string `json:"u1"`
	U2          string `json:"u2"`
	ECO         string `json:"eco"`
	Assignor    string `json:"assignor"`
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
		"referee",
		"u1",
		"u2",
		"eco",
		"assignor",
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

func buildGamePreviewRow(rowNumber int, record []string, tId string) GamePreviewRow {

	row := GamePreviewRow{
		RowNumber: rowNumber,
		Valid:     false,
		Errors:    make([]string, 0),
	}

	// Need to convert the first column to int64 for GameId and NumOfGames
	gameId, _ := strconv.ParseInt(csvColumn(record, 0), 10, 64)
	numOfGames, _ := strconv.ParseInt(csvColumn(record, 6), 10, 64)

	if len(record) != 19 {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf(
				"Expected 19 columns but found %d",
				len(record),
			),
		)

		/* Still copy any available values so the user can see what was read from the CSV. */

		row.Data = GameImportData{
			GameId:      gameId,
			Date:        csvColumn(record, 1),
			Time:        csvColumn(record, 2),
			Sport:       csvColumn(record, 3),
			Site:        csvColumn(record, 4),
			Field:       csvColumn(record, 5),
			NumOfGames:  numOfGames,
			Level:       csvColumn(record, 7),
			GameFee:     csvColumn(record, 8),
			TravelPay:   csvColumn(record, 9),
			AssignorFee: csvColumn(record, 10),
			Deductions:  csvColumn(record, 11),
			Association: csvColumn(record, 12),
			Status:      csvColumn(record, 13),
			Referee:     csvColumn(record, 14),
			U1:          csvColumn(record, 15),
			U2:          csvColumn(record, 16),
			ECO:         csvColumn(record, 17),
			Assignor:    csvColumn(record, 18),
		}
		return row
	}

	row.Data = GameImportData{
		GameId:      gameId,
		Date:        csvColumn(record, 1),
		Time:        csvColumn(record, 2),
		Sport:       csvColumn(record, 3),
		Site:        csvColumn(record, 4),
		Field:       csvColumn(record, 5),
		NumOfGames:  numOfGames,
		Level:       csvColumn(record, 7),
		GameFee:     csvColumn(record, 8),
		TravelPay:   csvColumn(record, 9),
		AssignorFee: csvColumn(record, 10),
		Deductions:  csvColumn(record, 11),
		Association: csvColumn(record, 12),
		Status:      csvColumn(record, 13),
		Referee:     csvColumn(record, 14),
		U1:          csvColumn(record, 15),
		U2:          csvColumn(record, 16),
		ECO:         csvColumn(record, 17),
		Assignor:    csvColumn(record, 18),
	}

	// Validate fields

	_, err := sc.GetSiteId(row.Data.Site, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching site ID: %v", err),
		)
	}

	_, err = ac.Exists(row.Data.Association, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching association ID: %v", err),
		)
	}

	_, err = oc.Exists(row.Data.Referee, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching referee: %v", err),
		)
	}

	_, err = oc.Exists(row.Data.U1, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching U1: %v", err),
		)
	}

	_, err = oc.Exists(row.Data.U2, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching U2: %v", err),
		)
	}

	_, err = oc.Exists(row.Data.ECO, tId)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching ECO: %v", err),
		)
	}

	_, err = ac.AssignorExists(row.Data.Assignor, tId, row.Data.Association)
	if err != nil {
		row.Errors = append(
			row.Errors,
			fmt.Sprintf("Error occurred while fetching Assignor: %v", err),
		)
	}

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

	officialsPreviewStore.RLock()
	defer officialsPreviewStore.RUnlock()

	preview, found := sitesPreviewStore.items[token]

	if !found {
		return SitesImportPreview{}, false
	}

	// Treat expired previews as missing.
	if time.Now().After(preview.ExpiresAt) {
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

	preview, found := getGamesPreview(request.PreviewToken)
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

	if oc.Coll == nil {
		log.Printf("CommitGamesImportHandler: games collection is nil")
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
	rowResults := make([]GamesCommitRowResult, 0)

	for _, previewRow := range preview.Rows {
		if !previewRow.Valid {
			continue
		}
		data := previewRow.Data

		exists, err := gc.Exists(data.Association, utils.ConvertInt64ToStr(data.GameId), tId)

		if err != nil {
			log.Printf(
				"CommitGamesImportHandler: "+"duplicate lookup failed: tenant=%s row=%d game=%d error=%v",
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
					Message:   "Unable to check whether the game already exists.",
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

		game := model.GameDescriptor{
			GameId:      utils.ConvertInt64ToStr(data.GameId),
			Date:        data.Date,
			Time:        data.Time,
			Sport:       data.Sport,
			Site:        data.Site,
			Field:       data.Field,
			NumOfGames:  utils.ConvertInt64ToStr(data.NumOfGames),
			Level:       data.Level,
			GameFee:     data.GameFee,
			TravelPay:   data.TravelPay,
			AssignorFee: data.AssignorFee,
			Deductions:  data.Deductions,
			Association: data.Association,
			Status:      data.Status,
			Referee:     data.Referee,
			U1:          data.U1,
			U2:          data.U2,
			ECO:         data.ECO,
			Assignor:    data.Assignor,
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
				"CommitGamesImportHandler: "+"insert failed: tenant=%s row=%d game=%d error=%v",
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
					Message:   "Unable to insert game.",
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
				Message:   fmt.Sprintf("Game %d was added.", data.GameId),
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"Method not allowed.",
		)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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
		log.Printf("CommitSitesImportHandler: sites collection is nil")
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"Sites collection is not available.",
		)
		return
	}

	added := 0
	skipped := 0
	failed := 0
	rowResults := make([]SitesCommitRowResult, 0)

	for _, previewRow := range preview.Rows {
		if !previewRow.Valid {
			continue
		}
		data := previewRow.Data

		exists, err := sc.Exists(data.SiteId, tId)

		if err != nil {
			log.Printf(
				"CommitSitesImportHandler: "+"duplicate lookup failed: tenant=%s row=%d site=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.SiteId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				SitesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to check whether the site already exists.",
				},
			)
			continue
		}

		if exists {
			if request.DuplicateAction == "stop" {
				writeJSON(
					w,
					http.StatusConflict,
					SitesCommitResponse{
						Added:   added,
						Skipped: skipped,
						Failed:  failed,
						Message: fmt.Sprintf(
							"Import stopped because %s already exists.",
							data.SiteId,
						),
						Rows: rowResults,
					},
				)

				return
			}

			skipped++

			rowResults = append(
				rowResults,
				SitesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "skipped",
					Message: fmt.Sprintf(
						"%s already exists.",
						data.SiteId,
					),
				},
			)

			continue

		}

		site := database.Site{
			Id:      data.SiteId,
			Name:    data.SiteName,
			Contact: data.ContactName,
			Phone:   data.ContactNumber,
			Email:   data.ContactEmail,
		}

		err = sc.Add(site, tId)

		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				if request.DuplicateAction == "stop" {
					writeJSON(
						w,
						http.StatusConflict,
						SitesCommitResponse{
							Added:   added,
							Skipped: skipped,
							Failed:  failed,
							Message: fmt.Sprintf(
								"Import stopped because %s already exists.",
								data.SiteId,
							),
							Rows: rowResults,
						},
					)
					return
				}

				skipped++

				rowResults = append(
					rowResults,
					SitesCommitRowResult{
						RowNumber: previewRow.RowNumber,
						Status:    "skipped",
						Message: fmt.Sprintf(
							"%s already exists.",
							data.SiteId,
						),
					},
				)

				continue

			}

			log.Printf(
				"CommitSitesImportHandler: "+"insert failed: tenant=%s row=%d site=%s error=%v",
				tId,
				previewRow.RowNumber,
				data.SiteId,
				err,
			)

			failed++

			rowResults = append(
				rowResults,
				SitesCommitRowResult{
					RowNumber: previewRow.RowNumber,
					Status:    "failed",
					Message:   "Unable to insert site.",
				},
			)

			continue
		}
		added++
		rowResults = append(
			rowResults,
			SitesCommitRowResult{
				RowNumber: previewRow.RowNumber,
				Status:    "added",
				Message:   fmt.Sprintf("%s %s was added.", data.SiteId, data.SiteName),
			},
		)
	}

	deleteSitesPreview(request.PreviewToken)

	writeJSON(
		w,
		http.StatusOK,
		SitesCommitResponse{
			Added:   added,
			Skipped: skipped,
			Failed:  failed,
			Message: "Sites import completed.",
			Rows:    rowResults,
		},
	)
}

func PreviewAssociationsImportHandler(w http.ResponseWriter, r *http.Request) {

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

	var tId string = database.TenantId
	var err error

	fmt.Println("Previewing games import...")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	/* Limit the complete request body. The additional 1 MB allows room for the multipart boundary and HTTP form metadata. */

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+(oneMB))

	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The CSV upload is invalid or exceeds the 5 MB limit.")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "A Games CSV file is required.")
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

	if err := validateGamesCSVHeader(header); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows := make([]GamePreviewRow, 0)

	/* Used to detect duplicates inside the uploaded CSV file. */
	gamesInFile := make(map[int64]int)
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
				GamePreviewRow{
					RowNumber: csvRowNumber,
					Valid:     false,
					Data:      GameImportData{},
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

		previewRow := buildGamePreviewRow(
			csvRowNumber,
			record,
			tId,
		)

		gameId := previewRow.Data.GameId

		if gameId != 0 {
			if _, found := gamesInFile[gameId]; found {
				previewRow.Errors = append(
					previewRow.Errors,
					fmt.Sprintf(
						"Duplicate game in CSV file; first appeared on row %d",
						gamesInFile[gameId],
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

	/* Check MongoDB for games that already exist. This only runs for rows that passed the basic CSV validation. */

	for i := range rows {
		if !rows[i].Valid {
			continue
		}

		exists, err := gc.Exists(rows[i].Data.Association, utils.ConvertInt64ToStr(rows[i].Data.GameId), tId)
		if err != nil {
			log.Printf(
				"PreviewGamesImportHandler: duplicate lookup failed for tenant %s, game %d: %v",
				tId,
				rows[i].Data.GameId,
				err,
			)
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"Unable to validate games against the database.",
			)
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

	var tId string = database.TenantId
	var err error

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
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

func GetOfficialsHandler(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	var tId string = database.TenantId
	var err error

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

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

func generateAccountsReceivableReport(assoc, tid string) []string {

	var rept []string = []string{}
	rept = reports.GenerateAcctsRecvReport(context.TODO(), assoc, tid)
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
	LogVisitor(r)

	var tId string = database.TenantId
	var err error
	var siteId string

	gameFilters := model.GFilters{}
	expenseFilters := model.EFilters{}
	rType := r.URL.Query().Get("type")
	rEmail := r.URL.Query().Get("emailaddr")
	rFile := r.URL.Query().Get("filename")
	rStatus := r.URL.Query().Get("status")
	rAssoc := r.URL.Query().Get("association")
	rGameIds := r.URL.Query().Get("gameids")
	rSite := r.URL.Query().Get("site")

	rept := []string{}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	// The HTML uses the site name, so we need to convert it to an ID
	if rSite != "" {
		siteId, err = sc.GetSiteId(rSite, tId)
		if err != nil {
			http.Error(w, "Invalid site ID", http.StatusBadRequest)
			return
		}
	}

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
	gameFilters.Site = siteId
	gameFilters.TenantId = tId
	fmt.Println("Tenant ID:", tId, "Game Filters Tenant ID:", gameFilters.TenantId)

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
		rept = generateAccountsReceivableReport(rAssoc, tId)
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

	LogVisitor(r)
	var game Game
	var tId string = database.TenantId
	var err error
	var siteId string
	var gameDesc []model.GameDescriptor
	var singleGameDesc model.GameDescriptor = model.GameDescriptor{}

	fmt.Println("##### UpdateGame Enpoint Called #####")

	err = json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("Updating game with game id", utils.ConvertIntToStr(game.GameId), "for association", game.Association)

	singleGameDesc, err = gc.Get(game.Association, utils.ConvertIntToStr(game.GameId), tId)
	if err != nil {
		fmt.Println(err)
		http.Error(w, "Game Not Found", http.StatusBadRequest)
		return
	}

	fmt.Println("UpdateGame: Game Fee:", game.GameFee)
	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	// The HTML uses the site name, so we need to convert it to an ID
	siteId, err = sc.GetSiteId(game.Site, tId)
	if err != nil {
		http.Error(w, "Invalid site ID", http.StatusBadRequest)
		return
	}

	game.Site = siteId

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
		api.DelGame(context.TODO(), singleGameDesc.GameId)
		return
	}

	fmt.Println("SaveGame: Single Game Descriptor:", singleGameDesc)
	var gDoc model.GameDoc = model.GameDoc{}

	gDoc.GameId = int64(game.GameId)
	gDoc.Association = game.Association

	checkForDup := false
	err = api.ValidateGameDescriptor(context.TODO(), singleGameDesc, checkForDup)
	if err != nil {
		fmt.Println(err)
		http.Error(w, "Invalid Game", http.StatusBadRequest)
		return
	}

	gameExists, err := database.GameExists(gDoc)

	if err != nil {
		fmt.Println(err)
		return
	}

	if gameExists {
		err = database.UpdateOneGameDoc(context.TODO(), singleGameDesc, database.Database, "games", tId)
		if err != nil {
			fmt.Println(err)
			return
		}
		return
	}

	gameDesc = append(gameDesc, singleGameDesc)
	database.InsertGameDocs(context.TODO(), gameDesc, database.Database, "games", tId)

}

func SaveGame(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	var game Game
	var tId string = database.TenantId
	var err error
	var siteId string
	var gameDesc []model.GameDescriptor
	var singleGameDesc model.GameDescriptor = model.GameDescriptor{}

	fmt.Println("##### SaveGame Enpoint Called #####")

	err = json.NewDecoder(r.Body).Decode(&game)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Println("UpdateGame: Game Fee:", game.GameFee)
	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	// The HTML uses the site name, so we need to convert it to an ID
	siteId, err = sc.GetSiteId(game.Site, tId)
	if err != nil {
		http.Error(w, "Invalid site ID", http.StatusBadRequest)
		return
	}

	game.Site = siteId

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
		api.DelGame(context.TODO(), singleGameDesc.GameId)
		return
	}

	fmt.Println("SaveGame: Single Game Descriptor:", singleGameDesc)
	var gDoc model.GameDoc = model.GameDoc{}

	gDoc.GameId = int64(game.GameId)
	gDoc.Association = game.Association

	checkForDup := true
	err = api.ValidateGameDescriptor(context.TODO(), singleGameDesc, checkForDup)
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

	if gameExists {
		err = database.UpdateOneGameDoc(context.TODO(), singleGameDesc, database.Database, "games", tId)
		if err != nil {
			fmt.Println(err)
			return
		}
		return
	}

	gameDesc = append(gameDesc, singleGameDesc)
	database.InsertGameDocs(context.TODO(), gameDesc, database.Database, "games", tId)

}

func UpdateGameStatus(w http.ResponseWriter, r *http.Request) {

	var tId string = database.TenantId
	var err error

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

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	gc.UpdateGameStatus(gameIds, gameUpdate.Status)

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Game status updated successfully"))
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

func ValidateLogin(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	fmt.Println("Validating Login")
	var user model.User
	var err error

	sessionDuration := 15 * time.Minute

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")

	usersCollection := database.Client.
		Database("refLedger_v2").
		Collection("users")

	err = usersCollection.FindOne(
		r.Context(),
		bson.M{"username": username},
	).Decode(&user)

	if err != nil {
		fmt.Println("User not found")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	err = bcrypt.CompareHashAndPassword(
		[]byte(user.PasswordHash),
		[]byte(password),
	)

	if err != nil {
		fmt.Println("Invalid password")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// 2. Delete previous sessions if they still exist
	fmt.Println("Deleting previous sessions")
	database.DeleteSessions(username)

	// 3. Create session
	fmt.Println("Creating session")
	sessionID := uuid.New().String()

	fmt.Println("session ID:", sessionID)
	session := model.Session{
		SessionID: sessionID,
		Username:  user.Username,
		TenantID:  user.TenantID,
		ExpiresAt: time.Now().Add(sessionDuration),
		Role:      user.Role,
	}

	fmt.Println("Updating Tenant ID", user.TenantID, "session for user:", user.Username)

	database.UpdateTenantId(user.TenantID)

	fmt.Println("Storing session in MongoDB")

	// 4. Store in MongoDB
	_, err = database.Client.
		Database("refLedger_v2").
		Collection("sessions").
		InsertOne(r.Context(), session)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 5. Set cookie
	fmt.Println("Setting cookie")
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

func CreateAssociation(w http.ResponseWriter, r *http.Request) {

	var tId string = database.TenantId
	var err error

	LogVisitor(r)
	var assocJson database.AssociationJson

	err = json.NewDecoder(r.Body).Decode(&assocJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)

		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	err = ac.Add(ac.ConvAssocJsonToAssoc(assocJson), tId)
	if err != nil {
		fmt.Println("Failed to create association")
		http.Error(w, "Failed to create association", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Association updated successfully"))
}

func CreateSite(w http.ResponseWriter, r *http.Request) {

	var tId string = database.TenantId
	var err error

	LogVisitor(r)
	var siteJson database.SiteJson

	err = json.NewDecoder(r.Body).Decode(&siteJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	err = sc.Add(sc.ConvJsonToSite(siteJson), tId)
	if err != nil {
		fmt.Println("Failed to create site")
		http.Error(w, "Failed to create site", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Site updated successfully"))
}

func CreateOfficial(w http.ResponseWriter, r *http.Request) {
	LogVisitor(r)
	var officialJson database.OfficialJson
	err := json.NewDecoder(r.Body).Decode(&officialJson)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	err = oc.Add(oc.ConvJsonToOfficial(officialJson), database.TenantId)
	if err != nil {
		fmt.Println("Failed to create official")
		http.Error(w, "Failed to create official", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Official updated successfully"))
}

func CreateExpense(w http.ResponseWriter, r *http.Request) {

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

	err = ec.Add(ec.ConvJsonToExpense(expenseJson), database.TenantId)
	if err != nil {
		fmt.Println("Failed to create expense")
		http.Error(w, "Failed to create expense", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Expense updated successfully"))
}

func DeleteAssociation(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	fmt.Println("DeleteAssociation called")

	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	assocId := r.PathValue("assocId")

	err := ac.Delete(assocId, database.TenantId)

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

	LogVisitor(r)
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

func DeleteOfficial(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	firstname := r.PathValue("firstName")
	lastname := r.PathValue("lastName")

	fmt.Println("Deleting Official", firstname, lastname)

	err := oc.Delete(firstname, lastname, database.TenantId)

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
	official, err := oc.GetById(id, database.TenantId)

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

	LogVisitor(r)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	siteId := r.PathValue("siteId")

	fmt.Println("Deleting Site", siteId)

	err := sc.Delete(siteId, database.TenantId)

	if err != nil {
		http.Error(w,
			fmt.Sprintf("Delete failed: %v", err),
			http.StatusNotFound,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func GetOfficials(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	official, err := oc.Get(r.PathValue("firstName"), r.PathValue("lastName"), database.TenantId)
	if err != nil {
		http.Error(w, "Official not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(official)
}

func GetSingleAssociation(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	assoc, err := ac.Get(r.PathValue("assocId"), database.TenantId)
	if err != nil {
		http.Error(w, "Association not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assoc)
}

func GetSingleSite(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	site, err := sc.Get(r.PathValue("siteId"), database.TenantId)
	if err != nil {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(site)
}

func GetSingleGame(w http.ResponseWriter, r *http.Request) {

	LogVisitor(r)

	var tId string = database.TenantId

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

	if tId == "na" {
		fmt.Println("Invalid Tenant Id")
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	//
	// Replace Site Id with Site Name
	//
	siteName, err := sc.GetSiteName(game.Site, tId)

	if err == nil {
		game.Site = siteName
	} else {
		fmt.Println("Failed to get site name:", err)
		game.Site = "Unknown"
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(game)

}

func GetPendingGamesCount(w http.ResponseWriter, r *http.Request) {

	var tId string = database.TenantId
	var err error

	fmt.Println("GetPendingGamesCount has been called")

	w.Header().Set("Content-Type", "application/json")

	// Use your application's tenant ID.
	if tId == "na" {
		fmt.Println("Invalid Tenant Id")
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

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

func GetGames(w http.ResponseWriter, r *http.Request) {

	fmt.Println("GetGames has been called")

	LogVisitor(r)

	var games []model.HtmlResponse
	var gameView []model.GameView
	var gameFilters model.GFilters = model.GFilters{}
	var siteId string
	var tId string = database.TenantId
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

	fmt.Println("Begin Date:", begindate, "End Date:", enddate)

	if begindate == "today" && enddate == "" {
		enddate = begindate
	}

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

	fmt.Println("bDate: ", bDate, "eDate:", eDate)
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

	if tId == "na" {
		tId, err = getTenantId(r)

		if err != nil {
			http.Error(w, "Invalid tenant ID", http.StatusBadRequest)
			return
		}
	}

	if len(site) > 0 {
		siteId, err = sc.GetSiteId(site, tId)
		if err != nil {
			http.Error(w, "Invalid site ID", http.StatusBadRequest)
			return
		}
	}

	gameFilters.Status = status
	gameFilters.Association = association
	gameFilters.Level = level
	gameFilters.FromDate = bDate
	gameFilters.ToDate = eDate
	gameFilters.Site = siteId
	gameFilters.Official = official
	gameFilters.TenantId = tId

	fmt.Println("Tenant ID:", tId, "Game Filters Tenant ID:", gameFilters.TenantId)
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

	fmt.Println("Game Filters", gameFilters)

	// 2. Query MongoDB

	opts := options.Find().
		SetSort(bson.D{
			{Key: "gameDateTime", Value: 1},
		})

	fmt.Println("opts:", opts, "filter:", mongoDbFilter)
	cursor, err := coll.Find(context.TODO(), mongoDbFilter, opts)

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

	if len(games) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)

		json.NewEncoder(w).Encode(map[string]string{
			"message": "No games were found matching your search criteria.",
		})

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

		view.GameFee = fmt.Sprintf("$%.2f", float64(gameFee)/100)

		abbrev := utils.DayOfWeekAbbreviation(game.Date)
		view.Date = fmt.Sprintf("%s (%s)", game.Date, abbrev)

		if game.Referee != "" && game.Referee != "Unassigned" {

			ov, error := oc.GetOfficialView(game.Referee, tId)
			if error == nil {
				view.Officials = append(view.Officials, ov)
			}
		}

		if game.U1 != "" && game.U1 != "Unassigned" {

			ov, error := oc.GetOfficialView(game.U1, tId)
			if error == nil {
				view.Officials = append(view.Officials, ov)
			}
		}

		if game.U1 != "" && game.U2 != "Unassigned" {

			ov, error := oc.GetOfficialView(game.U2, tId)
			if error == nil {
				view.Officials = append(view.Officials, ov)
			}
		}

		//view.Officials = reports.FormatOfficialString(game.Referee, game.U1, game.U2)

		gameView = append(gameView, view)
		fmt.Println("view ", view)
	}

	/*
		reptLines := HtmlAssocGameTotals.FormatTotalLine()

		if len(reptLines) > 0 {
			fmt.Println(reptLines)
		}
	*/

	fmt.Println("Returning the following", gameView)

	// 4. Return JSON
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(gameView)

}

func isAuthenticated(r *http.Request) bool {

	cookie, err := r.Cookie("rl_session")
	if err != nil {
		return false
	}

	sessionID := cookie.Value

	collection := database.Client.
		Database("refLedger_v2").
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
			Database("refLedger_v2").
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

func CreateAccount(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var role string = "user"

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "invalid request",
		})
		return
	}

	req.Username = strings.ToLower(strings.TrimSpace(req.Username))

	if !isValidEmail(req.Username) {
		http.Error(w, "Username must be a valid email address.", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "username and password are required",
		})
		return
	}

	usersCollection := database.Client.
		Database("refLedger_v2").
		Collection("users")

	var existingUser model.User

	err = usersCollection.FindOne(
		r.Context(),
		bson.M{"username": req.Username},
	).Decode(&existingUser)

	if err == nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "username already exists",
		})
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "could not create account",
		})
		return
	}

	tenantID := primitive.NewObjectID().Hex()

	user := model.User{
		Username:     req.Username,
		PasswordHash: string(passwordHash),
		TenantID:     tenantID,
		Role:         role,
		CreatedAt:    time.Now(),
		Name:         req.Name,
	}

	_, err = usersCollection.InsertOne(r.Context(), user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "could not save account",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message":  "account created",
		"tenantId": tenantID,
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

	result, err, numOfIndices = oc.IsIndexed()

	if result == false && numOfIndices != 2 {
		err = oc.CreateIndices()
	}

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

	mux.Handle("/images/", http.StripPrefix("/images/",
		http.FileServer(http.Dir("./internal/html/images"))))

	mux.HandleFunc("/expenses", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/expenses.html")
	}))

	mux.HandleFunc("/gameStatus", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/gameStatus.html")
	}))

	mux.HandleFunc("/reports", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/reports.html")
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
	mux.HandleFunc("/api/loadSites", GetSitesHandler)
	mux.HandleFunc("/api/loadAssociations", GetAssociationsHandler)
	mux.HandleFunc("/api/officialsDirectory", GetOfficialsDirectoryHandler)
	mux.HandleFunc("/api/associationsDirectory", GetAssociationsDirectoryHandler)
	mux.HandleFunc("/api/sitesDirectory", GetSitesDirectoryHandler)
	mux.HandleFunc("/api/loadAssignors", GetAssignorsHandler)
	mux.HandleFunc("/api/game/{association}/{gameid}", GetSingleGame)
	mux.HandleFunc("/api/association/{assocId}", GetSingleAssociation)
	mux.HandleFunc("/api/officials/{firstName}/{lastName}", GetOfficials)
	//mux.HandleFunc("/api/deleteAssociation/{assocId}", DeleteAssociation)

	mux.HandleFunc("/api/import/officials/template", authRequired(readOnlyForbidden(DownloadOfficialsTemplateHandler)))
	mux.HandleFunc("/api/import/associations/template", authRequired(readOnlyForbidden(DownloadAssociationsTemplateHandler)))
	mux.HandleFunc("/api/import/sites/template", authRequired(readOnlyForbidden(DownloadSitesTemplateHandler)))
	mux.HandleFunc("/api/import/games/template", authRequired(readOnlyForbidden(DownloadGamesTemplateHandler)))

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
	mux.HandleFunc("/api/expenses", authRequired(readOnlyForbidden(CreateExpense)))
	//mux.HandleFunc("/api/associations", CreateAssociation)

	mux.HandleFunc("/api/associations",
		authRequired(readOnlyForbidden(CreateAssociation)))

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
	mux.HandleFunc("/api/games/status", authRequired(readOnlyForbidden(UpdateGameStatus)))
	mux.HandleFunc("/api/reports", GenerateReport)
	mux.HandleFunc("/api/game-save", authRequired(readOnlyForbidden(SaveGame)))
	mux.HandleFunc("/api/game-update", authRequired(readOnlyForbidden(UpdateGame)))
	mux.HandleFunc("/api/dashboard", GetGames)
	mux.HandleFunc("/api/payments", authRequired(readOnlyForbidden(CreatePayment)))
	mux.HandleFunc("/api/login", ValidateLogin)
	mux.HandleFunc("/api/logout", Logout)
	mux.HandleFunc("/api/forgotPassword", handlers.ForgotPasswordHandler)
	mux.HandleFunc("/api/resetPassword", handlers.ResetPasswordHandler)

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

	mux.Handle("/", authRequired(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./internal/html/index.html")
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
