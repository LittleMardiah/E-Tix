# ⚡ E-Tix Flash: Smart, Fair & Scalp-Proof Ticketing Platform

> **"Jangan hanya berpikir bahwa Sistem akhirnya bisa berjalan dengan Normal, tapi berpikirlah apakah sistem juga bisa berjalan dengan Normal di Situasi yang sedang 'Tidak Normal'."** — *Vibe Coder Principle*

E-Tix Flash adalah platform ticketing *high-concurrency* yang dibangun untuk menyelesaikan 3 masalah utama di industri tiket:
1. **Server Crash** saat *flash sale* (dirancang untuk menahan 20,000 RPS).
2. **Calo/Bot** yang memborong tiket dalam hitungan detik.
3. **Grup Gagal Duduk Bareng** karena *race condition* saat pemilihan kursi.

## 🌐 Live Demo & Architecture

- **Frontend (Edge):** [https://e-tix-flash.m-arif250602.workers.dev](https://e-tix-flash.m-arif250602.workers.dev)
- **Deployment:** Frontend dideploy di **Cloudflare Workers (via OpenNext)**. Backend (Go), PostgreSQL, Redis, dan RabbitMQ di-*self-host* secara lokal dan diekspos via **Ngrok Tunnel**. *(Catatan: Demo hanya online selama laptop developer menyala).*

## 🛠️ Tech Stack
- **Backend:** Golang (Gin), PostgreSQL (Supabase/Pgx), Redis (Upstash/Mini), RabbitMQ (CloudAMQP).
- **Frontend:** Next.js 15 (App Router), Tailwind CSS, Zustand, React Hook Form + Zod.
- **Infrastructure:** Docker Compose, Cloudflare Workers, Ngrok, GitHub Actions.

## 🚀 Core Features (F001 - F006)
- 🛡️ **Behavioral Ingress:** Sistem antrean dengan *Priority Tier* (Fan Score) dan proteksi *CAPTCHA*.
- 🔒 **Atomic Group Locking:** Mengunci N kursi secara *atomic* via **Redis Lua Scripts** (Zero Race Condition).
- 💳 **Hybrid Payment:** Mendukung *Split-Pay* (patungan) dan *Bulk Pay* (satu orang bayar semua).
- 🎟️ **Dynamic QR + PIN:** QR Code berubah tiap 30 detik + *Fallback PIN* untuk *offline mode*.
- 🔄 **Controlled Resale:** Pasar sekunder dengan batas maksimal +10% dari harga asli.
- 👨‍💼 **Admin Emergency Tools:** Pembatalan massal (Bulk Cancel) dan *Force Release* dengan 2FA.

## 🧠 Technical Challenges Solved (Deep Dive)
Ini adalah masalah *engineering* nyata yang diselesaikan selama pengembangan:

1. **PostgreSQL FK Deadlock on Payment:**
   * *Problem:* Terjadi `statement timeout` (13s) saat transaksi pembayaran karena FK `payment_obligations` memblokir row `bookings` yang sedang di-`FOR UPDATE`.
   * *Solution:* Memisahkan pemanggilan *Payment Gateway* ke luar transaksi dan menggunakan `CreateTx` (menggunakan koneksi transaksi yang sama) untuk menghindari *KEY SHARE lock*.
2. **Redis Bitmask Locking:**
   * Menggunakan `SETBIT` dan `GETBIT` via Lua Script untuk mengecek dan mengunci 10.000+ kursi dalam hitungan milidetik tanpa membebani Database.
3. **Resilience (Circuit Breaker):**
   * Jika Redis mati, sistem otomatis masuk ke *Degraded Mode* (Queue berbasis Database) dan pull kembali datanya setelah Redis hidup menggunakan `DBQueueSyncWorker`.
4. **Cloudflare Workers (OpenNext):**
   * Menghindari *vendor lock-in* Vercel dengan melakukan *build* Next.js App Router ke runtime Edge Cloudflare via adapter `@opennextjs/cloudflare`.

## 🚦 Local Setup

### 1. Prasyarat
- Go 1.26+
- Node.js 22+
- Docker (untuk PostgreSQL, Redis, RabbitMQ)

### 2. Jalankan Infrastruktur (Docker)
```bash
docker compose up -d
```

### 3. Jalankan Backend
```bash
cd backend
cp .env.example .env.local
# Sesuaikan environment variable di .env.local
go run ./cmd/api/main.go
```

### 4. Jalankan Frontend
```bash
cd frontend
npm install
npm run dev
```

## 🧪 Testing
Backend: 84.23% Coverage (Unit + Integration). Menggunakan testcontainers-go untuk database nyata.

E2E: Playwright (9 Journeys: Auth, Customer, Admin, Gate, Resilience, dll).

Load Test: K6 Script tersedia di backend/tests/performance/.

Dibuat dengan ❤️ dan ☕ oleh M. Arif Aulia.
