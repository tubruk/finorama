package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	cliHandler "github.com/chickenzord/finorama/internal/cli"
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
  -data-dir string    Directory for SQLite DB and cache (default "./data")
  -port int           HTTP port for web server (default 8080)
`)
}

func main() {
	var dataDir string
	var port int

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
	flags.StringVar(&dataDir, "data-dir", "./data", "Directory to store sqlite DB and sessions")
	flags.IntVar(&port, "port", 8080, "HTTP server port")

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
		runServer(dataDir, port)

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
		log.Fatalf("Failed to create data directory: %v", err)
	}

	st, err := store.Open(filepath.Join(dataDir, "finorama.db"))
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, dataDir)
	cli := cliHandler.New(st, v, syncWorker, dataDir)

	if err := fn(cli); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func runServer(dataDir string, port int) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	dbPath := filepath.Join(dataDir, "finorama.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer st.Close()

	v := vault.New()
	syncWorker := syncer.New(st, v, dataDir)
	syncWorker.StartCron()
	defer syncWorker.Stop()

	srv, err := server.NewWithTemplateFS(st, v, syncWorker, templatesFS)
	if err != nil {
		log.Fatalf("Failed to init server: %v", err)
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting Finorama on http://localhost%s (data: %s)", addr, dataDir)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
