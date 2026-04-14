package domain

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/storage"
)

func TestLocalAuthRoutesRejectRemoteClients(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "atlas.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	authSvc := auth.NewService(db)
	localAuthSvc, err := auth.NewLocalAuthService(db, 1984)
	if err != nil {
		t.Fatalf("new local auth service: %v", err)
	}
	authSvc.SetLocalAuth(localAuthSvc)

	domain := NewLocalAuthDomain(authSvc, localAuthSvc)
	r := chi.NewRouter()
	domain.RegisterPublic(r)

	req := httptest.NewRequest(http.MethodGet, "/auth/local/status", nil)
	req.RemoteAddr = "192.168.1.44:5050"
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote local-auth request, got %d", rr.Code)
	}
}

func TestLocalAuthRoutesAllowLoopbackClients(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "atlas.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	authSvc := auth.NewService(db)
	localAuthSvc, err := auth.NewLocalAuthService(db, 1984)
	if err != nil {
		t.Fatalf("new local auth service: %v", err)
	}
	authSvc.SetLocalAuth(localAuthSvc)

	domain := NewLocalAuthDomain(authSvc, localAuthSvc)
	r := chi.NewRouter()
	domain.RegisterPublic(r)

	req := httptest.NewRequest(http.MethodGet, "/auth/local/status", nil)
	req.RemoteAddr = "127.0.0.1:5050"
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback local-auth request, got %d", rr.Code)
	}
}
