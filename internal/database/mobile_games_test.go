package database

import (
	"bytes"
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"ref-ledger-v2/internal/model"
	"strings"
	"testing"
)

func TestMobileSnapshotRejectsStaleAndPaidWritesBeforeDatabase(t *testing.T) {
	old := MobileGameSnapshot{Version: "current", Doc: model.GameDoc{Status: "Pending"}}
	for _, version := range []string{"", "stale"} {
		if err := WriteMobileGame(context.Background(), old, nil, version); !errors.Is(err, ErrMobileConflict) {
			t.Fatalf("delete version %q: %v", version, err)
		}
		if err := WriteMobileGame(context.Background(), old, &model.GameDescriptor{}, version); !errors.Is(err, ErrMobileConflict) {
			t.Fatalf("update version %q: %v", version, err)
		}
	}
	old.Doc.Status = "Paid"
	if err := WriteMobileGame(context.Background(), old, nil, "current"); !errors.Is(err, ErrMobilePaid) {
		t.Fatal(err)
	}
	if err := WriteMobileGame(context.Background(), old, &model.GameDescriptor{}, "current"); !errors.Is(err, ErrMobilePaid) {
		t.Fatal(err)
	}
}

// Exact embedded-document equality is order-sensitive in MongoDB. Using raw
// BSON must retain the stored officials array, not rebuild it from Go maps.
func TestMobileSnapshotPreservesOfficialDocumentOrder(t *testing.T) {
	original := bson.D{
		{Key: "_id", Value: primitive.NewObjectID()},
		{Key: "tenantId", Value: "tenant-one"},
		{Key: "association", Value: "GOLLC"},
		{Key: "gameId", Value: int64(71)},
		{Key: "status", Value: "Pending"},
		{Key: "gameFee", Value: int64(4500)},
		{Key: "officials", Value: bson.A{bson.D{
			{Key: "title", Value: "Plate"},
			{Key: "roleId", Value: "plate"},
			{Key: "name", Value: "John Doe"},
			{Key: "displayOrder", Value: int32(1)},
			{Key: "officialId", Value: "9"},
		}}},
	}
	raw, err := bson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var version string
	for i := 0; i < 100; i++ {
		snapshot, err := decodeMobileGameSnapshot(raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := bson.Marshal(snapshot.Filter)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, encoded) {
			t.Fatal("conditional update/delete changed stored BSON field order")
		}
		if !bytes.Equal(bson.Raw(raw).Lookup("officials").Value, bson.Raw(encoded).Lookup("officials").Value) {
			t.Fatal("official order changed")
		}
		if version != "" && snapshot.Version != version {
			t.Fatal("unchanged game produced a different version")
		}
		version = snapshot.Version
	}
	// A genuine edit still produces a different version.
	original[4].Value = "Completed"
	changed, _ := bson.Marshal(original)
	snapshot, err := decodeMobileGameSnapshot(changed)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version == version {
		t.Fatal("changed status did not change version")
	}
	// The snapshot must own its bytes, rather than sharing the caller's buffer.
	saved := append([]byte(nil), snapshot.Filter...)
	changed[0] ^= 1
	if !bytes.Equal(saved, snapshot.Filter) {
		t.Fatal("snapshot shares query buffer")
	}
}
func TestMobileConflictMessageAppliesToDeletesAndUpdates(t *testing.T) {
	if strings.Contains(ErrMobileConflict.Error(), "saving") || !strings.Contains(ErrMobileConflict.Error(), "refresh") {
		t.Fatal("conflict message must describe both operations")
	}
}
