package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRuntimeHost_AppliesMountedRoutes(t *testing.T) {
	host := NewHost(nil, nil, nil, NoopContextAssembler{}, nil)
	host.MountPublic(func(r chi.Router) {
		r.Get("/public/ping", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	host.MountProtected(func(r chi.Router) {
		r.Get("/protected/ping", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})
	})

	r := chi.NewRouter()
	host.ApplyPublic(r)
	host.ApplyProtected(r)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/public/ping", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("public route status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/protected/ping", nil))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("protected route status = %d", rr.Code)
	}
}
