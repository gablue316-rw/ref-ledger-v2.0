package database

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var ErrConflict = errors.New("record changed or was removed since it was loaded; refresh or reopen it before trying again")

func RecordVersion(raw bson.Raw) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

// SnapshotFilter compares the entire original document atomically, including
// missing fields and fields added by another writer. _id keeps the match indexed.
// $literal prevents user data such as dollar-prefixed strings becoming expressions.
func SnapshotFilter(raw bson.Raw) bson.M {
	return bson.M{"_id": raw.Lookup("_id"), "$expr": bson.M{"$eq": bson.A{"$$ROOT", bson.M{"$literal": raw}}}}
}

// ConditionalFilter checks the version shown to the client, then retains the
// snapshot as the database write predicate. Checking before an unconditional
// write would leave a race between the read and the write.
func ConditionalFilter(ctx context.Context, coll *mongo.Collection, identity bson.M, version string) (bson.M, error) {
	if version == "" {
		return nil, ErrConflict
	}
	raw, err := coll.FindOne(ctx, identity).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if RecordVersion(raw) != version {
		return nil, ErrConflict
	}
	return SnapshotFilter(raw), nil
}

func firstVersion(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}
