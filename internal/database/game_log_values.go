package database

import (
	"reflect"

	"ref-ledger-v2/internal/model"

	"go.mongodb.org/mongo-driver/bson"
)

type gameLogValues map[string]any
type gameLogChanges map[string]gameLogChange

type gameLogChange struct {
	Old any `bson:"old" json:"old"`
	New any `bson:"new" json:"new"`
}

var gameLogFields = []string{
	"gameId", "date", "time", "sport", "site", "field", "numOfGames",
	"level", "home", "visitor", "gameFee", "travelPay", "assignorFee",
	"deductions", "association", "status", "taxYear",
}

func gameLogDocument(doc model.GameDoc) bson.M {
	raw, err := bson.Marshal(doc)
	if err != nil {
		return nil
	}
	var result bson.M
	if bson.Unmarshal(raw, &result) != nil {
		return nil
	}
	return result
}

func gameLogValue(value any) any {
	switch v := value.(type) {
	case string:
		return safeLogText(v)
	case int32, int64, int, nil:
		return v
	default:
		return nil
	}
}

func savedGameValues(doc model.GameDoc) gameLogValues {
	stored := gameLogDocument(doc)
	result := make(gameLogValues)
	for _, field := range gameLogFields {
		if value, ok := stored[field]; ok {
			result[field] = gameLogValue(value)
		}
	}
	return result
}

// Updates use $set with omitempty, so absent fields retain their old values.
func changedGameValues(previous, update model.GameDoc) gameLogChanges {
	old, set := gameLogDocument(previous), gameLogDocument(update)
	result := make(gameLogChanges)
	for _, field := range gameLogFields {
		value, ok := set[field]
		if ok && !reflect.DeepEqual(old[field], value) {
			result[field] = gameLogChange{Old: gameLogValue(old[field]), New: gameLogValue(value)}
		}
	}
	return result
}
