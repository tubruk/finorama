# Finorama

Self-hosted web-based personal finance & portfolio overview for **KSEI** (Kustodian Sentral Efek Indonesia), written in Go.

Inspired by [ksei-exporter](https://github.com/chickenzord/ksei-exporter) and powered by [goksei](https://github.com/chickenzord/goksei).

## Development

### Quick Start (Dev Mode)

Run both Go backend (port 8080) and Vite frontend (port 5173 with HMR) with a single command:

```bash
make dev
```

Then open `http://localhost:5173`. Vite automatically proxies `/api` calls to `:8080`.

Or run them individually:
```bash
# Terminal 1: Backend
make dev-backend

# Terminal 2: Frontend
make dev-ui
```

### Production Build

```bash
# Builds UI assets into web/dist and compiles standalone Go binary
make build

# Run single self-contained binary
./bin/finorama serve
```

- **Multi-Account Support**: Manage multiple AKSES KSEI accounts in one unified dashboard.
- **Master Passphrase Vault**: Zero credentials stored in plaintext. Passwords encrypted in local SQLite using AES-256-GCM derived via Argon2id. Passphrase kept in-memory only while unlocked.
- **Pure Go SQLite**: Single-binary deployment with modernc pure Go SQLite engine and WAL mode enabled.
- **Automated Daily Capture**: Scheduled background syncer (default: 01:00 AM after KSEI midnight data updates) + manual trigger button via HTMX.
- **Snapshot & Breakdown**: Aggregated portfolio totals, asset allocation (Equity, Mutual Funds, Bonds, RDN Cash), and historical valuation timeline.

## CLI Usage

Finorama includes interactive CLI commands alongside the web dashboard:

```bash
# Connect and test KSEI credentials interactively
./finorama account add

# View latest holding balances (interactive picker or pass account ID)
./finorama holdings
./finorama holdings 1

# Manually trigger capture and sync
./finorama sync
./finorama sync 1

# Run web dashboard
./finorama serve -port 8080 -data-dir ./data
```

Then visit `http://localhost:8080`:
1. On first run, configure your master passphrase.
2. Go to **Accounts** and connect your AKSES KSEI credentials.
3. Click **Trigger Sync Now** to fetch the latest holdings and portfolio valuation.
