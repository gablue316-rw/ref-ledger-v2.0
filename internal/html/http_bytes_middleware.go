package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"ref-ledger-v2/internal/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Install in main.go with:
// loggedMux := LogRequest(HTTPBytesMiddleware(mux))
// Build the entire package (go build .), so this file is included.
// Counts HTTP body bytes only, excluding headers and transport overhead.
// Request bytes count only bytes read by handlers; unread bodies are not drained.
// Bytes written after connection hijacking are not counted.
// HTTP totals use category="http" and collection="http"; collection-operation
// byte metrics are separate and should not be added to these HTTP totals.
func HTTPBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		tenantID := "anonymous"
		// Resolve before logout can delete the session. Login requests without an
		// existing valid session are attributed to anonymous.
		if session, err := database.GetSession(r); err == nil && session != nil &&
			strings.TrimSpace(session.TenantID) != "" && session.TenantID != "na" {
			tenantID = session.TenantID
		}
		var body *httpByteRequestBody
		if r.Body != nil {
			body = &httpByteRequestBody{ReadCloser: r.Body}
			r.Body = body
		}
		response := &httpByteResponseWriter{ResponseWriter: w, method: r.Method}
		defer func() {
			var bytesIn int64
			if body != nil {
				bytesIn = body.bytesRead
			}
			log.Printf("HTTP Method=%s Path=%s BytesIn=%d BytesOut=%d Duration=%s",
				r.Method, r.URL.Path, bytesIn, response.bytesWritten, time.Since(started))
			saveHTTPByteMetrics(tenantID, started.UTC().Truncate(time.Minute), bytesIn, response.bytesWritten)
		}()
		next.ServeHTTP(response, r)
	})
}

type httpByteRequestBody struct {
	io.ReadCloser
	bytesRead int64
}

func (b *httpByteRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytesRead += int64(n)
	return n, err
}

type httpByteResponseWriter struct {
	http.ResponseWriter
	bytesWritten int64
	status       int
	method       string
}

func (w *httpByteResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *httpByteResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
}
func (w *httpByteResponseWriter) Write(p []byte) (int, error) {
	// Let the underlying writer perform automatic Content-Type detection.
	n, err := w.ResponseWriter.Write(p)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.method != http.MethodHead {
		w.bytesWritten += int64(n)
	}
	return n, err
}
func (w *httpByteResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	// Hide ReadFrom to avoid recursion and count file-download writes.
	return io.Copy(struct{ io.Writer }{w}, r)
}
func (w *httpByteResponseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *httpByteResponseWriter) Flush() { _ = w.FlushError() }
func (w *httpByteResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func saveHTTPByteMetrics(tenantID string, bucketStart time.Time, bytesIn, bytesOut int64) {
	if database.Client == nil || database.Database == "" {
		return
	}
	// A client disconnect must not cancel the metrics write. Writes are bounded
	// and synchronous; this does not create a goroutine for every request.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	coll := database.Client.Database(database.Database).Collection("metrics")
	filter := bson.M{"tenantId": tenantID, "bucketStart": bucketStart, "category": "http", "collection": "http"}
	update := bson.M{"$inc": bson.M{"requests": int64(1), "bytesIn": bytesIn, "bytesOut": bytesOut}}
	_, err := coll.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		_, err = coll.UpdateOne(ctx, filter, update)
	}
	if err != nil {
		log.Printf("Unable to record HTTP byte metrics: %v", err)
	}
}
