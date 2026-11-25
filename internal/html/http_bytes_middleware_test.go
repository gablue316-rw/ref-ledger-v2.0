package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCompletionMetadata(t *testing.T) {
	for _, status := range []int{200, 400, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /games/{id}", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				setHTTPLogIdentity(r.Context(), "tenant", "user")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("result"))
			})
			count := 0
			handler := httpBytesMiddleware(mux, func(id, method, route, tenant, user string, gotStatus int, in, out, elapsed int64, panicked bool) {
				count++
				if id == "" || method != "POST" || route != "POST /games/{id}" ||
					tenant != "tenant" || user != "user" || gotStatus != status ||
					in != 4 || out != 6 || elapsed < 0 || panicked {
					t.Errorf("unexpected completion: %q %q %q %q %q %d %d %d %d %v", id, method, route, tenant, user, gotStatus, in, out, elapsed, panicked)
				}
			})
			request := httptest.NewRequest("POST", "/games/123?secret=hidden", strings.NewReader("body"))
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if count != 1 {
				t.Fatalf("got %d completion records", count)
			}
		})
	}
}

func TestHTTPCompletionOnPanic(t *testing.T) {
	count := 0
	handler := httpBytesMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("failure")
	}), func(_, _, _, _, _ string, status int, _, _, _ int64, panicked bool) {
		count++
		if !panicked || status != http.StatusInternalServerError {
			t.Errorf("status=%d panic=%v", status, panicked)
		}
	})
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if count != 1 {
		t.Fatalf("got %d completion records", count)
	}
}
