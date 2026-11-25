# Finorama

[![CI](https://github.com/tubruk/finorama/actions/workflows/ci.yml/badge.svg)](https://github.com/tubruk/finorama/actions)
[![Docker](https://img.shields.io/badge/docker-ghcr.io%2Ftubruk%2Ffinorama-blue?logo=docker)](https://github.com/tubruk/finorama/pkgs/container/finorama)
[![Go Report Card](https://goreportcard.com/badge/github.com/tubruk/finorama)](https://goreportcard.com/report/github.com/tubruk/finorama)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Self-hosted web dashboard and personal portfolio overview for **AKSES KSEI** (Kustodian Sentral Efek Indonesia).

Finorama aggregates Indonesian investment holdings (stocks, mutual funds, bonds, and RDN cash balances) across multiple accounts in a unified interface, with zero plain credentials stored on disk.

Inspired by [ksei-exporter](https://github.com/chickenzord/ksei-exporter) and powered by [goksei](https://github.com/chickenzord/goksei).

---

## Features

- **Multi-Account Aggregation**: View multiple KSEI accounts and consolidated balances in one place.
- **Client-Side Encryption Vault**: Passwords encrypted with AES-256-GCM (Argon2id key derivation). Master passphrase resides strictly in RAM while unlocked.
- **Embedded SQLite (Pure Go)**: Standalone single-binary engine using modernc pure Go SQLite in WAL mode.
- **Automated Nightly Sync**: Scheduled background sync (01:00 AM post-KSEI settlement) with manual trigger option.
- **Modern Responsive UI**: React + Tailwind CSS dashboard with historical valuation timeline chart.

---

## Quick Start (Docker)

Run with Docker using GitHub Container Registry (`ghcr.io`):

```bash
docker run -d \
  --name finorama \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  ghcr.io/tubruk/finorama:latest
```

Open `http://localhost:8080` in your browser:
1. Configure your master passphrase on first start.
2. Add your AKSES KSEI accounts.
3. Trigger sync to fetch holdings and portfolio valuation.

### Docker Compose

```yaml
version: "3.8"

services:
  finorama:
    image: ghcr.io/tubruk/finorama:latest
    container_name: finorama
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - TZ=Asia/Jakarta
```

---

## Local Development

```bash
# Run backend (:8080) and frontend (:5173) concurrently with hot reload
make dev

# Or build standalone release binary (bundles frontend inside Go binary)
make build
./bin/finorama serve -port 8080 -data-dir ./data
```
