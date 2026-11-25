package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chickenzord/finorama/internal/server"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

// test server package

func TestInitPageRendering(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "finorama-srv-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, tmpDir)

	// Read real templates
	rootFS := os.DirFS("../..")
	srv, err := server.NewWithTemplateFS(st, v, syncWorker, rootFS)
	if err != nil {
		t.Fatalf("NewWithTemplateFS failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/init", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Setup Master Passphrase") {
		t.Fatalf("body does not contain 'Setup Master Passphrase', length=%d, body:\n%s", len(body), body)
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("body missing layout html wrapper")
	}
}

func TestRateLimitingFormEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "finorama-srv-rate-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, tmpDir)

	rootFS := os.DirFS("../..")
	srv, err := server.NewWithTemplateFS(st, v, syncWorker, rootFS)
	if err != nil {
		t.Fatalf("NewWithTemplateFS failed: %v", err)
	}

	handler := srv.Routes()

	// Make 10 requests to POST /unlock (capacity is 10)
	clientIP := "198.51.100.5:1234"
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader("passphrase=badpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = clientIP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Should not be 429
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 on request %d", i+1)
		}
	}

	// 11th request should be 429 Too Many Requests
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader("passphrase=badpass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = clientIP
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on 11th request, got %d", rec.Code)
	}
}
