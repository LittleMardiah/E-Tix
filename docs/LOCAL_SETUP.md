# LOCAL SETUP — E-Tix Flash

Panduan menjalankan proyek E-Tix Flash di mesin lokal.

> Status: skeleton. Konten added bertahap per tahap FASE.
> Referensi: docs/TECHNICAL DEBT.txt TD-F0-06 (bootstrap script dev).

## 1. Prasyarat

| Tool | Versi | Catatan |
|------|-------|---------|
| Go | 1.26.10 | `backend/go.mod` → `go 1.26.10` (source of truth) |
| Docker | Compose v2 | untuk PostgreSQL + Redis + RabbitMQ |
| PostgreSQL | 17+ | container `etix-postgres` (port 15433) |
| Redis | 7+ | container `etix-redis` (port 16379) |
| RabbitMQ | 3.13+ | container `etix-rabbitmq` (port 5673) |

## 2. Bootstrap

```bash
cd backend
./scripts/dev_setup.sh          # docker compose up + migrate up + seed fixtures
```

Script bersifat idempotent — aman dijalankan ulang. Opsi:

- `--no-docker` — lewati start container (pakai infra yang sudah jalan).
- `DATABASE_URL` — override target database.

## 3. Menjalankan service

```bash
cd backend
go run ./cmd/api
```

Health check: `GET /health`, readiness: `GET /ready`.

## 4. Menjalankan test

```bash
cd backend
go test ./internal/... -cover -count=1                              # unit only
TEST_DB_URL="postgresql://postgres:postgres@localhost:15433/etix_flash_dev?sslmode=disable" \
  go test -p 1 -tags integration ./internal/... -cover -count=1     # integration (serial)
```

## 5. Scan keamanan lokal — govulncheck (FIX P9a-9b TD-F9-12)

`govulncheck` yang sudah terpasang tapi dibangun dengan toolchain lama
(mis. v1.1.4 built with go1.22) akan gagal memindai `backend/` dengan pesan:

```
internal error: error "package requires newer Go version go1.26" without position
```

Scanner versi lama tidak bisa membaca paket yang butuh Go >= 1.26
(`backend/go.mod` sekarang `go 1.26.10`).

Upgrade scanner:

```bash
GOTOOLCHAIN=go1.26.10 go install golang.org/x/vuln/cmd/govulncheck@latest
```

Verify:

```bash
cd backend
govulncheck ./...    # harus: "No vulnerabilities found" untuk codebase saat ini
```

CI tidak terpengaruh — GitHub Actions memasang `govulncheck` versi fresh
pada setiap run (`.github/workflows/test.yml`, job `security-scan`:
`go install golang.org/x/vuln/cmd/govulncheck@latest`).

## 6. Referensi

- docs/TECHNICAL DEBT.txt — TD register (TD-F0-06, TD-F9-12)
- docs/AUDIT FASE 0.md — hasil audit FASE 0
- docs/TEAM_CONTRACT.txt — aturan kerja tim
