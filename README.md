# E-Tix Flash

Platform ticketing flash-sale untuk event berskala besar. Sistem ini dirancang untuk
tiga masalah utama ticketing: lonjakan trafik saat flash sale, aksi calo/bot, dan
race condition saat pemilihan kursi.

Selain alur ticketing standar (auth, seat locking, pembayaran, QR check-in, dan
resale terkontrol), E-Tix Flash menyediakan **custom interactive map** (F007): EO
mengunggah denah sendiri dan pengunjung memilih area di gambar, baik untuk
*assigned seating* (kursi bernomor) maupun *zoned / general admission*.

## Tech Stack

- **Backend:** Go 1.26 (Gin), pgx v5, golang-migrate, Zap, Prometheus
- **Frontend:** Next.js 15 (App Router), TypeScript, Tailwind CSS, Zustand, Zod
- **Database:** PostgreSQL 15
- **Cache / Lock:** Redis 7 (Lua script, bitmask, atomic counter)
- **Message Broker:** RabbitMQ (worker asynchronous)
- **Testing:** Playwright (E2E), `go test` (unit + integration)
- **Infrastruktur:** Docker Compose, GitHub Actions

## Features

- **F001 — Behavioral Ingress:** antrean flash sale dengan Fan Score, Priority Tier, dan CAPTCHA.
- **F002 — Bitmask Multi-Seat Atomic Locking:** lock N kursi sekaligus via Redis Lua script, tanpa race condition.
- **F003 — Group Split-Pay Engine:** pembayaran kolektif mode SPLIT (patungan) dan BULK (satu pembayar), plus bulk assign tiket ke anggota.
- **F004 — Dynamic HMAC QR + Offline Fallback PIN:** QR berotasi dan PIN statis untuk gate saat offline.
- **F004b — Gate Analytics:** laporan scan dan post-event report.
- **F005 — Controlled Secondary Resale:** pasar sekunder dengan batas harga dan payout terkontrol (anti-scalper).
- **F006 — Admin Emergency & Reconciliation Tools:** bulk cancel, force release, user management, dan audit ledger (mutasi diproteksi 2FA).
- **Fase 10 — Custom Map & Dual-Mode Ticketing (F007):** denah custom (SVG polygon), mode ZONED (kuota per zona) dan ASSIGNED (kursi spesifik).

## Prerequisites

- Docker + Docker Compose
- Go 1.26+ (mengikuti `backend/go.mod`)
- Node.js 20+ dan npm

Catatan: port host Docker Compose di-offset agar tidak bentrok dengan service lokal
lain — PostgreSQL `15433`, Redis `16379`, RabbitMQ `5673` (AMQP) dan `15673` (UI).

## How to Run

**1. Nyalakan infrastruktur**

```bash
docker compose up -d
```

**2. Migrasi database + seed data**

```bash
cd backend
bash scripts/dev_setup.sh --no-docker
```

**3. Jalankan backend** (default `http://localhost:8080`)

```bash
cd backend
set -a && source .env.local && set +a
go run ./cmd/api/main.go
```

**4. Jalankan frontend** (`http://localhost:3000`)

```bash
cd frontend
npm install
npm run dev
```

## Testing

**Unit & integration (backend)**

```bash
cd backend && go test ./...
```

**E2E (Playwright)** — butuh backend, frontend, dan database yang sudah di-seed
berjalan lebih dulu:

```bash
cd frontend
npx playwright install chromium
npx playwright test
```

Suite E2E berisi 7 journey (auth, assigned seat, resale, gate, admin, EO map,
zoned checkout) dan dijalankan otomatis di GitHub Actions (`.github/workflows/e2e.yml`).

## Architecture

```
Client (Next.js PWA)
        |
        v
  Go API (Gin)  --->  PostgreSQL   (data transaksional, source of truth)
        |        --->  Redis        (seat lock, kuota, cache, rate limit)
        |        --->  RabbitMQ     (job async: split-pay, expiry, resale, report)
        v
  Payment Gateway (webhook -> reconciliation worker)
```

Alur singkat: client memanggil API Go; operasi yang butuh kecepatan tinggi
(lock kursi, potong kuota zona) dieksekusi atomik di Redis lebih dulu, lalu
hasilnya dipersist ke PostgreSQL. Pekerjaan yang tidak perlu ditunggu client
(pembayaran, expiry, notifikasi, laporan) dilempar ke RabbitMQ dan diproses worker.
