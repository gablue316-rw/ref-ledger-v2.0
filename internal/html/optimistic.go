package main

import (
	"errors"
	"net/http"
	"ref-ledger-v2/internal/database"
	"strings"
)

func requestVersion(r *http.Request) string {
	if v := strings.Trim(r.Header.Get("If-Match"), "\""); v != "" {
		return v
	}
	return r.URL.Query().Get("version")
}
func writeConflict(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, database.ErrConflict) {
		return false
	}
	http.Error(w, database.ErrConflict.Error(), http.StatusConflict)
	return true
}
