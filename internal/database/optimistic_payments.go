package database

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strconv"
	"strings"
	"time"
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
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		gameChanges = 0
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
			total += utils.CalculateGameFee(utils.ConvertGameDocToGameDescr(game))
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
			result, err := payments.DeleteOne(tx, filter)
			if err != nil {
				return nil, err
			}
			if result.DeletedCount != 1 {
				return nil, ErrConflict
			}
			return nil, nil
		}
		// Editing a payment retains its original association and assignment membership.
		ids, err := utils.ConvertGameIdStrToInt(change.GameIds)
		if err != nil {
			return nil, err
		}
		if change.Association != association || len(ids) != len(seen) {
			return nil, fmt.Errorf("payment association and game IDs cannot be changed")
		}
		submitted := map[int64]bool{}
		for _, id := range ids {
			if !seen[id] || submitted[id] {
				return nil, fmt.Errorf("payment game IDs cannot be changed")
			}
			submitted[id] = true
		}
		amount, err := utils.ConvertAmtStrToInt64(change.PaymentAmt)
		if err != nil {
			return nil, err
		}
		if amount != total {
			return nil, fmt.Errorf("payment amount must equal selected game earnings")
		}
		if _, err := time.Parse("1/2/2006", change.PaymentDate); err != nil {
			return nil, err
		}
		year, err := strconv.Atoi(strings.TrimSpace(change.TaxYear))
		if err != nil || year < utils.MinTaxYear || year > utils.MaxTaxYear {
			return nil, fmt.Errorf("invalid tax year")
		}
		result, err := payments.UpdateOne(tx, filter, bson.M{"$set": bson.M{"paymentDate": change.PaymentDate, "paymentAmt": amount, "taxYear": year}})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != 1 {
			return nil, ErrConflict
		}
		return nil, nil
	})
	if err == nil {
		if gameChanges > 0 {
			MetricsUpdated(tenant, "games", gameChanges)
		}
		if change == nil {
			MetricsDeleted(tenant, "payments", 1)
		} else {
			MetricsUpdated(tenant, "payments", 1)
		}
	}
	return err
}
