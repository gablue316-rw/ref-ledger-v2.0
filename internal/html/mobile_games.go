package main

import (
	"context"
	"encoding/json"
	"errors"
	"go.mongodb.org/mongo-driver/mongo"
	"io"
	"net/http"
	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type mobileWriteContextKey struct{}

// Mobile routes use live linked-account roles; cookies and supplied tenant IDs
// cannot authorize access. Existing native tokens continue to work.
func mobileAccountHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "Method not allowed", 405)
			return
		}
		// The shared maintenance middleware admits and releases all data writes.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		identity, _, ok := authenticateMobile(w, r.WithContext(ctx))
		if !ok {
			return
		}
		if database.Client == nil {
			http.Error(w, "Service unavailable", 503)
			return
		}
		user, err := findAuth0Account(ctx, identity)
		if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errAuth0AccountDenied) {
			http.Error(w, "Account access denied", 403)
			return
		}
		if err != nil {
			http.Error(w, "Service unavailable", 503)
			return
		}
		if r.Method != http.MethodGet && !mobileCanWrite(user.Role) {
			http.Error(w, "This account has read-only access", 403)
			return
		}
		ctx = context.WithValue(ctx, mobileWriteContextKey{}, mobileCanWrite(user.Role))
		next(w, r.WithContext(context.WithValue(ctx, mobileAccountContextKey{}, user.TenantID)))
	}
}
func mobileCanWrite(role string) bool { return role == "user" || role == "admin" }
func validateMobileGame(game model.GameDescriptor, old model.GameDescriptor) error {
	if game.GameId != old.GameId || game.Association != old.Association {
		return errors.New("game ID and association cannot be changed")
	}
	if old.Status == "Paid" {
		return database.ErrMobilePaid
	}
	switch game.Status {
	case "Pending", "Completed", "Cancelled":
	default:
		return errors.New("choose Pending, Completed, or Cancelled; record payments on the website")
	}
	if utf8.RuneCountInString(game.Notes) > MaxNotesLength {
		return errors.New("notes exceed maximum length")
	}
	if _, err := time.Parse("3:04 PM", game.Time); err != nil {
		return errors.New("invalid game time")
	}
	if n, err := strconv.Atoi(game.NumOfGames); err != nil || n < 1 {
		return errors.New("number of games must be positive")
	}
	return nil
}
func MobileGame(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("gameId"), 10, 64)
	association := strings.TrimSpace(r.URL.Query().Get("association"))
	if err != nil || id <= 0 || association == "" {
		http.Error(w, "Game ID and association are required", 400)
		return
	}
	tenant, _ := getTenantId(r)
	snapshot, err := database.LoadMobileGame(r.Context(), tenant, association, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		http.Error(w, "Game not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "Unable to load game", 500)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"game": snapshot.Game, "version": snapshot.Version, "notesLimit": MaxNotesLength, "canWrite": r.Context().Value(mobileWriteContextKey{}) == true})
		return
	}
	var request struct {
		Game    model.GameDescriptor `json:"game"`
		Version string               `json:"version"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		http.Error(w, "Invalid game JSON", 400)
		return
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Provide one JSON object", 400)
		return
	}
	var game *model.GameDescriptor
	if r.Method == http.MethodPut {
		if err = validateMobileGame(request.Game, snapshot.Game); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Game.Status == "Cancelled" {
			request.Game.GameFee = "0.00"
			request.Game.TravelPay = "0.00"
			request.Game.AssignorFee = "0.00"
			request.Game.Deductions = "0.00"
		}
		if err = api.ValidateGameDescriptor(r.Context(), request.Game, false, tenant); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		game = &request.Game
	}
	err = database.WriteMobileGame(r.Context(), snapshot, game, request.Version)
	if errors.Is(err, database.ErrMobileConflict) || errors.Is(err, database.ErrMobilePaid) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		http.Error(w, "Unable to save game: "+err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func mobileReadOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method not allowed", 405)
			return
		}
		next(w, r)
	}
}
