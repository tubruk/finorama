package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/chickenzord/finorama/internal/api"
	"github.com/chickenzord/finorama/internal/crypto"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

type Server struct {
	store      *store.Store
	vault      *vault.Vault
	syncer     *syncer.Syncer
	apiHandler http.Handler
	templates  map[string]*template.Template
	sessionMu  sync.RWMutex
	sessions   map[string]time.Time
}

func NewWithTemplateFS(st *store.Store, v *vault.Vault, sync *syncer.Syncer, templatesFS fs.FS) (*Server, error) {
	printer := message.NewPrinter(language.Indonesian)
	funcMap := template.FuncMap{
		"formatIDR": func(v float64) string {
			return "IDR " + printer.Sprintf("%d", int64(v))
		},
		"formatNumber": func(v float64) string {
			return printer.Sprintf("%.2f", v)
		},
	}

	// Each page needs its own cloned or paired template with layout.html
	pages := []string{"unlock.html", "dashboard.html", "accounts.html"}
	tmplMap := make(map[string]*template.Template)

	for _, page := range pages {
		t, err := template.New("layout.html").Funcs(funcMap).ParseFS(
			templatesFS,
			"web/templates/layout.html",
			"web/templates/"+page,
		)
		if err != nil {
			return nil, fmt.Errorf("parse template for %s: %w", page, err)
		}
		tmplMap[page] = t
	}

	// API middleware with local frontend dev origins
	allowedOrigins := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:3000",
		"http://127.0.0.1:3000",
	}
	apiMW := api.NewMiddleware(v, allowedOrigins)
	apiServer := api.New(st, v, sync, apiMW)

	return &Server{
		store:      st,
		vault:      v,
		syncer:     sync,
		apiHandler: http.StripPrefix("/api/v1", apiServer.Routes()),
		templates:  tmplMap,
		sessions:   make(map[string]time.Time),
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// REST API mounted under /api/v1/
	mux.Handle("/api/v1/", s.apiHandler)

	// HTML Monolith routes
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/unlock", s.handleUnlock)
	mux.HandleFunc("/init", s.handleInit)
	mux.HandleFunc("/lock", s.handleLock)
	mux.HandleFunc("/accounts", s.handleAccounts)
	mux.HandleFunc("/accounts/delete", s.handleAccountDelete)
	mux.HandleFunc("/sync", s.handleSync)

	return mux
}

func (s *Server) hasActiveSession(r *http.Request) bool {
	cookie, err := r.Cookie("finorama_session")
	if err != nil || cookie.Value == "" {
		return false
	}

	s.sessionMu.RLock()
	expiry, exists := s.sessions[cookie.Value]
	s.sessionMu.RUnlock()

	if !exists || time.Now().After(expiry) {
		return false
	}
	return s.vault.IsUnlocked()
}

func (s *Server) createSession(w http.ResponseWriter) {
	token, _ := crypto.GenerateRandomToken()
	s.sessionMu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "finorama_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.IsInitialized()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !initialized {
		http.Redirect(w, r, "/init", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		s.render(w, "unlock.html", map[string]any{
			"IsFirstTime": false,
			"IsUnlocked":  false,
		})
		return
	}

	if r.Method == http.MethodPost {
		pass := r.FormValue("passphrase")
		key, valid, err := s.store.VerifyAndDeriveKey(pass)
		if err != nil {
			log.Printf("[Server] Error verifying master passphrase: %v", err)
			s.render(w, "unlock.html", map[string]any{"Error": "Database error: " + err.Error()})
			return
		}
		if !valid {
			log.Printf("[Server] Failed unlock attempt: invalid passphrase from %s", r.RemoteAddr)
			s.render(w, "unlock.html", map[string]any{"Error": "Invalid master passphrase"})
			return
		}

		s.vault.Unlock(key)
		s.createSession(w)
		log.Printf("[Server] Vault unlocked successfully")
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.IsInitialized()
	if err != nil {
		log.Printf("[Server] Error checking DB init status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if initialized {
		http.Redirect(w, r, "/unlock", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		s.render(w, "unlock.html", map[string]any{
			"IsFirstTime": true,
			"IsUnlocked":  false,
		})
		return
	}

	if r.Method == http.MethodPost {
		pass := r.FormValue("passphrase")
		confirm := r.FormValue("confirm_passphrase")
		if len(pass) < 6 {
			s.render(w, "unlock.html", map[string]any{
				"IsFirstTime": true,
				"Error":       "Passphrase must be at least 6 characters",
			})
			return
		}
		if pass != confirm {
			s.render(w, "unlock.html", map[string]any{
				"IsFirstTime": true,
				"Error":       "Passphrases do not match",
			})
			return
		}

		key, err := s.store.InitializeMaster(pass)
		if err != nil {
			log.Printf("[Server] Failed to initialize master vault: %v", err)
			s.render(w, "unlock.html", map[string]any{
				"IsFirstTime": true,
				"Error":       "Initialization error: " + err.Error(),
			})
			return
		}

		s.vault.Unlock(key)
		s.createSession(w)
		log.Printf("[Server] Finorama master vault initialized and unlocked")
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	s.vault.Lock()
	s.syncer.ClearSessionCache()
	log.Printf("[Server] Vault locked by user and session cache cleared")
	http.SetCookie(w, &http.Cookie{
		Name:     "finorama_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/unlock", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.hasActiveSession(r) {
		http.Redirect(w, r, "/unlock", http.StatusSeeOther)
		return
	}

	overview, err := s.store.GetPortfolioOverview()
	if err != nil {
		log.Printf("[Server] Error querying portfolio overview: %v", err)
		http.Error(w, "Failed to load portfolio: "+err.Error(), http.StatusInternalServerError)
		return
	}

	histJSON, _ := json.Marshal(overview.Historical)

	s.render(w, "dashboard.html", map[string]any{
		"IsUnlocked":     true,
		"Overview":       overview,
		"HistoricalJSON": template.JS(histJSON),
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.hasActiveSession(r) {
		http.Redirect(w, r, "/unlock", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		username := r.FormValue("username")
		password := r.FormValue("password")

		key, err := s.vault.Key()
		if err != nil {
			log.Printf("[Server] AddAccount failed: vault is locked")
			http.Redirect(w, r, "/unlock", http.StatusSeeOther)
			return
		}

		newAcc, err := s.store.AddAccount(key, name, username, password)
		if err != nil {
			log.Printf("[Server] Failed to add account '%s' (%s): %v", name, username, err)
			accs, _ := s.store.ListAccounts()
			s.render(w, "accounts.html", map[string]any{
				"IsUnlocked": true,
				"Accounts":   accs,
				"Error":      "Failed to add account: " + err.Error(),
			})
			return
		}

		log.Printf("[Server] Added new encrypted account ID %d: '%s' (%s)", newAcc.ID, newAcc.Name, newAcc.Username)
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
		return
	}

	accs, err := s.store.ListAccounts()
	if err != nil {
		log.Printf("[Server] Error listing accounts: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "accounts.html", map[string]any{
		"IsUnlocked": true,
		"Accounts":   accs,
	})
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	if !s.hasActiveSession(r) {
		http.Redirect(w, r, "/unlock", http.StatusSeeOther)
		return
	}

	idStr := r.FormValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err == nil {
		if delErr := s.store.DeleteAccount(id); delErr != nil {
			log.Printf("[Server] Error deleting account ID %d: %v", id, delErr)
		} else {
			log.Printf("[Server] Deleted account ID %d", id)
		}
	}
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if !s.hasActiveSession(r) {
		http.Error(w, "Vault locked", http.StatusUnauthorized)
		return
	}

	accountIDStr := r.URL.Query().Get("account_id")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var err error
	if accountIDStr != "" {
		accID, parseErr := strconv.ParseInt(accountIDStr, 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid account id", http.StatusBadRequest)
			return
		}
		log.Printf("[Server] Manual sync triggered for account ID %d", accID)
		err = s.syncer.SyncAccountByID(ctx, accID)
	} else {
		log.Printf("[Server] Manual sync triggered for all accounts")
		err = s.syncer.SyncAll(ctx)
	}

	if err != nil {
		log.Printf("[Server] Manual sync failed: %v", err)
	} else {
		log.Printf("[Server] Manual sync completed successfully")
	}

	w.Header().Set("Content-Type", "text/html")
	if err != nil {
		fmt.Fprintf(w, `<div class="p-3.5 mb-4 rounded-xl bg-rose-950/70 border border-rose-800 text-sm text-rose-300">Sync completed with issues: %s</div>`, err.Error())
	} else {
		fmt.Fprintf(w, `<div class="p-3.5 mb-4 rounded-xl bg-emerald-950/70 border border-emerald-800 text-sm text-emerald-300 flex items-center justify-between"><span>Sync completed successfully!</span><a href="/" class="underline font-semibold ml-2">Refresh View</a></div>`)
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	tmpl, exists := s.templates[name]
	if !exists {
		log.Printf("template not found: %s", name)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := tmpl.ExecuteTemplate(w, "layout.html", data)
	if err != nil {
		log.Printf("template render error for %s: %v", name, err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}
