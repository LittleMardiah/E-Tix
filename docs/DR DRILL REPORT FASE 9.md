# DR DRILL REPORT — FASE 9 (Epic B)

| Field | Value |
|---|---|
| Report ID | DR-DRILL-F9-01 |
| Tanggal | 2026-10-22 |
| Sesi | P9a-11 |
| Environment | Local (Docker Compose) — dev/staging-lokal |
| Verdict | **PASS** — 16/16 check hijau, exit code 0 |
| Artefak | `backend/scripts/dr_drill.sh`, `backend/tests/chaos/dr_drill_test.go` |
| Raw log | `backend/logs/dr_drill_final.log` (gitignored) |
| Gap | Epic B PARTIAL — 3/9 playbook DRP → `TD-F9-17` |

---

## 1. Ringkasan Eksekusi

Playbook #1 (Postgres), #2 (Redis), dan #3 (RabbitMQ) dari `docs/DRP.txt` §5.2-§5.4
dieksekusi otomatis di environment lokal dengan **fault injection nyata** (container
dimatikan / schema di-drop), bukan simulasi. Setiap probe memanggil **kode aplikasi
yang sama** dengan yang dipakai runtime (`internal/redis.Client.Health`,
`internal/queue.Connection.Health`, `pgxpool`), sehingga hasil drill relevan dengan
perilaku produksi.

**16 check lulus, 0 gagal, exit code 0.** RTO terukur jauh di bawah target DRP.

## 2. Environment

| Komponen | Container | Port host | Peran |
|---|---|---|---|
| PostgreSQL 16 | `etix-postgres` | 15433 | target corruption + backup/restore |
| Redis 7 | `etix-redis` | 16379 | target total failure (PB#2) |
| RabbitMQ 3 | `etix-rabbitmq` | 5673 | target broker failure (PB#3) |

Drill memakai **database drill terpisah** `etix_flash_dr_<timestamp>` (dibuat, lalu
di-`DROP` di akhir run). Database dev `etix_flash_dev` **tidak pernah disentuh** —
dicek di akhir run: hanya `etix_flash_dev` yang tersisa.

## 3. Metodologi

- **Orchestrator**: `backend/scripts/dr_drill.sh` (bash, `set -euo pipefail`).
  - Fault injection: `docker stop` / `docker start` (bukan `pause`, agar TCP refused
    benar-benar terjadi dan readiness probe bisa diuji).
  - Backup: `pg_dump -Fc` di dalam container; restore: `pg_restore --clean --if-exists`.
  - Corruption: `DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS auth CASCADE; CREATE SCHEMA public;`
  - Snapshot JSON pra-korupsi: `captured_at`, daftar 12 tabel kritis + jumlah baris,
    flag RLS per tabel.
  - Cleanup `trap` (EXIT/INT/TERM): container yang dimatikan selalu dinyalakan
    kembali, drill DB selalu di-drop, drill DB ephemeral (tidak pernah memakai DB dev).
- **Probes**: `backend/tests/chaos/dr_drill_test.go`, build tag `chaos` (di luar CI
  normal), di-gate env `DR_DRILL=1` + `DR_DRILL_PHASE=down|up`.
  - `run_probe` menolak false-green: wajib melihat baris `--- PASS` di output go test.
  - 12 tabel kritis yang dipantau: users, events, sections, bookings,
    payment_obligations, tickets, ticket_scans, resale_transactions, admin_actions,
    insights, blacklist, system_config.
- **Safety**: karena probe memakai env-gate + build tag, `go test ./...` biasa tidak
  pernah menyalakan chaos test.

## 4. Hasil per Playbook

### Playbook #2 — Redis Cluster Total Failure (DRP §5.3)

| Step | Hasil | Bukti |
|---|---|---|
| Fault injection | `docker stop etix-redis` | container `exited` |
| Probe `down` | PASS | `Health()` → `dial tcp 127.0.0.1:16379: connect: connection refused` |
| Degraded mode | PASS | readiness probe gagal (service degraded, DB tetap hidup) |
| Recovery | PASS | `docker start` → probe `up` PASS |
| RTO | **1s** | target DRP ≤ 300s |
| RPO | OK | marker Redis yang ditulis sebelum outage masih ada setelah recovery (state rebuildable) |

### Playbook #3 — RabbitMQ Failure (DRP §5.4)

| Step | Hasil | Bukti |
|---|---|---|
| Pre-flight plugin | PASS | `rabbitmq_delayed_message_exchange` enabled (DRP §5.4 Step 3) |
| Fault injection | `docker stop etix-rabbitmq` | container `exited` |
| Probe `down` | PASS | `Connection.Health()` → `dial tcp 127.0.0.1:5673: connect: connection refused` |
| Recovery | PASS | probe `up` PASS |
| RTO | **8s** | target DRP ≤ 600s |

### Playbook #1 — Postgres Primary Failure / Corruption (DRP §5.2)

| Step | Hasil | Bukti |
|---|---|---|
| Prepare | PASS | `migrate` + `seed` ke drill DB |
| Backup | PASS | `pg_dump` custom format |
| Snapshot | PASS | fingerprint JSON 12 tabel + row count + flag RLS |
| Corruption | PASS | `DROP SCHEMA public CASCADE` → probe mendeteksi **12 dari 12** tabel kritis hilang, 0 tabel tersisa |
| Restore | PASS | `pg_restore --clean --if-exists` → **RTO 1s** (target DRP ≤ 900s) |
| Integrity verify | PASS | 12 tabel hadir, **0 row-count mismatch**, **0 RLS flag mismatch** vs snapshot pra-korupsi |

## 5. Metrik RTO/RPO vs Target DRP

| Skenario | RTO terukur | Target DRP | Selisih | Status |
|---|---|---|---|---|
| Redis total failure (PB#2) | 1s | ≤ 300s | 299s | ✅ |
| RabbitMQ failure (PB#3) | 8s | ≤ 600s | 592s | ✅ |
| Postgres corruption + restore (PB#1) | 1s (restore) | ≤ 900s | 899s | ✅ |
| Total durasi drill (3 playbook) | ~40s | ≤ 900s (DRP §7.2) | — | ✅ |

RPO: tidak ada data hilang pada skenario yang diuji — Postgres restore diverifikasi
per-tabel (row count + flag RLS identik dengan snapshot), dan marker Redis tetap utuh
setelah outage.

## 6. Checklist DRP §7.3

```
  PASS  preflight/docker     docker daemon reachable
  PASS  preflight/redis      redis container healthy
  PASS  preflight/rabbitmq   rabbitmq container healthy
  PASS  preflight/plugin     rabbitmq_delayed_message_exchange enabled (Playbook #3 Step 3)
  PASS  PB2/down             readiness probe fails while redis is down (degraded mode)
  PASS  PB2/recovery         readiness probe healthy again (RTO 1s)
  PASS  PB2/state            redis state survived the outage (RPO: state is rebuildable)
  PASS  PB3/down             broker connection fails while rabbitmq is down (degraded path)
  PASS  PB3/recovery         broker connection healthy again (RTO 8s)
  PASS  PB1/migrate          migrations applied to etix_flash_dr_<ts> (Playbook #1 Step 3)
  PASS  PB1/seed             seed fixtures loaded into etix_flash_dr_<ts>
  PASS  PB1/backup           backup captured (pg_dump custom format)
  PASS  PB1/snapshot         pre-corruption fingerprint written to logs/dr_snapshot_<ts>.json
  PASS  PB1/detect           corruption detected (critical tables missing)
  PASS  PB1/restore          pg_restore completed in 1s (DRP target RTO <= 900s)
  PASS  PB1/verify           tables + row counts + RLS flags match the pre-corruption snapshot

  checks: 16   failures: 0

  Pre-Drill   : [x] safe environment prepared   [x] backup captured   [x] success criteria set
  During-Drill: [x] timers started              [x] playbook steps executed
  Post-Drill  : [x] recovery verified           [x] RTO measured      [x] findings written
```

Checklist §7.3 dihitung dari hasil run (bukan hardcoded): `[x]` = check yang
berjalan dan lulus, `[ ]` = ada check yang gagal, `[-]` = tidak berlaku untuk
playbook yang dipilih. Pada run gagal, script tetap mencetak summary lengkap
lalu exit non-zero.

## 7. Negative Control (anti false-green)

Setiap probe diuji terhadap kondisi yang seharusnya GAGAL, untuk memastikan PASS
bukan artefak environment:

| Negative control | Ekspektasi | Hasil |
|---|---|---|
| Redis hidup + `DR_DRILL_PHASE=down` | probe HARUS gagal | ✅ gagal (`redis healthy but expected unreachable`) |
| RabbitMQ hidup + `DR_DRILL_PHASE=down` | probe HARUS gagal | ✅ gagal |
| DB utuh + CorruptionDetected | probe HARUS gagal | ✅ gagal (tabel masih ada) |
| Baris dihapus sebelum restore | RestoreIntegrity HARUS gagal | ✅ gagal (`row count drift on "users" after the restore`) |

## 8. Temuan & Gap (tercatat sebagai TD)

| ID | Temuan | Status |
|---|---|---|
| TD-F9-16 | Pola race port-reuse (miniredis) yang sama masih ada di `internal/auth`, `internal/booking`, `internal/payment` → flaky risk saat full-suite paralel | DEFERRED |
| TD-F9-17 | Epic B residual: tidak ada app-level circuit breaker (hanya config), `internal/worker/db_queue_sync.go` masih stub 1 baris (DBQueueSyncWorker), tidak ada `redis_seats_rebuild.go` (anti Ghost Lock), PITR terenkripsi + Redis resync belum diuji, B1-T12 failover region Render.com + PB#4-#9 butuh akun SaaS live | DEFERRED |

**Status Epic B = PARTIAL (3/9 playbook).** Tidak ditandai DONE karena 6 playbook
sisanya dan B1-T12 memerlukan environment live (Render / Upstash / CloudAMQP /
Supabase) plus kredensial, dan 3 komponen yang disebut DoD DRP memang belum ada
di codebase.

## 9. Cara Menjalankan Ulang

```bash
cd backend
bash scripts/dr_drill.sh                 # semua playbook
bash scripts/dr_drill.sh --playbook 2    # hanya Playbook #2
# env: DR_DRILL_PHASE=down|up, DR_REDIS_ADDR, DR_PG_DSN, DR_SNAPSHOT_FILE
```

Exit code `0` = semua check hijau; `!= 0` = ada failure (script menjatuhkan
kesimpulan lewat trap cleanup, container dikembalikan ke kondisi semula).

## 10. Catatan Keamanan

- Backup drill memakai `pg_dump` custom format **di host lokal** dan hanya berisi
  data dev. Untuk produksi wajib PITR terenkripsi + backup off-site (DRP §5.2).
- Snapshot JSON berisi fingerprint skema/row-count (bukan data pengguna) dan ditulis
  ke `backend/logs/` yang sudah gitignored.
- `etix_flash_dev` tidak pernah menjadi target corruption/restore.
