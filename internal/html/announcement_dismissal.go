package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"ref-ledger-v2/internal/database"
)

func (a announcement) timed() bool {
	if a.Scheduled != nil {
		return *a.Scheduled
	}
	// Older immediate announcements stored the server-generated start and update times separately.
	return a.EndsAt != nil || a.StartsAt.After(a.UpdatedAt.Add(time.Second))
}

func dismissalKey(a announcement, recipient announcementRecipient) string {
	if a.timed() {
		return a.ID + ":" + recipient.TargetID
	}
	return a.ID
}

type announcementDismissalStore interface {
	dismissed(context.Context, announcementRecipient, []string) (map[string]bool, error)
	dismiss(context.Context, announcementRecipient, string) error
}

type mongoAnnouncementDismissals struct{}

func announcementDismissalsCollection() *mongo.Collection {
	return database.Client.Database(database.GetMongoDbName()).Collection("announcementDismissals")
}

func (mongoAnnouncementDismissals) dismissed(ctx context.Context, recipient announcementRecipient, keys []string) (map[string]bool, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	ids := make([]bson.D, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, dismissalID(recipient, key))
	}
	cursor, err := announcementDismissalsCollection().Find(ctx, bson.M{
		"_id": bson.M{"$in": ids},
	})
	if err != nil {
		return nil, err
	}
	var records []struct {
		ID struct {
			Key string `bson:"key"`
		} `bson:"_id"`
	}
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}
	dismissed := make(map[string]bool, len(records))
	for _, record := range records {
		dismissed[record.ID.Key] = true
	}
	return dismissed, nil
}

func dismissalID(recipient announcementRecipient, key string) bson.D {
	return bson.D{{Key: "tenantId", Value: recipient.TenantID}, {Key: "userId", Value: recipient.UserID}, {Key: "key", Value: key}}
}

func (mongoAnnouncementDismissals) dismiss(ctx context.Context, recipient announcementRecipient, key string) error {
	id := dismissalID(recipient, key)
	_, err := announcementDismissalsCollection().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$setOnInsert": bson.M{"_id": id}}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}

func (s *announcementService) dismissHandler(resolve resolveRecipient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if media := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]); media != "application/json" {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		recipient, err := resolve(r.Context())
		if err != nil {
			writeAnnouncementAuthError(w, err)
			return
		}
		var request struct {
			ID string `json:"id"`
		}
		if !decodeAnnouncementBody(w, r, &request) {
			return
		}
		a, err := s.store.get(r.Context(), request.ID)
		if err != nil {
			writeAnnouncementResult(w, 0, nil, err)
			return
		}
		if !announcementEligible(a, recipient, s.clock()) {
			writeAnnouncementResult(w, 0, nil, errAnnouncementNotFound)
			return
		}
		if a.timed() && recipient.TargetID == "" {
			http.Error(w, "A managed sign-in session is required", http.StatusConflict)
			return
		}
		if err := s.dismissals.dismiss(r.Context(), recipient, dismissalKey(a, recipient)); err != nil {
			writeAnnouncementResult(w, 0, nil, err)
			return
		}
		s.hub.notify()
		w.WriteHeader(http.StatusNoContent)
	}
}

func webAnnouncementDismiss(w http.ResponseWriter, r *http.Request) {
	announcements().dismissHandler(webAnnouncementRecipient(r))(w, r)
}

func mobileAnnouncementDismiss(w http.ResponseWriter, r *http.Request) {
	announcements().dismissHandler(mobileAnnouncementRecipient(r))(w, r)
}
