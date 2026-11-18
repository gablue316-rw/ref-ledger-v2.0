package database

import (
	"context"
	"fmt"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Payment and assignment status changes commit together. A stale payment must
// never change assignment statuses before its own conditional write fails.
func MutatePayment(ctx context.Context, tenant, id, association, version string, change *model.PaymentDescriptor) error {
	if version == "" {
		return ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, err := Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	payments := Client.Database(Database).Collection("payments")
	games := Client.Database(Database).Collection("games")
	var gameChanges int64
	var expenseChange int64
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		gameChanges = 0
		expenseChange = 0
		filter, err := ConditionalFilter(tx, payments, bson.M{"tenantId": tenant, "paymentId": id, "association": association}, version)
		if err != nil {
			return nil, err
		}
		var old model.PaymentDoc
		if err = payments.FindOne(tx, filter).Decode(&old); err != nil {
			return nil, err
		}
		if len(old.GameIds) == 0 {
			return nil, fmt.Errorf("payment has no games")
		}
		var total int64
		seen := map[int64]bool{}
		for _, gameID := range old.GameIds {
			if seen[gameID] {
				return nil, fmt.Errorf("payment contains duplicate game IDs")
			}
			seen[gameID] = true
			identity := bson.M{"tenantId": tenant, "association": association, "gameId": gameID}
			count, err := games.CountDocuments(tx, identity)
			if err != nil {
				return nil, err
			}
			if count != 1 {
				return nil, ErrConflict
			}
			var game model.GameDoc
			if err := games.FindOne(tx, identity).Decode(&game); err != nil {
				return nil, err
			}
			next := "Paid"
			if change == nil {
				if game.Status != "Paid" {
					return nil, ErrConflict
				}
				next = "Completed"
			} else if game.Status != "Paid" && game.Status != "Completed" && game.Status != "Pending" {
				return nil, ErrConflict
			}
			total += gameEarningsCents(game)
			result, err := games.UpdateOne(tx, identity, bson.M{"$set": bson.M{"status": next}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, ErrConflict
			}
			gameChanges += result.ModifiedCount
		}
		if change == nil {
			if err := syncPaymentDeduction(tx, tenant, model.PaymentDoc{
				PaymentId: id, Association: association,
			}); err != nil {
				return nil, fmt.Errorf("remove payroll deduction expense: %w", err)
			}
			result, err := payments.DeleteOne(tx, filter)
			if err != nil {
				return nil, err
			}
			if result.DeletedCount != 1 {
				return nil, ErrConflict
			}
			if old.DeductionAmount > 0 {
				expenseChange = 1
			}
			return nil, nil
		}
		ids, err := utils.ConvertGameIdStrToInt(change.GameIds)
		if err != nil {
			return nil, err
		}
		if change.Association != association || len(ids) != len(seen) {
			return nil, fmt.Errorf("payment association and game IDs cannot be changed")
		}
		submitted := map[int64]bool{}
		for _, gameID := range ids {
			if !seen[gameID] || submitted[gameID] {
				return nil, fmt.Errorf("payment game IDs cannot be changed")
			}
			submitted[gameID] = true
		}
		amount, err := utils.ParseAmountCents(change.PaymentAmt)
		if err != nil {
			return nil, err
		}
		deduction, deductionType, err := NormalizePaymentDeduction(change.DeductionAmount, change.DeductionType)
		if err != nil {
			return nil, err
		}
		if deduction > total {
			return nil, fmt.Errorf("payment deduction exceeds selected-game earnings")
		}
		expectedDeposit := total - deduction
		if expectedDeposit < 0 || amount != expectedDeposit {
			return nil, fmt.Errorf("payment amount must equal selected-game earnings minus the deduction")
		}
		if _, err := time.Parse("1/2/2006", change.PaymentDate); err != nil {
			return nil, err
		}
		year, err := strconv.Atoi(strings.TrimSpace(change.TaxYear))
		if err != nil || year < utils.MinTaxYear || year > utils.MaxTaxYear {
			return nil, fmt.Errorf("invalid tax year")
		}
		updatedPayment := old
		updatedPayment.PaymentDate = change.PaymentDate
		updatedPayment.PaymentAmt = amount
		updatedPayment.TaxYear = year
		updatedPayment.DeductionAmount = deduction
		updatedPayment.DeductionType = deductionType
		result, err := payments.UpdateOne(tx, filter, bson.M{"$set": bson.M{
			"paymentDate":     change.PaymentDate,
			"paymentAmt":      amount,
			"taxYear":         year,
			"deductionAmount": deduction,
			"deductionType":   deductionType,
		}})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != 1 {
			return nil, ErrConflict
		}
		if err := syncPaymentDeduction(tx, tenant, updatedPayment); err != nil {
			return nil, fmt.Errorf("save payroll deduction expense: %w", err)
		}
		if old.DeductionAmount != deduction || old.DeductionType != deductionType {
			expenseChange = 1
		}
		return nil, nil
	})
	if err == nil {
		if gameChanges > 0 {
			MetricsUpdated(tenant, "games", gameChanges)
		}
		if change == nil {
			MetricsDeleted(tenant, "payments", 1)
			if expenseChange > 0 {
				MetricsDeleted(tenant, "expenses", expenseChange)
			}
		} else {
			MetricsUpdated(tenant, "payments", 1)
			if expenseChange > 0 {
				MetricsUpdated(tenant, "expenses", expenseChange)
			}
		}
	}
	return err
}
