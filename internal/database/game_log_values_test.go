package database

import (
	"strings"
	"testing"

	"ref-ledger-v2/internal/model"
)

func TestGameLogValuesOnlyIncludeApprovedSavedFields(t *testing.T) {
	doc := model.GameDoc{GameId: 42, Site: "North", Notes: "private", TenantId: "tenant", GameFee: 25}
	values := savedGameValues(doc)
	if values["gameId"] != int64(42) || values["site"] != "North" || values["gameFee"] != int64(25) {
		t.Fatalf("unexpected values: %#v", values)
	}
	if _, ok := values["notes"]; ok {
		t.Fatal("notes must not be captured")
	}
	doc.Site = strings.Repeat("x", 201)
	if savedGameValues(doc)["site"] != "[redacted]" {
		t.Fatal("oversized values must be redacted")
	}
}

func TestChangedGameValuesReflectSetAndOmitUnchangedFields(t *testing.T) {
	previous := model.GameDoc{Site: "North", Sport: "Soccer", GameFee: 25, Notes: "old"}
	update := model.GameDoc{Site: "South", GameFee: 30, Notes: "new"}
	changes := changedGameValues(previous, update)
	if changes["site"] != (gameLogChange{Old: "North", New: "South"}) ||
		changes["gameFee"] != (gameLogChange{Old: int64(25), New: int64(30)}) {
		t.Fatalf("unexpected changes: %#v", changes)
	}
	if _, ok := changes["sport"]; ok {
		t.Fatal("omitted $set fields must not be reported as changed")
	}
	if _, ok := changes["notes"]; ok {
		t.Fatal("notes must not be captured")
	}
	clean := cleanLogAttributes(map[string]any{"changedValues": changes})
	if _, ok := clean["changedValues"]; !ok {
		t.Fatal("change values must survive attribute sanitization")
	}
}
