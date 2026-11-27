package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoAnnouncementStore struct{}

func announcementsCollection() *mongo.Collection {
	return database.Client.Database(database.GetMongoDbName()).Collection("announcements")
}

func (mongoAnnouncementStore) list(ctx context.Context) ([]announcement, error) {
	cur, err := announcementsCollection().Find(ctx, bson.M{"cleared": false})
	if err != nil {
		return nil, err
	}
	var out []announcement
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (mongoAnnouncementStore) get(ctx context.Context, id string) (announcement, error) {
	var a announcement
	err := announcementsCollection().FindOne(ctx, bson.M{"_id": id}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return a, errAnnouncementNotFound
	}
	return a, err
}

func (mongoAnnouncementStore) save(ctx context.Context, a announcement) error {
	if a.Version <= 1 {
		_, err := announcementsCollection().InsertOne(ctx, a)
		return err
	}
	res, err := announcementsCollection().ReplaceOne(ctx, bson.M{"_id": a.ID, "version": a.Version - 1}, a)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errAnnouncementConflict
	}
	return nil
}

type mongoAnnouncementDirectory struct{}

func (mongoAnnouncementDirectory) userID(ctx context.Context, tenantID, username string) (string, error) {
	var doc struct {
		ID       primitive.ObjectID `bson:"_id"`
		TenantID string             `bson:"tenantId"`
	}
	err := auth0Users().FindOne(ctx, bson.M{"username": username}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) || (err == nil && tenantID != "" && doc.TenantID != tenantID) {
		return "", errAnnouncementUserUnknown
	}
	return doc.ID.Hex(), err
}

func (d mongoAnnouncementDirectory) resolveUser(ctx context.Context, username string) (announcementUser, error) {
	username = strings.TrimSpace(username)
	for _, candidate := range []string{username, strings.ToLower(username)} {
		var doc struct {
			ID       primitive.ObjectID `bson:"_id"`
			TenantID string             `bson:"tenantId"`
			Username string             `bson:"username"`
		}
		err := auth0Users().FindOne(ctx, bson.M{"username": candidate}).Decode(&doc)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue
		}
		if err != nil {
			return announcementUser{}, err
		}
		return announcementUser{TenantID: doc.TenantID, UserID: doc.ID.Hex(), Username: doc.Username}, nil
	}
	return announcementUser{}, errAnnouncementUserUnknown
}

// Web sessions have no "type"; Android sessions are "mobile". The targeting ID is
// random and separate from the web cookie value and mobile session secret.
func announcementSessionFilter() bson.M {
	return bson.M{"$or": bson.A{bson.M{"type": bson.M{"$exists": false}}, bson.M{"type": mobileSessionType}}}
}

func ensureSessionTargetID(ctx context.Context, filter bson.M) (string, error) {
	var doc struct {
		TargetID string `bson:"targetId"`
	}
	sessions := mobileSessions()
	if err := sessions.FindOne(ctx, filter).Decode(&doc); err != nil {
		return "", err
	}
	if doc.TargetID != "" {
		return doc.TargetID, nil
	}
	missing := bson.M{"$and": bson.A{filter, bson.M{"targetId": bson.M{"$exists": false}}}}
	if _, err := sessions.UpdateOne(ctx, missing, bson.M{"$set": bson.M{"targetId": uuid.NewString()}}); err != nil {
		return "", err
	}
	if err := sessions.FindOne(ctx, filter).Decode(&doc); err != nil {
		return "", err
	}
	return doc.TargetID, nil
}

type announcementSessionDoc struct {
	SessionID string    `bson:"sessionId"`
	TargetID  string    `bson:"targetId"`
	Type      string    `bson:"type"`
	Platform  string    `bson:"platform"`
	Client    string    `bson:"client"`
	Username  string    `bson:"username"`
	TenantID  string    `bson:"tenantId"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

func (d mongoAnnouncementDirectory) toSession(ctx context.Context, doc announcementSessionDoc) (announcementSession, error) {
	userID, err := d.userID(ctx, doc.TenantID, doc.Username)
	if err != nil {
		return announcementSession{}, errAnnouncementSessionBad
	}
	platform := doc.Platform
	if platform == "" {
		platform = "web"
	}
	return announcementSession{TargetID: doc.TargetID, TenantID: doc.TenantID, UserID: userID, Username: doc.Username, Platform: platform, CreatedAt: doc.CreatedAt, ExpiresAt: doc.ExpiresAt}, nil
}

func (d mongoAnnouncementDirectory) sessionByTarget(ctx context.Context, targetID string) (announcementSession, error) {
	var doc announcementSessionDoc
	err := mobileSessions().FindOne(ctx, bson.M{"$and": bson.A{announcementSessionFilter(),
		bson.M{"targetId": targetID, "expiresAt": bson.M{"$gt": time.Now()}}}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return announcementSession{}, errAnnouncementSessionBad
	}
	if err != nil {
		return announcementSession{}, err
	}
	return d.toSession(ctx, doc)
}

func (d mongoAnnouncementDirectory) sessionsForUser(ctx context.Context, tenantID, username string) ([]announcementSession, error) {
	filter := bson.M{"$and": bson.A{announcementSessionFilter(),
		bson.M{"tenantId": tenantID, "username": username, "expiresAt": bson.M{"$gt": time.Now()}}}}
	cur, err := mobileSessions().Find(ctx, filter, options.Find().SetSort(bson.M{"createdAt": -1}))
	if err != nil {
		return nil, err
	}
	var docs []announcementSessionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := []announcementSession{}
	for _, doc := range docs {
		if doc.TargetID == "" { // sessions created before targeting IDs existed
			if doc.TargetID, err = ensureSessionTargetID(ctx, bson.M{"sessionId": doc.SessionID, "tenantId": tenantID}); err != nil {
				continue
			}
		}
		if s, err := d.toSession(ctx, doc); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

var (
	announcementsOnce sync.Once
	announcementsSvc  *announcementService
)

func announcements() *announcementService {
	announcementsOnce.Do(func() {
		announcementsSvc = &announcementService{store: mongoAnnouncementStore{}, dir: mongoAnnouncementDirectory{}}
	})
	return announcementsSvc
}

func announcementAdminAuthorized(r *http.Request) bool {
	_, ok := logsAdminAuthorization(r)
	return ok && logsAdminTransportAllowed(r) && database.Client != nil
}

func announcementAdminHandler(w http.ResponseWriter, r *http.Request) {
	announcements().adminHandler(announcementAdminAuthorized, func(r *http.Request) string {
		return safeLogsActor(r.Header.Get("X-Ref-Ledger-Admin-Actor"))
	})(w, r)
}

// webAnnouncementRecipient derives identity from the rl_session cookie.
func webAnnouncementRecipient(r *http.Request) resolveRecipient {
	return func(ctx context.Context) (announcementRecipient, error) {
		cookie, err := r.Cookie("rl_session")
		if err != nil || cookie.Value == "" {
			return announcementRecipient{}, errAnnouncementSessionEnd
		}
		filter := database.WebSessionFilter(cookie.Value)
		filter["expiresAt"] = bson.M{"$gt": time.Now()}
		var session model.Session
		if err := mobileSessions().FindOne(ctx, filter).Decode(&session); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return announcementRecipient{}, errAnnouncementSessionEnd
			}
			return announcementRecipient{}, err
		}
		target, err := ensureSessionTargetID(ctx, filter)
		if err != nil {
			return announcementRecipient{}, err
		}
		userID, err := (mongoAnnouncementDirectory{}).userID(ctx, session.TenantID, session.Username)
		if err != nil {
			return announcementRecipient{}, errAnnouncementSessionEnd
		}
		return announcementRecipient{TenantID: session.TenantID, UserID: userID, Username: session.Username, TargetID: target}, nil
	}
}

// mobileAnnouncementRecipient derives identity from the Auth0 bearer token and
// the managed mobile session, exactly as other mobile endpoints do.
func mobileAnnouncementRecipient(r *http.Request) resolveRecipient {
	return func(ctx context.Context) (announcementRecipient, error) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return announcementRecipient{}, errAnnouncementReauth
		}
		validator, err := configuredMobileValidator()
		if err != nil {
			return announcementRecipient{}, err
		}
		identity, err := validator.Validate(ctx, parts[1])
		if err != nil {
			return announcementRecipient{}, errAnnouncementReauth
		}
		user, err := findMobileAccount(ctx, identity)
		if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errAuth0AccountDenied) {
			return announcementRecipient{}, errAnnouncementSessionEnd
		}
		if err != nil {
			return announcementRecipient{}, err
		}
		if err := requireMobileSession(ctx, r, identity, user); err != nil {
			if errors.Is(err, errMobileSessionRequired) {
				return announcementRecipient{}, errAnnouncementSessionEnd
			}
			return announcementRecipient{}, err
		}
		userID, err := (mongoAnnouncementDirectory{}).userID(ctx, user.TenantID, user.Username)
		if err != nil {
			return announcementRecipient{}, errAnnouncementSessionEnd
		}
		rec := announcementRecipient{TenantID: user.TenantID, UserID: userID, Username: user.Username}
		if token := strings.TrimSpace(r.Header.Get(mobileSessionHeader)); token != "" {
			rec.TargetID, err = ensureSessionTargetID(ctx, bson.M{
				"type": mobileSessionType, "mobileTokenHash": hashMobileSessionToken(token),
				"issuer": identity.Issuer, "subject": identity.Subject, "tenantId": user.TenantID,
			})
			if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
				return announcementRecipient{}, err
			}
		}
		return rec, nil
	}
}

func webAnnouncementCurrent(w http.ResponseWriter, r *http.Request) {
	announcements().currentHandler(webAnnouncementRecipient(r))(w, r)
}

func webAnnouncementEvents(w http.ResponseWriter, r *http.Request) {
	announcements().eventsHandler(webAnnouncementRecipient(r))(w, r)
}

func mobileAnnouncementCurrent(w http.ResponseWriter, r *http.Request) {
	announcements().currentHandler(mobileAnnouncementRecipient(r))(w, r)
}
