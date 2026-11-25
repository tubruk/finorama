package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chickenzord/finorama/internal/api"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

func setupTestAPI(t *testing.T) (*api.API, *vault.Vault, *store.Store, func()) {
	t.Helper()
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

func TestVaultInitValidation(t *testing.T) {
	apiServer, _, _, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	testCases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid JSON",
			body:       `{not-valid-json`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_BODY",
		},
		{
			name:       "empty passphrase",
			body:       `{"passphrase":"","confirm_passphrase":""}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "WEAK_PASSPHRASE",
		},
		{
			name:       "short passphrase less than 8 chars",
			body:       `{"passphrase":"short12","confirm_passphrase":"short12"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "WEAK_PASSPHRASE",
		},
		{
			name:       "mismatch confirm_passphrase",
			body:       `{"passphrase":"validpass123","confirm_passphrase":"mismatch123"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "MISMATCH_PASSPHRASE",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/vault/init", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d (%s)", tc.wantStatus, rec.Code, rec.Body.String())
			}

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error object in response: %+v", resp)
			}
			if errObj["code"] != tc.wantCode {
				t.Errorf("expected error code %q, got %q", tc.wantCode, errObj["code"])
			}
		})
	}
}

func TestVaultUnlock(t *testing.T) {
	apiServer, _, st, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	// Initialize vault directly
	pass := "correct-master-passphrase"
	if _, err := st.InitializeMaster(context.Background(), pass); err != nil {
		t.Fatalf("init master: %v", err)
	}

	// 1. Invalid JSON body
	req := httptest.NewRequest(http.MethodPost, "/vault/unlock", bytes.NewBufferString(`{invalid-json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid json, got %d", rec.Code)
	}

	// 2. Wrong passphrase -> 401 Unauthorized
	req = httptest.NewRequest(http.MethodPost, "/vault/unlock", bytes.NewBufferString(`{"passphrase":"wrong-passphrase"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong passphrase, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. Correct passphrase -> 200 OK with token
	req = httptest.NewRequest(http.MethodPost, "/vault/unlock", bytes.NewBufferString(fmt.Sprintf(`{"passphrase":%q}`, pass)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on correct passphrase, got %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Errorf("expected non-empty token")
	}
}

func TestAccountsEndpoints(t *testing.T) {
	apiServer, _, _, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	// Init vault
	initBody := bytes.NewBufferString(`{"passphrase":"mastersecretpass123","confirm_passphrase":"mastersecretpass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/vault/init", initBody)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	var initResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &initResp)
	token := initResp["token"].(string)

	// 1. Invalid JSON body when adding account
	req = httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(`{invalid`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid json, got %d", rec.Code)
	}

	// 2. Missing required fields
	req = httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(`{"name":"","username":"","password":""}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing fields, got %d", rec.Code)
	}

	// 3. Successfully create account
	accBody := `{"name":"Broker A","username":"broker_user@test.com","password":"secretpassword"}`
	req = httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(accBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d (%s)", rec.Code, rec.Body.String())
	}

	var createdAcc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &createdAcc)
	accID := int64(createdAcc["id"].(float64))

	// 4. Duplicate account creation -> 400 Bad Request
	req = httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(accBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate account, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 5. List accounts
	req = httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list accounts, got %d", rec.Code)
	}
	var listResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	accs := listResp["accounts"].([]any)
	if len(accs) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accs))
	}

	// 6. DELETE /accounts/{id} with invalid ID format
	req = httptest.NewRequest(http.MethodDelete, "/accounts/abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on non-numeric account ID, got %d", rec.Code)
	}

	// 7. DELETE /accounts/{id} valid ID
	deleteURL := fmt.Sprintf("/accounts/%d", accID)
	req = httptest.NewRequest(http.MethodDelete, deleteURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete account, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Verify account is removed from list
	req = httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	var listAfterDelete map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &listAfterDelete)
	accsAfter, _ := listAfterDelete["accounts"].([]any)
	if len(accsAfter) != 0 {
		t.Errorf("expected 0 accounts after deletion, got %d", len(accsAfter))
	}
}

func TestSyncWhenVaultLocked(t *testing.T) {
	apiServer, v, _, cleanup := setupTestAPI(t)
	defer cleanup()

	routes := apiServer.Routes()

	// Init vault first
	initBody := bytes.NewBufferString(`{"passphrase":"mastersecretpass123","confirm_passphrase":"mastersecretpass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/vault/init", initBody)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	var initResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &initResp)
	token := initResp["token"].(string)

	// Lock the vault in RAM directly while keeping token valid
	v.Lock()

	// POST /sync should fail with 423 Locked
	req = httptest.NewRequest(http.MethodPost, "/sync", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423 StatusLocked when vault is locked, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	v := vault.New()
	mw := api.NewMiddleware(v, []string{})

	// Capacity: 5, Refill: 0.1 tokens/sec
	limiter := mw.RateLimit(5, 0.1)
	handler := limiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	clientIP := "203.0.113.10:4321"

	// First 5 requests should succeed
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = clientIP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rec.Code)
		}
	}

	// 6th request should hit rate limit (429 Too Many Requests)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = clientIP
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", rec.Code)
	}

	// A different client IP should still be allowed
	reqOther := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqOther.RemoteAddr = "203.0.113.20:5432"
	recOther := httptest.NewRecorder()
	handler.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusOK {
		t.Fatalf("different IP expected 200, got %d", recOther.Code)
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
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials to be true for whitelisted origin")
	}

	// Test wildcard origin handling does not reflect arbitrary origin with credentials
	wildcardMW := api.NewMiddleware(nil, []string{"*"})
	wildcardHandler := wildcardMW.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	reqWild := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqWild.Header.Set("Origin", "http://evil.com")
	recWild := httptest.NewRecorder()
	wildcardHandler.ServeHTTP(recWild, reqWild)

	if recWild.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("expected allow origin '*', got %s", recWild.Header().Get("Access-Control-Allow-Origin"))
	}
	if recWild.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("expected no Access-Control-Allow-Credentials header with wildcard origin, got %s", recWild.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestRecoverMiddleware(t *testing.T) {
	mw := api.NewMiddleware(nil, nil)

	// API path panic -> JSON 500
	apiPanicHandler := mw.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went critically wrong in api")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/panic", nil)
	rec := httptest.NewRecorder()
	apiPanicHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 on panic, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL_SERVER_ERROR") {
		t.Errorf("expected INTERNAL_SERVER_ERROR in json body, got %s", rec.Body.String())
	}

	// Non-API path panic -> plain text 500
	webPanicHandler := mw.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went critically wrong in web")
	}))

	reqWeb := httptest.NewRequest(http.MethodGet, "/classic/panic", nil)
	recWeb := httptest.NewRecorder()
	webPanicHandler.ServeHTTP(recWeb, reqWeb)

	if recWeb.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 on panic, got %d", recWeb.Code)
	}
}
