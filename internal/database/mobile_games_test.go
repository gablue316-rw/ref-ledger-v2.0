package database

import (
	"context"
	"errors"
	"ref-ledger-v2/internal/model"
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
