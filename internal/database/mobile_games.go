package database

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"ref-ledger-v2/internal/model"
)

var ErrMobileConflict = errors.New("game changed since it was loaded; refresh the game list and retry your action")
var ErrMobilePaid = errors.New("paid games cannot be edited or deleted")

// MobileGameSnapshot retains the exact database predicates for a conditional write.
type MobileGameSnapshot struct {
	Game    model.GameDescriptor
	Version string
	Filter  bson.Raw
	Doc     model.GameDoc
}

func LoadMobileGame(ctx context.Context, tenant, association string, id int64) (MobileGameSnapshot, error) {
	var s MobileGameSnapshot
	raw, err := Client.Database(Database).Collection("games").FindOne(ctx, bson.M{"tenantId": tenant, "association": association, "gameId": id}).Raw()
	if err != nil {
		return s, err
	}
	return decodeMobileGameSnapshot(raw)
}

func decodeMobileGameSnapshot(raw bson.Raw) (MobileGameSnapshot, error) {
	var s MobileGameSnapshot
	if err := raw.Validate(); err != nil {
		return s, err
	}
	// Keep embedded documents in their stored BSON order. Decoding the filter
	// into maps reorders officials on re-encoding, so MongoDB equality can reject
	// an unchanged record. Copy bytes because the query result owns its buffer.
	s.Filter = append(bson.Raw(nil), raw...)
	if err := bson.Unmarshal(raw, &s.Doc); err != nil {
		return s, err
	}
	s.Game = convertGameDocWithOfficials(s.Doc)
	s.Version = fmt.Sprintf("%x", sha256.Sum256(s.Filter))
	return s, nil
}
func WriteMobileGame(ctx context.Context, s MobileGameSnapshot, game *model.GameDescriptor, version string) error {
	if s.Doc.Status == "Paid" {
		return ErrMobilePaid
	}
	if version == "" || version != s.Version {
		return ErrMobileConflict
	}
	coll := Client.Database(Database).Collection("games")
	// Match all original fields, including status, to reject concurrent changes/payments.
	if game == nil {
		result, err := coll.DeleteOne(ctx, s.Filter)
		if err != nil {
			return err
		}
		if result.DeletedCount != 1 {
			return ErrMobileConflict
		}
		MetricsDeleted(s.Doc.TenantId, "games", 1)
		return nil
	}
	doc := convertGameDescrWithOfficials(*game)
	doc.TenantId = s.Doc.TenantId
	var err error
	doc.GameDateTime, err = convertDateStringToTime(game.Date, game.Time)
	if err != nil {
		return err
	}
	if err = prepareGameOfficials(ctx, coll.Database(), &doc, &s.Doc); err != nil {
		return err
	}
	result, err := coll.UpdateOne(ctx, s.Filter, bson.M{"$set": doc, "$unset": bson.M{"referee": "", "u1": "", "u2": "", "eco": ""}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrMobileConflict
	}
	MetricsUpdated(s.Doc.TenantId, "games", result.ModifiedCount)
	return nil
}
