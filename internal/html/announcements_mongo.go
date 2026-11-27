package main

import (
	"context"
	"errors"
	"log"
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
	var docs []struct {
		ID       primitive.ObjectID `bson:"_id"`
		TenantID string             `bson:"tenantId"`
	}
	if tenantID == "" {
		return "", errAnnouncementUserUnknown
	}
	cur, err := auth0Users().Find(ctx, bson.M{"username": username, "tenantId": tenantID}, options.Find().SetProjection(bson.M{"_id": 1, "tenantId": 1}).SetLimit(2))
	if err != nil {
		return "", err
	}
	if err := cur.All(ctx, &docs); err != nil {
		return "", err
	}
	if len(docs) != 1 {
		return "", errAnnouncementUserUnknown
	}
	return docs[0].ID.Hex(), nil
}

func (mongoAnnouncementDirectory) userByID(ctx context.Context, tenantID, userID string) (announcementUser, error) {
	id, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return announcementUser{}, errAnnouncementUserUnknown
	}
	var doc struct {
		ID       primitive.ObjectID `bson:"_id"`
		TenantID string             `bson:"tenantId"`
		Username string             `bson:"username"`
	}
	err = auth0Users().FindOne(ctx, bson.M{"_id": id, "tenantId": tenantID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return announcementUser{}, errAnnouncementUserUnknown
	}
	if err != nil {
		return announcementUser{}, err
	}
	return announcementUser{TenantID: doc.TenantID, UserID: doc.ID.Hex(), Username: doc.Username}, nil
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
	return bson.M{
		"_id.type": bson.M{"$ne": "mobileActivity"},
		"$or": bson.A{
			bson.M{"type": bson.M{"$exists": false}},
			bson.M{"type": mobileSessionType, "createdAt": bson.M{"$gt": time.Now().Add(-sessionDuration)}},
		},
	}
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
		if errors.Is(err, errAnnouncementUserUnknown) {
			return announcementSession{}, errAnnouncementSessionBad
		}
		return announcementSession{}, err
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
				return nil, err
			}
		}
		if s, err := d.toSession(ctx, doc); err == nil {
			out = append(out, s)
		} else if !errors.Is(err, errAnnouncementSessionBad) {
			return nil, err
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

func announcementAdminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	requestID := uuid.NewString()
	w.Header().Set("X-Request-ID", requestID)
	authorizationStatus, authorized := logsAdminAuthorization(r)
	transportAllowed := logsAdminTransportAllowed(r)
	if !authorized || !transportAllowed {
		logAnnouncementAdminAuthenticationFailure(r, requestID, authorizationStatus, transportAllowed)
		http.Error(w, "Administrator service authentication failed.", http.StatusUnauthorized)
		return
	}
	if database.Client == nil {
		log.Printf("announcement administrator unavailable: request_id=%s database=not_initialized", requestID)
		http.Error(w, "Announcement service is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	response := &httpByteResponseWriter{ResponseWriter: w, method: r.Method}
	announcements().adminHandler(func(*http.Request) bool { return true }, func(r *http.Request) string {
		return safeLogsActor(r.Header.Get("X-Ref-Ledger-Admin-Actor"))
	})(response, r)
	if response.status == http.StatusInternalServerError {
		log.Printf("announcement administrator operation failed: request_id=%s database=operation_failed", requestID)
	}
}

func logAnnouncementAdminAuthenticationFailure(r *http.Request, requestID, authorizationStatus string, transportAllowed bool) {
	transport := logsAdminCheckRejected
	if transportAllowed {
		transport = logsAdminCheckAccepted
	}
	directTLS := "no"
	if r.TLS != nil {
		directTLS = "yes"
	}
	if authorizationStatus == logsAdminCheckAccepted && !transportAllowed {
		values := r.Header.Values("X-Forwarded-Proto")
		formatted, truncated := safeAnnouncementForwardedProto(values)
		log.Printf("announcement administrator authentication failed: request_id=%s authorization=%s transport=%s direct_tls=%s forwarded_proto_count=%d forwarded_proto_values=%s forwarded_proto_truncated=%t",
			requestID, authorizationStatus, transport, directTLS, len(values), formatted, truncated)
		return
	}
	log.Printf("announcement administrator authentication failed: request_id=%s authorization=%s transport=%s direct_tls=%s",
		requestID, authorizationStatus, transport, directTLS)
}

// Only known protocol names and separators are safe to echo from this untrusted header.
func safeAnnouncementForwardedProto(values []string) (string, bool) {
	const maxValues = 64
	truncated := len(values) > maxValues
	if truncated {
		values = values[:maxValues]
	}
	safe := make([]string, 0, len(values))
	for _, value := range values {
		valid := len(value) <= 64
		var normalized []string
		if valid {
			for i := 0; i < len(value); i++ {
				if !strings.ContainsRune("hHtTpPsS, \t", rune(value[i])) {
					valid = false
					break
				}
			}
		}
		if valid {
			for _, part := range strings.Split(value, ",") {
				part = strings.Trim(part, " \t")
				switch {
				case strings.EqualFold(part, "http"):
					normalized = append(normalized, "http")
				case strings.EqualFold(part, "https"):
					normalized = append(normalized, "https")
				default:
					valid = false
				}
			}
		}
		if valid {
			safe = append(safe, strings.Join(normalized, ","))
		} else {
			safe = append(safe, "[redacted]")
		}
	}
	formatted, formatTruncated := formatLogsAdminForwardedProto(safe)
	return formatted, truncated || formatTruncated
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
