package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const PayrollDeductionSource = "Payroll Deduction"

func NormalizePaymentDeduction(amount, expenseType string) (int64, string, error) {
	cents, err := utils.ParseAmountCents(amount)
	if err != nil {
		return 0, "", fmt.Errorf("invalid deduction amount: %w", err)
	}
	if cents == 0 {
		return 0, "", nil
	}
	for _, supported := range GetExpenseTypes() {
		if strings.EqualFold(strings.TrimSpace(expenseType), supported.ExpenseType) {
			return cents, supported.ExpenseType, nil
		}
	}
	return 0, "", fmt.Errorf("a supported expense type is required for a positive deduction")
}

func PayrollPaymentReference(association, paymentID string) string {
	association = strings.TrimSpace(association)
	return strconv.Itoa(len(association)) + ":" + association + ":" + strings.TrimSpace(paymentID)
}

func payrollDeductionExpenseID(paymentRef string) string {
	sum := sha256.Sum256([]byte(paymentRef))
	return "PD-" + hex.EncodeToString(sum[:])
}

func paymentDeductionExpense(tenant string, payment model.PaymentDoc, paymentRef string) model.ExpenseDoc {
	return model.ExpenseDoc{
		ExpenseId:   payrollDeductionExpenseID(paymentRef),
		Date:        payment.PaymentDate,
		Type:        payment.DeductionType,
		Amount:      payment.DeductionAmount,
		Association: payment.Association,
		Description: "Payroll deduction from payment " + payment.PaymentId,
		TaxYear:     strconv.Itoa(payment.TaxYear),
		TenantId:    tenant,
		Source:      PayrollDeductionSource,
		PaymentRef:  paymentRef,
	}
}

func syncPaymentDeduction(ctx mongo.SessionContext, tenant string, payment model.PaymentDoc) error {
	return syncPaymentDeductionInCollection(
		ctx,
		Client.Database(Database).Collection("expenses"),
		tenant,
		payment,
	)
}

func syncPaymentDeductionInCollection(
	ctx mongo.SessionContext,
	expenses *mongo.Collection,
	tenant string,
	payment model.PaymentDoc,
) error {
	paymentRef := PayrollPaymentReference(payment.Association, payment.PaymentId)
	filter := bson.M{
		"tenantId": tenant, "paymentRef": paymentRef,
		"source": PayrollDeductionSource,
	}
	if payment.DeductionAmount == 0 {
		_, err := expenses.DeleteMany(ctx, filter)
		return err
	}
	doc := paymentDeductionExpense(tenant, payment, paymentRef)
	_, err := expenses.UpdateOne(
		ctx,
		filter,
		bson.M{
			"$set": bson.M{
				"date":        doc.Date,
				"type":        doc.Type,
				"amount":      doc.Amount,
				"mileage":     int64(0),
				"association": doc.Association,
				"gameId":      int64(0),
				"description": doc.Description,
				"taxYear":     doc.TaxYear,
				"source":      doc.Source,
			},
			"$setOnInsert": bson.M{
				"expenseId":  doc.ExpenseId,
				"tenantId":   doc.TenantId,
				"paymentRef": doc.PaymentRef,
			},
		},
		options.Update().SetUpsert(true),
	)
	return err
}

func CreatePayrollDeductionIndexes(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := Client.Database(Database).Collection("expenses").Indexes().CreateOne(
		ctx,
		mongo.IndexModel{
			Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "paymentRef", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetName("payroll_deduction_tenant_payment_unique").
				SetPartialFilterExpression(bson.M{"source": PayrollDeductionSource}),
		},
	)
	if err != nil {
		return fmt.Errorf("create payroll deduction expense index: %w", err)
	}
	return nil
}

func gameEarningsCents(game model.GameDoc) int64 {
	return game.GameFee*game.NumOfGames + game.TravelPay - game.AssignorFee
}
