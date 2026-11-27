package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Announcements are informational banners authored in Ref Ledger Monitor. Ref
// Ledger owns the authoritative state; eligibility and expiry always use server
// time and an identity derived from authentication, never from client input.

const (
	announcementAudienceSystem  = "system"
	announcementAudienceUser    = "user"
	announcementAudienceSession = "session"

	announcementMaxMessageRunes = 4000
	announcementAdminPrefix     = "/api/internal/v1/admin/announcements"
)

var (
	errAnnouncementNotFound    = errors.New("announcement not found")
	errAnnouncementConflict    = errors.New("announcement was changed by another request")
	errAnnouncementUserUnknown = errors.New("user not found")
	errAnnouncementSessionBad  = errors.New("session is invalid, expired, revoked, or does not match")
	errAnnouncementSessionEnd  = errors.New("session ended")
	errAnnouncementReauth      = errors.New("re-authentication required")

	// announcementPollInterval is how often open connections re-evaluate state, so
	// scheduled starts, expirations, and changes made by other pods are delivered.
	announcementPollInterval = 5 * time.Second
	announcementMaxWait      = 25 * time.Second
)

type announcement struct {
	ID              string     `bson:"_id" json:"id"`
	Message         string     `bson:"message" json:"message"`
	Audience        string     `bson:"audience" json:"audience"`
	TenantID        string     `bson:"tenantId,omitempty" json:"tenantId,omitempty"`
	UserID          string     `bson:"userId,omitempty" json:"userId,omitempty"`
	Username        string     `bson:"username,omitempty" json:"username,omitempty"`
	SessionTargetID string     `bson:"sessionTargetId,omitempty" json:"sessionTargetId,omitempty"`
	StartsAt        time.Time  `bson:"startsAt" json:"startsAt"`
	EndsAt          *time.Time `bson:"endsAt,omitempty" json:"endsAt,omitempty"`
	Timezone        string     `bson:"timezone,omitempty" json:"timezone,omitempty"`
	Cleared         bool       `bson:"cleared" json:"cleared"`
	ClearedAt       *time.Time `bson:"clearedAt,omitempty" json:"clearedAt,omitempty"`
	CreatedBy       string     `bson:"createdBy,omitempty" json:"createdBy,omitempty"`
	CreatedAt       time.Time  `bson:"createdAt" json:"createdAt"`
	UpdatedAt       time.Time  `bson:"updatedAt" json:"updatedAt"`
	Version         int64      `bson:"version" json:"version"`
}

// announcementView is all a customer client ever receives.
type announcementView struct {
	ID       string     `json:"id"`
	Message  string     `json:"message"`
	Version  int64      `json:"version"`
	StartsAt time.Time  `json:"startsAt"`
	EndsAt   *time.Time `json:"endsAt,omitempty"`
}

type announcementRecipient struct {
	TenantID string
	UserID   string
	Username string
	TargetID string // session targeting ID; empty when the client has no managed session
}

type announcementUser struct{ TenantID, UserID, Username string }

type announcementSession struct {
	TargetID  string    `json:"sessionTargetId"`
	TenantID  string    `json:"tenantId"`
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	Platform  string    `json:"platform"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type announcementStore interface {
	list(ctx context.Context) ([]announcement, error) // not cleared
	get(ctx context.Context, id string) (announcement, error)
	// save inserts when a.Version == 1 and otherwise replaces version a.Version-1.
	save(ctx context.Context, a announcement) error
}

type announcementDirectory interface {
	resolveUser(ctx context.Context, username string) (announcementUser, error)
	userByID(ctx context.Context, tenantID, userID string) (announcementUser, error)
	sessionByTarget(ctx context.Context, targetID string) (announcementSession, error)
	sessionsForUser(ctx context.Context, tenantID, username string) ([]announcementSession, error)
}

type announcementHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (h *announcementHub) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan struct{}]struct{}{}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() { h.mu.Lock(); delete(h.subs, ch); h.mu.Unlock() }
}

func (h *announcementHub) notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type announcementService struct {
	store announcementStore
	dir   announcementDirectory
	hub   announcementHub
	now   func() time.Time
}

func (s *announcementService) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func announcementActive(a announcement, now time.Time) bool {
	if a.Cleared || now.Before(a.StartsAt) {
		return false
	}
	return a.EndsAt == nil || now.Before(*a.EndsAt)
}

// announcementEligible enforces the audience on the server for reads and live delivery.
func announcementEligible(a announcement, r announcementRecipient, now time.Time) bool {
	if !announcementActive(a, now) {
		return false
	}
	switch a.Audience {
	case announcementAudienceSystem:
		return true
	case announcementAudienceUser:
		return r.TenantID != "" && r.UserID != "" && a.TenantID == r.TenantID && a.UserID == r.UserID
	case announcementAudienceSession:
		return r.TargetID != "" && a.SessionTargetID == r.TargetID && a.TenantID == r.TenantID && a.UserID == r.UserID
	}
	return false
}

func announcementRank(a announcement) int {
	switch a.Audience {
	case announcementAudienceSession:
		return 3
	case announcementAudienceUser:
		return 2
	}
	return 1
}

// current returns the most specific, most recently updated eligible announcement.
func (s *announcementService) current(ctx context.Context, r announcementRecipient) (*announcementView, error) {
	all, err := s.store.list(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	var best *announcement
	for i := range all {
		a := all[i]
		if !announcementEligible(a, r, now) {
			continue
		}
		if best == nil || announcementRank(a) > announcementRank(*best) ||
			(announcementRank(a) == announcementRank(*best) && a.UpdatedAt.After(best.UpdatedAt)) {
			best = &a
		}
	}
	if best == nil {
		return nil, nil
	}
	return &announcementView{ID: best.ID, Message: best.Message, Version: best.Version, StartsAt: best.StartsAt, EndsAt: best.EndsAt}, nil
}

func announcementKey(v *announcementView) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", v.ID, v.Version)
}

type announcementRequest struct {
	Message         string `json:"message"`
	Audience        string `json:"audience"`
	Username        string `json:"username"`
	TenantID        string `json:"tenantId"`
	UserID          string `json:"userId"`
	SessionTargetID string `json:"sessionTargetId"`
	StartsAt        string `json:"startsAt"`
	EndsAt          string `json:"endsAt"`
	Timezone        string `json:"timezone"`
	Version         int64  `json:"version"`
}

type announcementInputError struct{ msg string }

func (e announcementInputError) Error() string { return e.msg }

func badAnnouncement(format string, args ...any) error {
	return announcementInputError{fmt.Sprintf(format, args...)}
}

func validAnnouncementMessage(message string) (string, error) {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if message == "" {
		return "", badAnnouncement("message is required")
	}
	if !utf8.ValidString(message) || utf8.RuneCountInString(message) > announcementMaxMessageRunes {
		return "", badAnnouncement("message must be valid text of at most %d characters", announcementMaxMessageRunes)
	}
	for _, c := range message {
		if c != '\n' && c != '\t' && unicode.IsControl(c) {
			return "", badAnnouncement("message must be plain text")
		}
	}
	return message, nil
}

// parseAnnouncementTime accepts RFC 3339, "YYYY-MM-DDTHH:MM[:SS]" in the named
// zone, or a date-only value. A date-only end is inclusive: it expires at the
// start of the following day in the named zone.
func parseAnnouncementTime(value string, loc *time.Location, isEnd bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t.UTC(), nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", value, loc); err == nil {
		if isEnd {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		}
		return t.UTC(), nil
	}
	return time.Time{}, badAnnouncement("invalid date/time %q; use RFC 3339, YYYY-MM-DDTHH:MM, or YYYY-MM-DD", value)
}

func (s *announcementService) build(ctx context.Context, req announcementRequest, base announcement) (announcement, error) {
	a := base
	var err error
	if a.Message, err = validAnnouncementMessage(req.Message); err != nil {
		return a, err
	}
	now := s.clock()
	loc := time.UTC
	tz := strings.TrimSpace(req.Timezone)
	if strings.TrimSpace(req.StartsAt) != "" || strings.TrimSpace(req.EndsAt) != "" {
		if tz == "" {
			return a, badAnnouncement("timezone is required when startsAt or endsAt is provided")
		}
		if loc, err = time.LoadLocation(tz); err != nil {
			return a, badAnnouncement("unknown timezone %q", tz)
		}
	}
	a.Timezone = tz
	a.StartsAt = now
	if strings.TrimSpace(req.StartsAt) != "" {
		if a.StartsAt, err = parseAnnouncementTime(req.StartsAt, loc, false); err != nil {
			return a, err
		}
	}
	a.EndsAt = nil
	if strings.TrimSpace(req.EndsAt) != "" {
		end, err := parseAnnouncementTime(req.EndsAt, loc, true)
		if err != nil {
			return a, err
		}
		if !end.After(a.StartsAt) {
			return a, badAnnouncement("endsAt must be after startsAt")
		}
		if !end.After(now) {
			return a, badAnnouncement("endsAt has already passed")
		}
		a.EndsAt = &end
	}

	a.Audience = strings.ToLower(strings.TrimSpace(req.Audience))
	a.TenantID, a.UserID, a.Username, a.SessionTargetID = "", "", "", ""
	switch a.Audience {
	case announcementAudienceSystem:
	case announcementAudienceUser:
		tenantID, userID := strings.TrimSpace(req.TenantID), strings.TrimSpace(req.UserID)
		if tenantID == "" || userID == "" {
			return a, badAnnouncement("tenantId and userId are required for the user audience")
		}
		u, err := s.dir.userByID(ctx, tenantID, userID)
		if err != nil {
			return a, err
		}
		if strings.TrimSpace(req.Username) != "" && !strings.EqualFold(strings.TrimSpace(req.Username), u.Username) {
			return a, errAnnouncementUserUnknown
		}
		a.TenantID, a.UserID, a.Username = u.TenantID, u.UserID, u.Username
	case announcementAudienceSession:
		target := strings.TrimSpace(req.SessionTargetID)
		if target == "" {
			return a, badAnnouncement("sessionTargetId is required for the session audience")
		}
		sess, err := s.dir.sessionByTarget(ctx, target)
		if err != nil {
			return a, err
		}
		if !sess.ExpiresAt.After(now) ||
			(req.TenantID != "" && req.TenantID != sess.TenantID) ||
			(req.UserID != "" && req.UserID != sess.UserID) ||
			(req.Username != "" && !strings.EqualFold(strings.TrimSpace(req.Username), sess.Username)) {
			return a, errAnnouncementSessionBad
		}
		a.TenantID, a.UserID, a.Username, a.SessionTargetID = sess.TenantID, sess.UserID, sess.Username, sess.TargetID
	default:
		return a, badAnnouncement("audience must be system, user, or session")
	}
	return a, nil
}

func (s *announcementService) publish(ctx context.Context, req announcementRequest, actor string) (announcement, error) {
	now := s.clock()
	a, err := s.build(ctx, req, announcement{ID: uuid.NewString(), CreatedBy: actor, CreatedAt: now})
	if err != nil {
		return a, err
	}
	a.UpdatedAt, a.Version = now, 1
	if err := s.store.save(ctx, a); err != nil {
		return a, err
	}
	s.hub.notify()
	return a, nil
}

func (s *announcementService) update(ctx context.Context, id string, req announcementRequest) (announcement, error) {
	existing, err := s.store.get(ctx, id)
	if err != nil {
		return existing, err
	}
	if existing.Cleared || (existing.EndsAt != nil && !s.clock().Before(*existing.EndsAt)) {
		return existing, errAnnouncementNotFound
	}
	if req.Version != 0 && req.Version != existing.Version {
		return existing, errAnnouncementConflict
	}
	a, err := s.build(ctx, req, existing)
	if err != nil {
		return a, err
	}
	a.UpdatedAt, a.Version = s.clock(), existing.Version+1
	if err := s.store.save(ctx, a); err != nil {
		return a, err
	}
	s.hub.notify()
	return a, nil
}

func (s *announcementService) clear(ctx context.Context, id string) (announcement, error) {
	a, err := s.store.get(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Cleared {
		return a, nil
	}
	now := s.clock()
	a.Cleared, a.ClearedAt, a.UpdatedAt, a.Version = true, &now, now, a.Version+1
	if err := s.store.save(ctx, a); err != nil {
		return a, err
	}
	s.hub.notify()
	return a, nil
}

// listActive returns pending and active announcements for Monitor.
func (s *announcementService) listActive(ctx context.Context) ([]announcement, error) {
	all, err := s.store.list(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	out := []announcement{}
	for _, a := range all {
		if !a.Cleared && (a.EndsAt == nil || now.Before(*a.EndsAt)) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ---- Monitor management API ----

func (s *announcementService) adminHandler(authorize func(*http.Request) bool, actorOf func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !authorize(r) {
			http.Error(w, "Administrator service authentication failed.", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, announcementAdminPrefix), "/")
		parts := []string{}
		if path != "" {
			parts = strings.Split(path, "/")
		}
		switch {
		case len(parts) == 0 && r.Method == http.MethodGet:
			list, err := s.listActive(ctx)
			writeAnnouncementResult(w, http.StatusOK, map[string]any{"announcements": list}, err)
		case len(parts) == 0 && r.Method == http.MethodPost:
			var req announcementRequest
			if !decodeAnnouncementBody(w, r, &req) {
				return
			}
			a, err := s.publish(ctx, req, actorOf(r))
			writeAnnouncementResult(w, http.StatusCreated, a, err)
		case len(parts) == 1 && parts[0] == "sessions" && r.Method == http.MethodGet:
			username := strings.TrimSpace(r.URL.Query().Get("username"))
			if username == "" {
				writeAnnouncementResult(w, 0, nil, badAnnouncement("username is required"))
				return
			}
			u, err := s.dir.resolveUser(ctx, username)
			if err != nil {
				writeAnnouncementResult(w, 0, nil, err)
				return
			}
			sessions, err := s.dir.sessionsForUser(ctx, u.TenantID, u.Username)
			writeAnnouncementResult(w, http.StatusOK, map[string]any{"tenantId": u.TenantID, "userId": u.UserID, "username": u.Username, "sessions": sessions}, err)
		case len(parts) == 1 && r.Method == http.MethodGet:
			a, err := s.store.get(ctx, parts[0])
			writeAnnouncementResult(w, http.StatusOK, a, err)
		case len(parts) == 1 && r.Method == http.MethodPut:
			var req announcementRequest
			if !decodeAnnouncementBody(w, r, &req) {
				return
			}
			a, err := s.update(ctx, parts[0], req)
			writeAnnouncementResult(w, http.StatusOK, a, err)
		case (len(parts) == 1 && r.Method == http.MethodDelete) || (len(parts) == 2 && parts[1] == "clear" && r.Method == http.MethodPost):
			a, err := s.clear(ctx, parts[0])
			writeAnnouncementResult(w, http.StatusOK, a, err)
		default:
			http.Error(w, "Announcement operation is not available.", http.StatusNotFound)
		}
	}
}

func decodeAnnouncementBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeAnnouncementResult(w, 0, nil, badAnnouncement("invalid JSON request body"))
		return false
	}
	return true
}

func writeAnnouncementResult(w http.ResponseWriter, status int, body any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code, text := http.StatusInternalServerError, "Announcement service is temporarily unavailable."
		var input announcementInputError
		switch {
		case errors.As(err, &input):
			code, text = http.StatusBadRequest, input.msg
		case errors.Is(err, errAnnouncementNotFound):
			code, text = http.StatusNotFound, "Announcement not found."
		case errors.Is(err, errAnnouncementUserUnknown):
			code, text = http.StatusNotFound, "User not found."
		case errors.Is(err, errAnnouncementSessionBad):
			code, text = http.StatusUnprocessableEntity, "Target session is invalid, expired, revoked, or does not match the tenant/user."
		case errors.Is(err, errAnnouncementConflict):
			code, text = http.StatusConflict, "Announcement was changed by another request; reload and retry."
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": text})
		return
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---- Customer delivery ----

// resolveRecipient derives the caller identity from authentication on every call.
type resolveRecipient func(ctx context.Context) (announcementRecipient, error)

type announcementPayload struct {
	Announcement *announcementView `json:"announcement"`
	Key          string            `json:"key"`
}

func (s *announcementService) snapshot(ctx context.Context, resolve resolveRecipient) (announcementPayload, error) {
	rec, err := resolve(ctx)
	if err != nil {
		return announcementPayload{}, err
	}
	v, err := s.current(ctx, rec)
	return announcementPayload{Announcement: v, Key: announcementKey(v)}, err
}

func writeAnnouncementAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAnnouncementSessionEnd):
		w.Header().Set("X-Ref-Ledger-Session", "invalid")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "mobile_session_invalid", "error": "Your session ended. Please sign in again."})
	case errors.Is(err, errAnnouncementReauth):
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	default:
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
	}
}

// currentHandler returns the current announcement. With ?known=<key> it long
// polls (used by Android, whose HTTP layer cannot stream) until the state
// differs from known or the wait elapses.
func (s *announcementService) currentHandler(resolve resolveRecipient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		known, polling := r.URL.Query()["known"]
		ch, unsubscribe := s.hub.subscribe()
		defer unsubscribe()
		deadline := time.NewTimer(announcementMaxWait)
		defer deadline.Stop()
		tick := time.NewTicker(announcementPollInterval)
		defer tick.Stop()
		for {
			p, err := s.snapshot(r.Context(), resolve)
			if err != nil {
				writeAnnouncementAuthError(w, err)
				return
			}
			if !polling || p.Key != known[0] {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(p)
				return
			}
			select {
			case <-ch:
			case <-tick.C:
			case <-deadline.C:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(p)
				return
			case <-r.Context().Done():
				return
			}
		}
	}
}

// eventsHandler streams changes over Server-Sent Events for web tabs.
func (s *announcementService) eventsHandler(resolve resolveRecipient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}
		if _, err := resolve(r.Context()); err != nil {
			writeAnnouncementAuthError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		ch, unsubscribe := s.hub.subscribe()
		defer unsubscribe()
		tick := time.NewTicker(announcementPollInterval)
		defer tick.Stop()
		last, first := "", true
		for {
			p, err := s.snapshot(r.Context(), resolve)
			if err != nil {
				if errors.Is(err, errAnnouncementSessionEnd) || errors.Is(err, errAnnouncementReauth) {
					fmt.Fprint(w, "event: session_ended\ndata: {}\n\n")
					flusher.Flush()
				}
				return
			}
			if first || p.Key != last {
				data, _ := json.Marshal(p)
				fmt.Fprintf(w, "event: announcement\ndata: %s\n\n", data)
				last, first = p.Key, false
			} else {
				fmt.Fprint(w, ": keep-alive\n\n")
			}
			flusher.Flush()
			select {
			case <-ch:
			case <-tick.C:
			case <-r.Context().Done():
				return
			}
		}
	}
}
