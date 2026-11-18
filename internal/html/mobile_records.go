package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func mobileDecode(w http.ResponseWriter, r *http.Request, value interface{}) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		http.Error(w, "Invalid request JSON", 400)
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Provide one JSON object", 400)
		return false
	}
	return true
}

func MobileCreateGame(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Game model.GameDescriptor `json:"game"`
	}
	if !mobileDecode(w, r, &request) {
		return
	}
	g := request.Game
	if err := normalizeMobileGameMoney(&g); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id, err := strconv.ParseInt(g.GameId, 10, 64)
	if err != nil || id <= 0 || strings.TrimSpace(g.Association) == "" {
		http.Error(w, "A positive game ID and association are required", 400)
		return
	}
	g.GameId = strconv.FormatInt(id, 10)
	g.Association = strings.TrimSpace(g.Association)
	if g.Assignor == "" {
		g.Assignor = "Unassigned"
	}
	if err = validateMobileGame(g, g); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(g.Sport) == "" || strings.TrimSpace(g.Level) == "" || strings.TrimSpace(g.Site) == "" {
		http.Error(w, "Sport, level, and site are required", 400)
		return
	}
	tenant, _ := getTenantId(r)
	if err = expenseImportReference(r.Context(), tenant, g.Association); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = api.ValidateGameDescriptor(r.Context(), g, true, tenant); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if g.TaxYear == "" {
		dt, _ := time.Parse("1/2/2006", g.Date)
		g.TaxYear = strconv.Itoa(dt.Year())
	}
	if g.Status == "Cancelled" {
		g.GameFee = "0.00"
		g.TravelPay = "0.00"
		g.AssignorFee = "0.00"
	}
	if err = database.CreateMobileGame(r.Context(), tenant, g); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			http.Error(w, "This game already exists", 409)
		} else {
			http.Error(w, "Unable to create game", 500)
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// MobileExpenseInput uses strings for decimal entry. Currency is converted to
// integer cents and mileage to hundredths, matching the website's storage.
type MobileExpenseInput struct {
	Date        string `json:"date"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Association string `json:"association"`
	GameID      string `json:"gameId"`
	Description string `json:"description"`
	TaxYear     string `json:"taxYear"`
}

func validateMobileExpense(in MobileExpenseInput) (database.ExpenseDoc, error) {
	var doc database.ExpenseDoc
	dt, err := time.Parse("2006-01-02", in.Date)
	if err != nil || dt.Year() < 2000 || dt.Year() > 2100 {
		return doc, errors.New("choose a date between 2000 and 2100")
	}
	value := strings.TrimSpace(in.Value)
	if in.Type != "Mileage" {
		value = "$" + strings.TrimPrefix(value, "$")
	}
	row := validateExpenseImportRow(1, []string{dt.Format("1/2/2006"), in.TaxYear, in.Type, value, in.Association, in.Description})
	if !row.Valid {
		return doc, errors.New(strings.Join(row.Errors, " "))
	}
	gameID := int64(0)
	if strings.TrimSpace(in.GameID) != "" {
		gameID, err = strconv.ParseInt(in.GameID, 10, 64)
		if err != nil || gameID < 0 {
			return doc, errors.New("game ID must be a nonnegative whole number")
		}
	}
	doc = database.ExpenseDoc{Date: row.Data.Date, TaxYear: row.Data.TaxYear, Type: row.Data.Type, Association: row.Data.Association, Description: row.Data.Description, GameId: gameID}
	if doc.Type == "Mileage" {
		doc.Mileage, err = utils.ConvertStrMilesToInt64(row.Data.Amount)
	} else {
		doc.Amount, err = utils.ConvertAmtStrToInt64(row.Data.Amount)
	}
	return doc, err
}
func expenseVersion(raw bson.Raw) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func expenseJSON(raw bson.Raw) (map[string]interface{}, error) {
	var doc model.ExpenseDoc
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return map[string]interface{}{"expense": utils.ConvertExpenseDocToExpenseDescr(doc), "version": expenseVersion(raw)}, nil
}
func MobileExpenses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", 405)
		return
	}
	tenant, _ := getTenantId(r)
	// Dates are legacy m/d/yyyy strings, so compare parsed dates rather than
	// lexicographic MongoDB ranges. Other filters narrow the account query first.
	q := r.URL.Query()
	filter := bson.M{"tenantId": tenant}
	for _, key := range []string{"association", "type", "taxYear", "expenseId"} {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			filter[key] = v
		}
	}
	if v := q.Get("gameId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 0 {
			http.Error(w, "Invalid game ID", 400)
			return
		}
		filter["gameId"] = id
	}
	var start, end time.Time
	var err error
	if v := q.Get("from"); v != "" {
		start, err = time.Parse("2006-01-02", v)
		if err != nil {
			http.Error(w, "Invalid from date", 400)
			return
		}
	}
	if v := q.Get("through"); v != "" {
		end, err = time.Parse("2006-01-02", v)
		if err != nil {
			http.Error(w, "Invalid through date", 400)
			return
		}
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		http.Error(w, "Through date must follow from date", 400)
		return
	}
	cursor, err := database.Client.Database(database.Database).Collection("expenses").Find(r.Context(), filter)
	if err != nil {
		http.Error(w, "Unable to load expenses", 500)
		return
	}
	defer cursor.Close(r.Context())
	result := []map[string]interface{}{}
	for cursor.Next(r.Context()) {
		raw := cursor.Current
		var doc model.ExpenseDoc
		if err = bson.Unmarshal(raw, &doc); err != nil {
			http.Error(w, "Unable to read expenses", 500)
			return
		}
		if !start.IsZero() || !end.IsZero() {
			dt, e := time.Parse("1/2/2006", doc.Date)
			if e != nil || (!start.IsZero() && dt.Before(start)) || (!end.IsZero() && dt.After(end)) {
				continue
			}
		}
		if term := strings.ToLower(strings.TrimSpace(q.Get("description"))); term != "" && !strings.Contains(strings.ToLower(doc.Description), term) {
			continue
		}
		record, e := expenseJSON(raw)
		if e != nil {
			http.Error(w, "Unable to read expenses", 500)
			return
		}
		result = append(result, record)
	}
	if cursor.Err() != nil {
		http.Error(w, "Unable to load expenses", 500)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"items": result, "types": database.GetExpenseTypes(), "canWrite": r.Context().Value(mobileWriteContextKey{}) == true})
}
func MobileExpense(w http.ResponseWriter, r *http.Request) {
	tenant, _ := getTenantId(r)
	coll := database.Client.Database(database.Database).Collection("expenses")
	var original bson.Raw
	if r.Method != http.MethodPost {
		id := strings.TrimSpace(r.URL.Query().Get("expenseId"))
		if id == "" {
			http.Error(w, "Expense ID is required", 400)
			return
		}
		var err error
		original, err = coll.FindOne(r.Context(), bson.M{"tenantId": tenant, "expenseId": id, "association": r.URL.Query().Get("association")}).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			http.Error(w, "Expense not found", 404)
			return
		}
		if err != nil {
			http.Error(w, "Unable to load expense", 500)
			return
		}
		if r.Method == http.MethodGet {
			record, err := expenseJSON(original)
			if err != nil {
				http.Error(w, "Unable to read expense", 500)
				return
			}
			record["canWrite"] = r.Context().Value(mobileWriteContextKey{}) == true
			writeJSON(w, 200, record)
			return
		}
		if r.Method != http.MethodPost {
			var currentExpense model.ExpenseDoc
			if err := bson.Unmarshal(original, &currentExpense); err != nil {
				http.Error(w, "Unable to read expense", 500)
				return
			}
			if currentExpense.Source == database.PayrollDeductionSource || currentExpense.PaymentRef != "" {
				http.Error(w, "Payroll deduction expenses must be changed through their payment", 400)
				return
			}
		}
	}
	var request struct {
		Expense MobileExpenseInput `json:"expense"`
		Version string             `json:"version"`
	}
	if !mobileDecode(w, r, &request) {
		return
	}
	if r.Method != http.MethodPost && (request.Version == "" || request.Version != expenseVersion(original)) {
		http.Error(w, "Expense changed; reopen it before trying again", 409)
		return
	}
	if r.Method == http.MethodDelete {
		result, err := coll.DeleteOne(r.Context(), database.SnapshotFilter(original))
		if err != nil {
			http.Error(w, "Unable to delete expense", 500)
			return
		}
		if result.DeletedCount != 1 {
			http.Error(w, "Expense changed; refresh and try again", 409)
			return
		}
		database.MetricsDeleted(tenant, "expenses", 1)
		w.WriteHeader(204)
		return
	}
	doc, err := validateMobileExpense(request.Expense)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = expenseImportReference(r.Context(), tenant, doc.Association); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	doc.TenantId = tenant
	if r.Method == http.MethodPost {
		doc.ExpenseId = "EXP-" + uuid.NewString()
		if _, err = coll.InsertOne(r.Context(), doc); err != nil {
			http.Error(w, "Unable to add expense", 500)
			return
		}
		database.MetricsAdded(tenant, "expenses", 1)
		w.WriteHeader(201)
		return
	}
	var old database.ExpenseDoc
	if err = bson.Unmarshal(original, &old); err != nil {
		http.Error(w, "Unable to read expense", 500)
		return
	}
	doc.ExpenseId = old.ExpenseId
	// Explicit fields ensure changing from monetary to mileage (or back) clears
	// the old value, even though legacy structs use omitempty.
	update := bson.M{"date": doc.Date, "type": doc.Type, "amount": doc.Amount, "mileage": doc.Mileage, "association": doc.Association, "gameId": doc.GameId, "description": doc.Description, "taxYear": doc.TaxYear}
	result, err := coll.UpdateOne(r.Context(), database.SnapshotFilter(original), bson.M{"$set": update})
	if err != nil {
		http.Error(w, "Unable to update expense", 500)
		return
	}
	if result.MatchedCount != 1 {
		http.Error(w, "Expense changed; reopen it before trying again", 409)
		return
	}
	database.MetricsUpdated(tenant, "expenses", result.ModifiedCount)
	w.WriteHeader(204)
}

func MobileAccess(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{"canWrite": r.Context().Value(mobileWriteContextKey{}) == true, "notesLimit": MaxNotesLength})
}

func normalizeMobileGameMoney(game *model.GameDescriptor) error {
	for _, field := range []*string{&game.GameFee, &game.TravelPay, &game.AssignorFee} {
		value, err := expenseImportValue(*field)
		if err != nil {
			return fmt.Errorf("invalid game fee: %w", err)
		}
		*field = value
	}
	return nil
}
