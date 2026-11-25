package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	cliHandler "github.com/chickenzord/finorama/internal/cli"
	"github.com/chickenzord/finorama/internal/logx"
	"github.com/chickenzord/finorama/internal/server"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

//go:embed web/templates/*.html
var templatesFS embed.FS

func printUsage() {
	fmt.Print(`Finorama - Personal finance overview for KSEI

Usage:
  finorama [command] [flags]

Commands:
  serve               Start the web UI server (default)
  account add         Interactively connect & test KSEI credentials
  holdings [acc_id]   Show latest holdings and balances for an account
  sync [acc_id]       Manually trigger sync for an account or all accounts

Flags:
  -host string        Host/IP to bind HTTP server (default "127.0.0.1")
  -data-dir string    Directory for SQLite DB and cache (default "./data")
  -port int           HTTP port for web server (default 8080)
  -log-access         Enable verbose HTTP access logging (default false)
`)
}

func main() {
	logx.InitLogger()

	var host string
	var dataDir string
	var port int
	var logAccess bool

	args := os.Args[1:]
	var cmd string
	var subArgs []string

	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		subArgs = args[1:]
	} else {
		cmd = "serve"
		subArgs = args
	}

	flags := flag.NewFlagSet("finorama", flag.ExitOnError)
	flags.StringVar(&host, "host", "127.0.0.1", "Host address to bind HTTP server")
	flags.StringVar(&dataDir, "data-dir", "./data", "Directory to store sqlite DB and sessions")
	flags.IntVar(&port, "port", 8080, "HTTP server port")
	flags.BoolVar(&logAccess, "log-access", false, "Enable verbose HTTP access logging")

	switch cmd {
	case "help", "-h", "--help":
		printUsage()
		return

	case "account":
		if len(subArgs) == 0 || subArgs[0] != "add" {
			fmt.Println("Usage: finorama account add")
			os.Exit(1)
		}
		_ = flags.Parse(subArgs[1:])
		runCLI(dataDir, func(c *cliHandler.CLI) error {
			return c.AddAccount()
		})

	case "holdings":
		var targetID int64
		if len(subArgs) > 0 && !isFlag(subArgs[0]) {
			parsed, err := strconv.ParseInt(subArgs[0], 10, 64)
			if err == nil {
				targetID = parsed
			}
			_ = flags.Parse(subArgs[1:])
		} else {
			_ = flags.Parse(subArgs)
		}

		runCLI(dataDir, func(c *cliHandler.CLI) error {
			return c.ShowHoldings(targetID)
		})

	case "sync":
		var targetID int64
		if len(subArgs) > 0 && !isFlag(subArgs[0]) {
			parsed, err := strconv.ParseInt(subArgs[0], 10, 64)
			if err == nil {
				targetID = parsed
			}
			_ = flags.Parse(subArgs[1:])
		} else {
			_ = flags.Parse(subArgs)
		}

		runCLI(dataDir, func(c *cliHandler.CLI) error {
			return c.SyncManual(targetID)
		})

	case "serve":
		_ = flags.Parse(subArgs)
		runServer(host, dataDir, port, logAccess)

	default:
		printUsage()
		os.Exit(1)
	}
}

func isFlag(arg string) bool {
	return len(arg) > 0 && arg[0] == '-'
}

func runCLI(dataDir string, fn func(*cliHandler.CLI) error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		slog.Error("Failed to create data directory", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(filepath.Join(dataDir, "finorama.db"))
	if err != nil {
		slog.Error("Failed to open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, dataDir)
	cli := cliHandler.New(st, v, syncWorker, dataDir)

	if err := fn(cli); err != nil {
		slog.Error("Execution failed", "err", err)
		os.Exit(1)
	}
}

func runServer(host string, dataDir string, port int, logAccess bool) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		slog.Error("Failed to create data directory", "err", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(dataDir, "finorama.db")
	st, err := store.Open(dbPath)
	if err != nil {
		slog.Error("Failed to open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, dataDir)
	syncWorker.StartCron()
	defer syncWorker.Stop()

	srv, err := server.NewWithConfig(st, v, syncWorker, templatesFS, logAccess)
	if err != nil {
		slog.Error("Failed to init server", "err", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("Starting Finorama server", "url", fmt.Sprintf("http://%s", addr), "data_dir", dataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("Shutting down Finorama server gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "err", err)
	}
}
