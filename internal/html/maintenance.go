package main

import (
	"encoding/json"
	"net/http"
	"ref-ledger-v2/internal/database"
)

func maintenanceUnavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "60")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Changes are temporarily unavailable during maintenance, or the maintenance status could not be verified. Please try again later."})
}

func maintenanceReadRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		// Email confirmation changes an account, even though it uses GET.
		return r.URL.Path != "/api/confirmEmail"
	default:
		return false
	}
}

func maintenanceMiddleware(next http.Handler) http.Handler {
	return maintenanceMiddlewareWithAdmission(next, func() (func(), error) {
		return beginMaintenanceWrite(database.Client.Database(database.Database))
	})
}

func maintenanceMiddlewareWithAdmission(next http.Handler, begin func() (func(), error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Proxy requests are checked by the Monitor service. Session operations are
		// intentionally available; these are not business-data backup locks.
		if maintenanceReadRequest(r) || r.URL.Path == "/api/login" || r.URL.Path == "/api/logout" || r.URL.Path == "/admin" || len(r.URL.Path) >= 7 && r.URL.Path[:7] == "/admin/" {
			next.ServeHTTP(w, r)
			return
		}
		release, err := begin()
		if err != nil {
			maintenanceUnavailable(w, r)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
