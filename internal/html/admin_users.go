package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// Call registerAdminUserRoutes(mux) once after mux := http.NewServeMux().
// These endpoints independently authenticate every request.
func registerAdminUserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/users", adminUsersAuthorized(adminUsersSearch))
	mux.HandleFunc("/api/admin/users/delete", adminUsersAuthorized(adminUsersDelete))
	mux.HandleFunc("/api/admin/users/sessions/cancel", adminUsersAuthorized(adminUsersCancelSessions))
}

type adminUserRecord struct {
	ID       primitive.ObjectID `bson:"_id" json:"id"`
	Username string             `bson:"username" json:"username"`
	Name     string             `bson:"name" json:"name"`
	TenantID string             `bson:"tenantId" json:"tenantId"`
	Role     string             `bson:"role" json:"role"`
}

type adminUserSession struct {
	SessionID string    `bson:"sessionId" json:"sessionId"`
	ExpiresAt time.Time `bson:"expiresAt" json:"expiresAt"`
}

type adminUserView struct {
	adminUserRecord `bson:",inline"`
	Sessions        []adminUserSession `json:"sessions"`
	IsCurrentUser   bool               `json:"isCurrentUser"`
}

type adminUserAction struct {
	UserID               string `json:"userId"`
	SessionID            string `json:"sessionId"`
	AllSessions          bool   `json:"allSessions"`
	ConfirmationUsername string `json:"confirmationUsername"`
}

// Check the live session AND the current user record; a stale session role
// alone must not grant access after an administrator is demoted or deleted.
func adminUsersAuthorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		session, err := database.GetSession(r)
		if err != nil || session == nil {
			adminUsersError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var actor adminUserRecord
		err = database.Client.Database(database.GetMongoDbName()).Collection("users").FindOne(
			ctx, bson.M{"username": session.Username, "tenantId": session.TenantID},
			options.FindOne().SetProjection(adminUserProjection()),
		).Decode(&actor)
		if errors.Is(err, mongo.ErrNoDocuments) {
			adminUsersError(w, http.StatusUnauthorized, "Your account no longer exists.")
			return
		}
		if err != nil {
			log.Printf("admin users authorization lookup: %v", err)
			adminUsersError(w, http.StatusInternalServerError, "Unable to verify administrator access.")
			return
		}
		if session.Role != "admin" || actor.Role != "admin" {
			adminUsersError(w, http.StatusForbidden, "Administrator access required.")
			return
		}
		// Custom header plus same-origin JSON requests prevent cross-site form writes.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Ref-Ledger-Admin") != "1" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				adminUsersError(w, http.StatusForbidden, "Invalid administration request.")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, parseErr := url.Parse(origin)
				if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !strings.EqualFold(parsed.Host, r.Host) {
					adminUsersError(w, http.StatusForbidden, "Administration requests must use the same origin.")
					return
				}
			}
		}
		next(w, r)
	}
}

func adminUserProjection() bson.M {
	return bson.M{"_id": 1, "username": 1, "name": 1, "tenantId": 1, "role": 1}
}

func adminUsersJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("admin users response: %v", err)
	}
}

func adminUsersError(w http.ResponseWriter, status int, message string) {
	adminUsersJSON(w, status, map[string]string{"message": message})
}

func adminUsersMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	adminUsersError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	return false
}

func adminUsersDecode(w http.ResponseWriter, r *http.Request) (adminUserAction, bool) {
	var action adminUserAction
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		adminUsersError(w, http.StatusUnsupportedMediaType, "JSON is required.")
		return action, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		adminUsersError(w, http.StatusBadRequest, "Invalid administration request.")
		return action, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		adminUsersError(w, http.StatusBadRequest, "Only one JSON object is allowed.")
		return action, false
	}
	return action, true
}

func adminUsersSearch(w http.ResponseWriter, r *http.Request) {
	if !adminUsersMethod(w, r, http.MethodGet) {
		return
	}
	query := r.URL.Query()
	username, name := strings.TrimSpace(query.Get("username")), strings.TrimSpace(query.Get("name"))
	if len(username) > 254 || len(name) > 200 {
		adminUsersError(w, http.StatusBadRequest, "Search text is too long.")
		return
	}
	var offset int64
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 || parsed > 1000000 {
			adminUsersError(w, http.StatusBadRequest, "Invalid page offset.")
			return
		}
		offset = parsed
	}
	filter := bson.M{}
	if username != "" {
		filter["username"] = primitive.Regex{Pattern: regexp.QuoteMeta(username), Options: "i"}
	}
	if name != "" {
		filter["name"] = primitive.Regex{Pattern: regexp.QuoteMeta(name), Options: "i"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	db := database.Client.Database(database.GetMongoDbName())
	const pageSize int64 = 50
	cursor, err := db.Collection("users").Find(ctx, filter, options.Find().
		SetProjection(adminUserProjection()).SetSort(bson.D{{Key: "username", Value: 1}, {Key: "_id", Value: 1}}).
		SetSkip(offset).SetLimit(pageSize+1))
	if err != nil {
		log.Printf("admin users search: %v", err)
		adminUsersError(w, http.StatusInternalServerError, "Unable to load users.")
		return
	}
	defer cursor.Close(ctx)
	users := make([]adminUserRecord, 0)
	if err := cursor.All(ctx, &users); err != nil {
		adminUsersError(w, http.StatusInternalServerError, "Unable to read users.")
		return
	}
	hasMore := len(users) > int(pageSize)
	if hasMore {
		users = users[:pageSize]
	}
	// One session query per page, with explicit username + tenant ownership pairs.
	pairs := make(bson.A, 0, len(users))
	for _, user := range users {
		pairs = append(pairs, bson.M{"username": user.Username, "tenantId": user.TenantID})
	}
	type storedSession struct {
		adminUserSession `bson:",inline"`
		Username         string `bson:"username"`
		TenantID         string `bson:"tenantId"`
	}
	sessionMap := make(map[string][]adminUserSession)
	if len(pairs) > 0 {
		sessionCursor, err := db.Collection("sessions").Find(ctx, bson.M{"$or": pairs}, options.Find().
			SetProjection(bson.M{"_id": 0, "sessionId": 1, "expiresAt": 1, "username": 1, "tenantId": 1}).
			SetSort(bson.D{{Key: "expiresAt", Value: -1}, {Key: "sessionId", Value: 1}}))
		if err != nil {
			adminUsersError(w, http.StatusInternalServerError, "Unable to load sessions.")
			return
		}
		defer sessionCursor.Close(ctx)
		var stored []storedSession
		if err := sessionCursor.All(ctx, &stored); err != nil {
			adminUsersError(w, http.StatusInternalServerError, "Unable to read sessions.")
			return
		}
		for _, session := range stored {
			key := session.TenantID + "\x00" + session.Username
			sessionMap[key] = append(sessionMap[key], session.adminUserSession)
		}
	}
	actor, err := database.GetSession(r)
	if err != nil {
		adminUsersError(w, http.StatusUnauthorized, "Your session has expired.")
		return
	}
	views := make([]adminUserView, 0, len(users))
	for _, user := range users {
		sessions := sessionMap[user.TenantID+"\x00"+user.Username]
		if sessions == nil {
			sessions = make([]adminUserSession, 0)
		}
		views = append(views, adminUserView{adminUserRecord: user, Sessions: sessions,
			IsCurrentUser: user.Username == actor.Username && user.TenantID == actor.TenantID})
	}
	adminUsersJSON(w, http.StatusOK, map[string]interface{}{
		"users": views, "hasMore": hasMore, "offset": offset, "pageSize": pageSize,
	})
}

func adminUsersTarget(ctx context.Context, id string) (adminUserRecord, error) {
	var user adminUserRecord
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return user, mongo.ErrNoDocuments
	}
	err = database.Client.Database(database.GetMongoDbName()).Collection("users").FindOne(
		ctx, bson.M{"_id": objectID}, options.FindOne().SetProjection(adminUserProjection()),
	).Decode(&user)
	return user, err
}

func adminUsersCancelSessions(w http.ResponseWriter, r *http.Request) {
	if !adminUsersMethod(w, r, http.MethodPost) {
		return
	}
	action, ok := adminUsersDecode(w, r)
	if !ok {
		return
	}
	// Require an explicit all-sessions flag OR one specific session ID.
	if action.AllSessions == (action.SessionID != "") {
		adminUsersError(w, http.StatusBadRequest, "Choose one session or explicitly choose all sessions.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	user, err := adminUsersTarget(ctx, action.UserID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		adminUsersError(w, http.StatusNotFound, "User not found. Refresh the table.")
		return
	}
	if err != nil {
		adminUsersError(w, http.StatusInternalServerError, "Unable to load the selected user.")
		return
	}
	filter := bson.M{"username": user.Username, "tenantId": user.TenantID}
	if !action.AllSessions {
		filter["sessionId"] = action.SessionID
	}
	result, err := database.Client.Database(database.GetMongoDbName()).Collection("sessions").DeleteMany(ctx, filter)
	if err != nil {
		log.Printf("admin cancel sessions: %v", err)
		adminUsersError(w, http.StatusInternalServerError, "Unable to cancel sessions.")
		return
	}
	// The current request was already authorized. Future requests must sign in.
	cookie, _ := r.Cookie("rl_session")
	currentCancelled := false
	if cookie != nil {
		err := database.Client.Database(database.GetMongoDbName()).Collection("sessions").FindOne(
			ctx, bson.M{"sessionId": cookie.Value}, options.FindOne().SetProjection(bson.M{"_id": 1}),
		).Err()
		currentCancelled = errors.Is(err, mongo.ErrNoDocuments)
	}
	adminUsersJSON(w, http.StatusOK, map[string]interface{}{
		"message":        fmt.Sprintf("Cancelled %d session(s). User data was preserved.", result.DeletedCount),
		"cancelledCount": result.DeletedCount, "currentSessionCancelled": currentCancelled,
	})
}

var errAdminSharedTenant = errors.New("multiple users share this tenant")
var errAdminDeleteSelf = errors.New("cannot delete your signed-in account")
var errAdminDeleteConfirmation = errors.New("email confirmation does not match")
var errAdminInvalidTenant = errors.New("missing or invalid tenant ID")

func adminUsersDelete(w http.ResponseWriter, r *http.Request) {
	if !adminUsersMethod(w, r, http.MethodPost) {
		return
	}
	action, ok := adminUsersDecode(w, r)
	if !ok {
		return
	}
	actor, err := database.GetSession(r)
	if err != nil {
		adminUsersError(w, http.StatusUnauthorized, "Your session has expired.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	db := database.Client.Database(database.GetMongoDbName())
	// Include every ordinary collection, including future tenant-owned collections.
	// Shared records without this tenantId (for example global roles/sports) remain.
	collections, err := db.ListCollectionNames(ctx, bson.M{"type": "collection"})
	if err != nil {
		adminUsersError(w, http.StatusInternalServerError, "Unable to enumerate account data. Nothing was deleted.")
		return
	}
	transaction, err := database.Client.StartSession()
	if err != nil {
		adminUsersError(w, http.StatusInternalServerError, "Unable to begin account deletion. Nothing was deleted.")
		return
	}
	defer transaction.EndSession(context.Background())
	txnOptions := options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority())
	var deleted map[string]int64
	_, err = transaction.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		// Re-read within the transaction so ownership and target are checked together.
		user, err := adminUsersTarget(sc, action.UserID)
		if err != nil {
			return nil, err
		}
		if user.Username == actor.Username || user.TenantID == actor.TenantID {
			return nil, errAdminDeleteSelf
		}
		if user.TenantID == "" || strings.TrimSpace(user.TenantID) != user.TenantID || user.TenantID == "na" {
			return nil, errAdminInvalidTenant
		}
		if action.ConfirmationUsername != user.Username {
			return nil, errAdminDeleteConfirmation
		}
		count, err := db.Collection("users").CountDocuments(sc, bson.M{"tenantId": user.TenantID})
		if err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, errAdminSharedTenant
		}
		deleted = make(map[string]int64)
		for _, collection := range collections {
			if collection == "users" || strings.HasPrefix(collection, "system.") {
				continue
			}
			filter := bson.M{"tenantId": user.TenantID}
			if collection == "sessions" {
				// Also remove older sessions missing tenantId for this unique username.
				filter = bson.M{"$or": bson.A{filter, bson.M{"username": user.Username}}}
			}
			result, err := db.Collection(collection).DeleteMany(sc, filter)
			if err != nil {
				return nil, fmt.Errorf("delete tenant records in %s: %w", collection, err)
			}
			deleted[collection] = result.DeletedCount
		}
		result, err := db.Collection("users").DeleteOne(sc, bson.M{"_id": user.ID, "tenantId": user.TenantID})
		if err != nil {
			return nil, err
		}
		if result.DeletedCount != 1 {
			return nil, mongo.ErrNoDocuments
		}
		deleted["users"] = result.DeletedCount
		return nil, nil
	}, txnOptions)
	if err != nil {
		switch {
		case errors.Is(err, errAdminDeleteSelf):
			adminUsersError(w, http.StatusConflict, "You cannot delete your signed-in account or its tenant.")
		case errors.Is(err, errAdminSharedTenant):
			adminUsersError(w, http.StatusConflict, "Deletion stopped: multiple users share this tenant. No data was deleted.")
		case errors.Is(err, errAdminDeleteConfirmation):
			adminUsersError(w, http.StatusBadRequest, "Enter the selected user's exact email address to confirm.")
		case errors.Is(err, errAdminInvalidTenant):
			adminUsersError(w, http.StatusConflict, "Deletion stopped: this account has an invalid tenant ID.")
		case errors.Is(err, mongo.ErrNoDocuments):
			adminUsersError(w, http.StatusNotFound, "User not found. Refresh the table.")
		default:
			log.Printf("admin account deletion failed: %v", err)
			adminUsersError(w, http.StatusInternalServerError,
				"Account deletion could not be confirmed. Refresh before retrying. MongoDB must support transactions (replica set or Atlas).")
		}
		return
	}
	adminUsersJSON(w, http.StatusOK, map[string]interface{}{
		"message": "The user, all tenant records, and sessions were deleted.", "deletedCounts": deleted,
	})
}
