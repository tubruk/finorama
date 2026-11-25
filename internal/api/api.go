package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

type API struct {
	store      *store.Store
	vault      *vault.Vault
	syncer     *syncer.Syncer
	middleware *Middleware
}

func New(st *store.Store, v *vault.Vault, syncWorker *syncer.Syncer, mw *Middleware) *API {
	return &API{
		store:      st,
		vault:      v,
		syncer:     syncWorker,
		middleware: mw,
	}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	rateLimiter := a.middleware.RateLimit(10, 2) // 10 burst, 2 req/sec
	authGuard := a.middleware.RequireUnlocked

	// Vault operations
	mux.Handle("GET /vault/status", rateLimiter(http.HandlerFunc(a.handleVaultStatus)))
	mux.Handle("POST /vault/init", rateLimiter(http.HandlerFunc(a.handleVaultInit)))
	mux.Handle("POST /vault/unlock", rateLimiter(http.HandlerFunc(a.handleVaultUnlock)))
	mux.Handle("POST /vault/lock", authGuard(http.HandlerFunc(a.handleVaultLock)))

	// Protected Portfolio data
	mux.Handle("GET /portfolio", authGuard(http.HandlerFunc(a.handlePortfolio)))

	// Protected Accounts management
	mux.Handle("GET /accounts", authGuard(http.HandlerFunc(a.handleAccountsList)))
	mux.Handle("POST /accounts", authGuard(rateLimiter(http.HandlerFunc(a.handleAccountsAdd))))
	mux.Handle("DELETE /accounts/{id}", authGuard(http.HandlerFunc(a.handleAccountsDelete)))

	// Protected Sync operations
	mux.Handle("POST /sync", authGuard(http.HandlerFunc(a.handleSyncTrigger)))
	mux.Handle("GET /sync/status", authGuard(http.HandlerFunc(a.handleSyncStatus)))

	return a.middleware.CORS(a.middleware.Logging(mux))
}

// GET /vault/status
func (a *API) handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	init, err := a.store.IsInitialized()
	if err != nil {
		slog.Error("Failed to check vault init", "err", err)
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query database status")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"initialized": init,
		"unlocked":    a.vault.IsUnlocked(),
	})
}

// POST /vault/init
type initRequest struct {
	Passphrase        string `json:"passphrase"`
	ConfirmPassphrase string `json:"confirm_passphrase"`
}

func (a *API) handleVaultInit(w http.ResponseWriter, r *http.Request) {
	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid JSON request body")
		return
	}

	if len(req.Passphrase) < 8 {
		WriteError(w, http.StatusBadRequest, "WEAK_PASSPHRASE", "Passphrase must be at least 8 characters")
		return
	}
	if req.Passphrase != req.ConfirmPassphrase {
		WriteError(w, http.StatusBadRequest, "MISMATCH_PASSPHRASE", "Passphrases do not match")
		return
	}

	key, err := a.store.InitializeMaster(req.Passphrase)
	if err != nil {
		slog.Error("Failed to initialize vault", "err", err)
		WriteError(w, http.StatusBadRequest, "INIT_FAILED", err.Error())
		return
	}

	a.vault.Unlock(key)
	token, _ := a.middleware.CreateToken()
	slog.Info("Finorama master vault initialized and unlocked")

	WriteJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"message": "Vault successfully initialized",
	})
}

// POST /vault/unlock
type unlockRequest struct {
	Passphrase string `json:"passphrase"`
}

func (a *API) handleVaultUnlock(w http.ResponseWriter, r *http.Request) {
	var req unlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid JSON request body")
		return
	}

	key, valid, err := a.store.VerifyAndDeriveKey(req.Passphrase)
	if err != nil {
		slog.Error("Error checking master passphrase", "err", err)
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to verify passphrase")
		return
	}

	if !valid {
		slog.Warn("Failed unlock attempt: invalid passphrase", "client_ip", r.RemoteAddr)
		WriteError(w, http.StatusUnauthorized, "INVALID_PASSPHRASE", "Invalid master passphrase")
		return
	}

	a.vault.Unlock(key)
	token, _ := a.middleware.CreateToken()
	slog.Info("Finorama master vault unlocked successfully")

	WriteJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"message": "Vault unlocked",
	})
}

// POST /vault/lock
func (a *API) handleVaultLock(w http.ResponseWriter, r *http.Request) {
	a.vault.Lock()
	a.syncer.ClearSessionCache()
	a.middleware.RevokeAllTokens()
	slog.Info("Finorama master vault locked and tokens revoked")

	WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Vault locked and sessions purged",
	})
}

// GET /portfolio
func (a *API) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	overview, err := a.store.GetPortfolioOverview()
	if err != nil {
		slog.Error("Error fetching portfolio overview", "err", err)
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch portfolio data")
		return
	}

	WriteJSON(w, http.StatusOK, overview)
}

// GET /accounts
func (a *API) handleAccountsList(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.store.ListAccounts()
	if err != nil {
		slog.Error("Error listing accounts", "err", err)
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list accounts")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"accounts": accounts,
	})
}

// POST /accounts
type addAccountRequest struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) handleAccountsAdd(w http.ResponseWriter, r *http.Request) {
	var req addAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid JSON request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" || req.Username == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Name, username and password are required")
		return
	}

	key, err := a.vault.Key()
	if err != nil {
		WriteError(w, http.StatusLocked, "VAULT_LOCKED", "Vault is locked")
		return
	}

	acc, err := a.store.AddAccount(key, req.Name, req.Username, req.Password)
	if err != nil {
		slog.Error("Failed to add account", "username", req.Username, "err", err)
		WriteError(w, http.StatusBadRequest, "ADD_FAILED", err.Error())
		return
	}

	slog.Info("Added new encrypted KSEI account", "id", acc.ID, "name", acc.Name, "username", acc.Username)
	WriteJSON(w, http.StatusCreated, acc)
}

// DELETE /accounts/{id}
func (a *API) handleAccountsDelete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "INVALID_ID", "Invalid account ID")
		return
	}

	if err := a.store.DeleteAccount(id); err != nil {
		slog.Error("Failed to delete account", "id", id, "err", err)
		WriteError(w, http.StatusInternalServerError, "DELETE_FAILED", "Failed to delete account")
		return
	}

	slog.Info("Deleted KSEI account", "id", id)
	WriteJSON(w, http.StatusOK, map[string]any{"message": "Account deleted successfully"})
}

// POST /sync
func (a *API) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	if a.syncer.IsSyncing() {
		WriteError(w, http.StatusConflict, "SYNC_RUNNING", "A synchronization is already in progress")
		return
	}

	accIDStr := r.URL.Query().Get("account_id")
	var targetAccID int64
	if accIDStr != "" {
		parsed, err := strconv.ParseInt(accIDStr, 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "INVALID_ACCOUNT_ID", "Invalid account ID")
			return
		}
		targetAccID = parsed
	}

	// Trigger async background job
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		slog.Info("Triggering background sync job", "target_account_id", targetAccID)
		var syncErr error
		if targetAccID > 0 {
			syncErr = a.syncer.SyncAccountByID(ctx, targetAccID)
		} else {
			syncErr = a.syncer.SyncAll(ctx)
		}

		if syncErr != nil {
			slog.Error("Background sync job finished with error", "err", syncErr)
		} else {
			slog.Info("Background sync job completed successfully")
		}
	}()

	WriteJSON(w, http.StatusAccepted, map[string]any{
		"status":  "started",
		"message": "Sync started in background",
	})
}

// GET /sync/status
func (a *API) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"is_syncing": a.syncer.IsSyncing(),
	})
}
