package server

import (
	"log"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// requestLogger logs method/path/status/latency without query strings.
// This prevents sensitive query parameters (for example auth bootstrap tokens)
// from being emitted to logs.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		log.Printf("%q from %s - %d in %s", r.Method+" "+r.URL.Path, r.RemoteAddr, ww.Status(), time.Since(start))
	})
}
