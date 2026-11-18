package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type HistoricalMigrationIssue struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	TenantID    string `json:"tenantId,omitempty"`
	Association string `json:"association,omitempty"`
	GameID      int64  `json:"gameId,omitempty"`
	PaymentID   string `json:"paymentId,omitempty"`
	Message     string `json:"message"`
}

type HistoricalMigrationTotal struct {
	TenantID    string `json:"tenantId"`
	Association string `json:"association"`
	TaxYear     string `json:"taxYear"`
	AmountCents int64  `json:"amountCents"`
	Source      string `json:"source"`
}

type HistoricalMigrationEntry struct {
	TenantID               string   `json:"tenantId"`
	Association            string   `json:"association"`
	PaymentID              string   `json:"paymentId"`
	PaymentDate            string   `json:"paymentDate"`
	PaymentTaxYear         int      `json:"paymentTaxYear"`
	ExpenseType            string   `json:"expenseType"`
	AmountCents            int64    `json:"amountCents"`
	LegacyGameIDs          []int64  `json:"legacyGameIds"`
	LegacyGameTaxYears     []string `json:"legacyGameTaxYears"`
	ExistingLinkedExpenses int      `json:"existingLinkedExpenses"`
	Action                 string   `json:"action"`
}

type HistoricalPayrollMigrationReport struct {
	Database       string                     `json:"database"`
	GeneratedAt    time.Time                  `json:"generatedAt"`
	ReadyToApply   bool                       `json:"readyToApply"`
	PlannedChanges int                        `json:"plannedChanges"`
	AppliedChanges int                        `json:"appliedChanges"`
	Issues         []HistoricalMigrationIssue `json:"issues"`
	Entries        []HistoricalMigrationEntry `json:"entries"`
	Totals         []HistoricalMigrationTotal `json:"totals"`
}

type HistoricalPayrollMigrationPlan struct {
	Report  HistoricalPayrollMigrationReport
	changes []historicalPayrollMigrationChange
}

type historicalPayrollMigrationChange struct {
	before model.PaymentDoc
	after  model.PaymentDoc
	games  []model.GameDoc
}

type historicalTotalGroup struct {
	tenantID, association, taxYear, source string
}

func HistoricalMigrationTypeMapKey(tenantID, association, paymentID string) string {
	tenantID = strings.TrimSpace(tenantID)
	association = strings.TrimSpace(association)
	paymentID = strings.TrimSpace(paymentID)
	return fmt.Sprintf(
		"%d:%s:%d:%s:%d:%s",
		len(tenantID), tenantID,
		len(association), association,
		len(paymentID), paymentID,
	)
}

func PlanHistoricalPayrollDeductionMigration(
	ctx context.Context,
	db *mongo.Database,
	typeMap map[string][]string,
) (HistoricalPayrollMigrationPlan, error) {
	plan := HistoricalPayrollMigrationPlan{
		Report: HistoricalPayrollMigrationReport{
			Database:    db.Name(),
			GeneratedAt: time.Now().UTC(),
			Issues:      []HistoricalMigrationIssue{},
			Entries:     []HistoricalMigrationEntry{},
			Totals:      []HistoricalMigrationTotal{},
		},
	}

	games, err := loadHistoricalDeductionGames(ctx, db)
	if err != nil {
		return plan, err
	}
	payments, err := loadHistoricalDeductionPayments(ctx, db)
	if err != nil {
		return plan, err
	}
	expenses, err := loadHistoricalDeductionExpenses(ctx, db)
	if err != nil {
		return plan, err
	}

	paymentByKey := make(map[string]model.PaymentDoc, len(payments))
	paymentKeysByGame := make(map[string][]string)
	blockedPayments := make(map[string]bool)
	for _, payment := range payments {
		key := HistoricalMigrationTypeMapKey(payment.TenantId, payment.Association, payment.PaymentId)
		if _, exists := paymentByKey[key]; exists {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "duplicate_payment_identity",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId,
				Message:   "Multiple payment records have the same tenant, association, and payment ID.",
			})
			continue
		}
		paymentByKey[key] = payment
		seenIDs := make(map[int64]bool, len(payment.GameIds))
		for _, id := range payment.GameIds {
			if seenIDs[id] {
				blockedPayments[key] = true
				addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
					Severity: "error", Code: "duplicate_payment_game_id",
					TenantID: payment.TenantId, Association: payment.Association,
					PaymentID: payment.PaymentId, GameID: id,
					Message: "The payment contains the same game ID more than once.",
				})
				continue
			}
			seenIDs[id] = true
			gameKey := historicalGameKey(payment.TenantId, payment.Association, id)
			paymentKeysByGame[gameKey] = append(paymentKeysByGame[gameKey], key)
		}
	}

	gameByKey := make(map[string]model.GameDoc, len(games))
	gameYearTotals := make(map[historicalTotalGroup]int64)
	groupedGames := make(map[string][]model.GameDoc)
	for _, game := range games {
		gameKey := historicalGameKey(game.TenantId, game.Association, game.GameId)
		if _, exists := gameByKey[gameKey]; exists {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "duplicate_game_identity",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: "Multiple game records have the same tenant, association, and game ID.",
			})
			continue
		}
		gameByKey[gameKey] = game
		if game.Deductions < 0 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "negative_legacy_deduction",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: "Legacy game deduction is negative.",
			})
			continue
		}
		if game.Deductions == 0 {
			continue
		}
		year := normalizeHistoricalTaxYear(game.TaxYear)
		if year == "" {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "invalid_game_tax_year",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: "Legacy game deduction has a missing or invalid tax year.",
			})
		} else {
			totalKey := historicalTotalGroup{
				tenantID: game.TenantId, association: game.Association,
				taxYear: year, source: "legacy_game",
			}
			gameYearTotals[totalKey] += game.Deductions
		}
		candidateKeys := paymentKeysByGame[gameKey]
		if game.Status != "Paid" {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "unpaid_game_with_deduction",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: "Game with a legacy deduction is not Paid.",
			})
			for _, key := range candidateKeys {
				blockedPayments[key] = true
			}
			continue
		}
		if len(candidateKeys) == 0 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "unmatched_game_with_deduction",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: "Paid game with a legacy deduction is not linked to a payment for the same tenant and association.",
			})
			continue
		}
		if len(candidateKeys) != 1 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "game_linked_to_multiple_payments",
				TenantID: game.TenantId, Association: game.Association,
				GameID: game.GameId, Message: fmt.Sprintf("Game is linked to %d payments.", len(candidateKeys)),
			})
			for _, key := range candidateKeys {
				blockedPayments[key] = true
			}
			continue
		}
		groupedGames[candidateKeys[0]] = append(groupedGames[candidateKeys[0]], game)
	}

	linkedExpenses := make(map[string][]model.ExpenseDoc)
	manualExpenses := make(map[string][]model.ExpenseDoc)
	for _, expense := range expenses {
		if expense.Source == PayrollDeductionSource {
			if strings.TrimSpace(expense.PaymentRef) == "" {
				addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
					Severity: "error", Code: "linked_expense_missing_payment_reference",
					TenantID: expense.TenantId, Association: expense.Association,
					Message: fmt.Sprintf("Payroll deduction expense %s has no payment reference.", expense.ExpenseId),
				})
			} else {
				key := historicalTenantPaymentKey(expense.TenantId, expense.PaymentRef)
				linkedExpenses[key] = append(linkedExpenses[key], expense)
			}
		} else if expense.PaymentRef == "" {
			key := historicalManualExpenseKey(expense.TenantId, expense.Association, expense.Date, expense.Amount)
			manualExpenses[key] = append(manualExpenses[key], expense)
		}
	}

	for paymentKey, gamesForPayment := range groupedGames {
		payment, exists := paymentByKey[paymentKey]
		if !exists || blockedPayments[paymentKey] {
			continue
		}
		amount := int64(0)
		gameIDs := make([]int64, 0, len(gamesForPayment))
		gameYears := make(map[string]bool)
		for _, game := range gamesForPayment {
			amount += game.Deductions
			gameIDs = append(gameIDs, game.GameId)
			year := normalizeHistoricalTaxYear(game.TaxYear)
			if year != "" {
				gameYears[year] = true
			}
			if year == "" || payment.TaxYear < 1000 || payment.TaxYear > 9999 {
				continue
			}
			if year != strconv.Itoa(payment.TaxYear) {
				addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
					Severity: "warning", Code: "game_payment_tax_year_difference",
					TenantID: payment.TenantId, Association: payment.Association,
					GameID: game.GameId, PaymentID: payment.PaymentId,
					Message: fmt.Sprintf("Game tax year %s differs from payment tax year %d; the linked expense will use the payment tax year.", year, payment.TaxYear),
				})
			}
		}
		sort.Slice(gameIDs, func(i, j int) bool { return gameIDs[i] < gameIDs[j] })
		years := make([]string, 0, len(gameYears))
		for year := range gameYears {
			years = append(years, year)
		}
		sort.Strings(years)

		if payment.TaxYear < 1000 || payment.TaxYear > 9999 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "invalid_payment_tax_year",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "Payment has an invalid tax year.",
			})
			continue
		}
		if payment.DeductionAmount < 0 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "negative_payment_deduction",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "Payment already has a negative deduction amount.",
			})
			continue
		}

		ref := PayrollPaymentReference(payment.Association, payment.PaymentId)
		existing := linkedExpenses[historicalTenantPaymentKey(payment.TenantId, ref)]
		if len(existing) > 1 {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "duplicate_linked_expenses",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: fmt.Sprintf("Found %d linked payroll deduction expenses for this payment.", len(existing)),
			})
			continue
		}

		mappedTypes := typeMap[HistoricalMigrationTypeMapKey(payment.TenantId, payment.Association, payment.PaymentId)]
		var expenseType string
		switch {
		case payment.DeductionAmount != 0 && payment.DeductionAmount != amount:
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "payment_deduction_mismatch",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: fmt.Sprintf("Payment already stores %d cents but linked legacy games total %d cents.", payment.DeductionAmount, amount),
			})
			continue
		case payment.DeductionAmount == amount && payment.DeductionType != "":
			expenseType = payment.DeductionType
		case len(existing) == 1 && existing[0].Amount == amount:
			expenseType = existing[0].Type
		case len(mappedTypes) == 1:
			expenseType = mappedTypes[0]
		case len(mappedTypes) > 1:
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "multiple_verified_expense_types",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "More than one expense type is mapped to this payment.",
			})
			continue
		default:
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "unknown_expense_type",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "No verified expense type is available; add exactly one type-map row for this payment.",
			})
			continue
		}
		normalizedAmount, normalizedType, err := NormalizePaymentDeduction(utils.ConvertInt64ToAmtStr(amount), expenseType)
		if err != nil || normalizedAmount != amount {
			message := "Verified expense type is not supported."
			if err != nil {
				message = err.Error()
			}
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "invalid_verified_expense_type",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: message,
			})
			continue
		}
		if len(mappedTypes) > 1 || (len(mappedTypes) == 1 && !strings.EqualFold(strings.TrimSpace(mappedTypes[0]), normalizedType)) {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "expense_type_mapping_conflict",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "Verified type map conflicts with the type already stored on the payment or linked expense.",
			})
			continue
		}
		if payment.DeductionAmount == 0 && len(existing) == 1 && existing[0].Amount != amount {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "linked_expense_amount_mismatch",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "Existing linked expense amount differs from the legacy game total.",
			})
			continue
		}
		if len(existing) == 1 && (existing[0].Amount != amount ||
			!strings.EqualFold(existing[0].Type, normalizedType) ||
			existing[0].TaxYear != strconv.Itoa(payment.TaxYear) ||
			existing[0].Date != payment.PaymentDate ||
			existing[0].Association != payment.Association) {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "linked_expense_mismatch",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: "Existing linked expense does not match the payment date, tax year, association, type, or amount.",
			})
			continue
		}

		manualMatches := manualExpenses[historicalManualExpenseKey(
			payment.TenantId, payment.Association, payment.PaymentDate, amount,
		)]
		for _, expense := range manualMatches {
			addHistoricalMigrationIssue(&plan.Report, HistoricalMigrationIssue{
				Severity: "error", Code: "possible_duplicate_manual_expense",
				TenantID: payment.TenantId, Association: payment.Association,
				PaymentID: payment.PaymentId, Message: fmt.Sprintf("Manual expense %s has the same date, association, and amount; review for a possible duplicate.", expense.ExpenseId),
			})
		}

		after := payment
		after.DeductionAmount = amount
		after.DeductionType = normalizedType
		action := "create_linked_expense"
		if len(existing) == 1 {
			action = "verify_existing_link"
		}
		entry := HistoricalMigrationEntry{
			TenantID: payment.TenantId, Association: payment.Association,
			PaymentID: payment.PaymentId, PaymentDate: payment.PaymentDate,
			PaymentTaxYear: payment.TaxYear, ExpenseType: normalizedType,
			AmountCents: amount, LegacyGameIDs: gameIDs,
			LegacyGameTaxYears: years, ExistingLinkedExpenses: len(existing),
			Action: action,
		}
		plan.Report.Entries = append(plan.Report.Entries, entry)
		plan.changes = append(plan.changes, historicalPayrollMigrationChange{
			before: payment, after: after, games: gamesForPayment,
		})
		plan.Report.PlannedChanges++
	}

	for key, amount := range gameYearTotals {
		plan.Report.Totals = append(plan.Report.Totals, HistoricalMigrationTotal{
			TenantID: key.tenantID, Association: key.association, TaxYear: key.taxYear,
			AmountCents: amount, Source: "legacy_game_tax_year",
		})
	}
	paymentYearTotals := make(map[historicalTotalGroup]int64)
	for _, entry := range plan.Report.Entries {
		key := historicalTotalGroup{
			tenantID: entry.TenantID, association: entry.Association,
			taxYear: strconv.Itoa(entry.PaymentTaxYear), source: "payment_tax_year",
		}
		paymentYearTotals[key] += entry.AmountCents
	}
	for key, amount := range paymentYearTotals {
		plan.Report.Totals = append(plan.Report.Totals, HistoricalMigrationTotal{
			TenantID: key.tenantID, Association: key.association, TaxYear: key.taxYear,
			AmountCents: amount, Source: "proposed_payment_tax_year",
		})
	}
	sort.Slice(plan.Report.Entries, func(i, j int) bool {
		a, b := plan.Report.Entries[i], plan.Report.Entries[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.Association != b.Association {
			return a.Association < b.Association
		}
		return a.PaymentID < b.PaymentID
	})
	sort.Slice(plan.Report.Totals, func(i, j int) bool {
		a, b := plan.Report.Totals[i], plan.Report.Totals[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.Association != b.Association {
			return a.Association < b.Association
		}
		if a.TaxYear != b.TaxYear {
			return a.TaxYear < b.TaxYear
		}
		return a.Source < b.Source
	})
	plan.Report.ReadyToApply = true
	for _, issue := range plan.Report.Issues {
		if issue.Severity == "error" {
			plan.Report.ReadyToApply = false
		}
	}
	return plan, nil
}

func ApplyHistoricalPayrollDeductionMigration(
	ctx context.Context,
	db *mongo.Database,
	plan *HistoricalPayrollMigrationPlan,
	approveTaxYearDifferences bool,
) error {
	if !plan.Report.ReadyToApply {
		return errors.New("migration plan has unresolved errors")
	}
	for _, issue := range plan.Report.Issues {
		if issue.Code == "game_payment_tax_year_difference" && !approveTaxYearDifferences {
			return errors.New("migration plan has game/payment tax-year differences; review and explicitly approve them before applying")
		}
	}
	if _, err := db.Collection("expenses").Indexes().CreateOne(
		ctx,
		mongo.IndexModel{
			Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "paymentRef", Value: 1}},
			Options: options.Index().SetUnique(true).
				SetName("payroll_deduction_tenant_payment_unique").
				SetPartialFilterExpression(bson.M{"source": PayrollDeductionSource}),
		},
	); err != nil {
		return fmt.Errorf("create payroll deduction expense index: %w", err)
	}

	for _, change := range plan.changes {
		session, err := db.Client().StartSession()
		if err != nil {
			return fmt.Errorf("start migration transaction for payment %s: %w", change.before.PaymentId, err)
		}
		_, transactionErr := session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
			payments := db.Collection("payments")
			games := db.Collection("games")
			var current model.PaymentDoc
			paymentFilter := bson.M{
				"tenantId": change.before.TenantId, "association": change.before.Association,
				"paymentId": change.before.PaymentId,
			}
			if err := payments.FindOne(tx, paymentFilter).Decode(&current); err != nil {
				return nil, fmt.Errorf("reload payment: %w", err)
			}
			if !sameHistoricalPayment(current, change.before) {
				return nil, errors.New("payment changed since migration preview")
			}
			currentGameTotal := int64(0)
			for _, expected := range change.games {
				var currentGame model.GameDoc
				filter := bson.M{
					"tenantId": expected.TenantId, "association": expected.Association,
					"gameId": expected.GameId,
				}
				if err := games.FindOne(tx, filter).Decode(&currentGame); err != nil {
					return nil, fmt.Errorf("reload game %d: %w", expected.GameId, err)
				}
				if currentGame.Status != expected.Status ||
					currentGame.Deductions != expected.Deductions ||
					currentGame.TaxYear != expected.TaxYear ||
					currentGame.Status != "Paid" {
					return nil, fmt.Errorf("game %d changed since migration preview", expected.GameId)
				}
				currentGameTotal += currentGame.Deductions
			}
			if currentGameTotal != change.after.DeductionAmount {
				return nil, errors.New("legacy game deduction total changed since migration preview")
			}
			manualFilter := bson.M{
				"tenantId": change.after.TenantId, "association": change.after.Association,
				"date": change.after.PaymentDate, "amount": change.after.DeductionAmount,
				"source":     bson.M{"$ne": PayrollDeductionSource},
				"paymentRef": bson.M{"$in": bson.A{"", nil}},
			}
			err := db.Collection("expenses").FindOne(tx, manualFilter).Err()
			if err == nil {
				return nil, errors.New("possible duplicate manual expense appeared after preview")
			}
			if !errors.Is(err, mongo.ErrNoDocuments) {
				return nil, fmt.Errorf("check for duplicate manual expense: %w", err)
			}
			result, err := payments.UpdateOne(tx, paymentFilter, bson.M{"$set": bson.M{
				"deductionAmount": change.after.DeductionAmount,
				"deductionType":   change.after.DeductionType,
			}})
			if err != nil {
				return nil, fmt.Errorf("update payment: %w", err)
			}
			if result.MatchedCount != 1 {
				return nil, errors.New("payment changed during migration")
			}
			if err := syncPaymentDeductionInCollection(
				tx, db.Collection("expenses"), change.after.TenantId, change.after,
			); err != nil {
				return nil, fmt.Errorf("upsert linked payroll expense: %w", err)
			}
			return nil, nil
		})
		session.EndSession(ctx)
		if transactionErr != nil {
			return fmt.Errorf("migrate payment %s: %w", change.before.PaymentId, transactionErr)
		}
		plan.Report.AppliedChanges++
	}
	return nil
}

func loadHistoricalDeductionGames(ctx context.Context, db *mongo.Database) ([]model.GameDoc, error) {
	cursor, err := db.Collection("games").Find(ctx, bson.M{
		"deductions": bson.M{"$exists": true, "$ne": int64(0)},
	})
	if err != nil {
		return nil, fmt.Errorf("query legacy deduction games: %w", err)
	}
	defer cursor.Close(ctx)
	var games []model.GameDoc
	if err := cursor.All(ctx, &games); err != nil {
		return nil, fmt.Errorf("read legacy deduction games: %w", err)
	}
	return games, nil
}

func loadHistoricalDeductionPayments(ctx context.Context, db *mongo.Database) ([]model.PaymentDoc, error) {
	cursor, err := db.Collection("payments").Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("query payments: %w", err)
	}
	defer cursor.Close(ctx)
	var payments []model.PaymentDoc
	if err := cursor.All(ctx, &payments); err != nil {
		return nil, fmt.Errorf("read payments: %w", err)
	}
	return payments, nil
}

func loadHistoricalDeductionExpenses(ctx context.Context, db *mongo.Database) ([]model.ExpenseDoc, error) {
	cursor, err := db.Collection("expenses").Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("query expenses: %w", err)
	}
	defer cursor.Close(ctx)
	var expenses []model.ExpenseDoc
	if err := cursor.All(ctx, &expenses); err != nil {
		return nil, fmt.Errorf("read expenses: %w", err)
	}
	return expenses, nil
}

func addHistoricalMigrationIssue(report *HistoricalPayrollMigrationReport, issue HistoricalMigrationIssue) {
	report.Issues = append(report.Issues, issue)
}

func historicalGameKey(tenantID, association string, gameID int64) string {
	return fmt.Sprintf("%d:%s:%d:%s:%d", len(tenantID), tenantID, len(association), association, gameID)
}

func historicalTenantPaymentKey(tenantID, paymentRef string) string {
	return fmt.Sprintf("%d:%s:%d:%s", len(tenantID), tenantID, len(paymentRef), paymentRef)
}

func historicalManualExpenseKey(tenantID, association, date string, amount int64) string {
	return fmt.Sprintf("%d:%s:%d:%s:%d:%s:%d",
		len(tenantID), tenantID, len(association), association, len(date), date, amount)
}

func normalizeHistoricalTaxYear(value string) string {
	year, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || year < 1000 || year > 9999 {
		return ""
	}
	return strconv.Itoa(year)
}

func sameHistoricalPayment(current, planned model.PaymentDoc) bool {
	return current.TenantId == planned.TenantId &&
		current.Association == planned.Association &&
		current.PaymentId == planned.PaymentId &&
		current.PaymentDate == planned.PaymentDate &&
		current.PaymentAmt == planned.PaymentAmt &&
		current.TaxYear == planned.TaxYear &&
		current.DeductionAmount == planned.DeductionAmount &&
		current.DeductionType == planned.DeductionType &&
		reflect.DeepEqual(current.GameIds, planned.GameIds)
}
