package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chickenzord/finorama/internal/api"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

func setupTestAPI(t *testing.T) (*api.API, *vault.Vault, *store.Store, func()) {
	tmpDir, err := os.MkdirTemp("", "finorama-api-test-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}

	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}

	v := vault.New()
	syncWorker := syncer.New(st, v, tmpDir)
	mw := api.NewMiddleware(v, []string{"http://localhost:5173"})
	apiServer := api.New(st, v, syncWorker, mw)

	cleanup := func() {
		st.Close()
		os.RemoveAll(tmpDir)
	}

	return apiServer, v, st, cleanup
}

func TestVaultLifecycleAndAPIAuth(t *testing.T) {
	apiServer, _, _, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	// 1. Initial vault status -> uninitialized, locked
	req := httptest.NewRequest(http.MethodGet, "/vault/status", nil)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status expected 200, got %d", rec.Code)
	}

	var statusResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &statusResp)
	if statusResp["initialized"] != false || statusResp["unlocked"] != false {
		t.Fatalf("expected uninitialized & locked, got %+v", statusResp)
	}

	// 2. Protected endpoint without token -> 401
	req = httptest.NewRequest(http.MethodGet, "/portfolio", nil)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// 3. Init Vault
	initBody := bytes.NewBufferString(`{"passphrase":"mastersecretpass123","confirm_passphrase":"mastersecretpass123"}`)
	req = httptest.NewRequest(http.MethodPost, "/vault/init", initBody)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("init expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var initResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &initResp)
	token, ok := initResp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("expected token in init response, got %+v", initResp)
	}

	// 4. Access protected endpoint with token -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/portfolio", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("portfolio expected 200, got %d", rec.Code)
	}

	// 5. Add Account via API
	accBody := bytes.NewBufferString(`{"name":"My Broker","username":"user@ksei.co.id","password":"kseipassword1"}`)
	req = httptest.NewRequest(http.MethodPost, "/accounts", accBody)
	req.Header.Set("X-Finorama-Token", token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create account expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 6. Lock Vault
	req = httptest.NewRequest(http.MethodPost, "/vault/lock", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("lock expected 200, got %d", rec.Code)
	}

	// 7. Verify token revoked and vault locked
	req = httptest.NewRequest(http.MethodGet, "/portfolio", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after lock, got %d", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	apiServer, _, _, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	req := httptest.NewRequest(http.MethodOptions, "/portfolio", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight expected 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("expected allow origin http://localhost:5173, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}
