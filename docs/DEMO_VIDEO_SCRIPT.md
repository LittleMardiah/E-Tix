# Demo Video Script — E-Tix Flash

**Target duration:** 6–7 minutes
**Format:** 1080p, browser 100%, mobile viewport for gate scenes
**Scope:** Only flows that exist in this repository. No cloud deployment is assumed.

---

## Pre-Recording Checklist

1. Start dependencies and seed data:

   ```bash
   cd backend
   ./scripts/dev_setup.sh
   go run ./cmd/api
   ```

2. Start the frontend:

   ```bash
   cd frontend
   npm run dev
   ```

3. Open `http://localhost:3000`.
4. Keep the backend terminal visible for the OTP scene. With `ENV=development`,
   the dev OTP fallback logs the code in plaintext. In production it is redacted.
5. Prepare two browser profiles if you want to show split-pay from two users:
   one leader and one member.
6. Optional: run `npm run build && npm start` if you want to record the
   production PWA service worker instead of the dev server (PWA is disabled in
   development mode).

> Honesty note: this repo has the offline gate **sync API** and a demo sync
> screen, plus PWA page caching. It does **not** yet have an IndexedDB queue for
> offline scans. Do not claim otherwise in the narration.

---

## Scene 1 — Intro and Problem (0:00–0:35)

**Visual**
- E-Tix Flash landing page (`/`).
- Slowly scroll through the three problem cards and the feature section.

**Narration**
> "E-Tix Flash is a smart ticketing platform built around three problems:
> bots and scalpers, group bookings that lose seats at checkout, and gate
> operations that break when the network is unstable.
>
> The stack is Go, PostgreSQL, Redis, RabbitMQ, and Next.js. In the next few
> minutes I will show the complete customer, gate, admin, and organizer flows."

---

## Scene 2 — Registration and OTP (0:35–1:20)

**Actions**
1. Click **Sign Up** on the landing page.
2. Fill **Full Name**, **Email**, **Phone**, **Password**.
3. Click **Sign Up**.
4. On `/auth/verify-otp`, enter the 6-digit code.
5. Show the backend terminal with the dev OTP log line.

**Narration**
> "Registration creates the account and sends an OTP. In development the OTP
> fallback logs the code so local testing works. In production the code field
> is redacted and a real SMS or email provider is required.
>
> The verify step issues a queue token, not just a session. That token is what
> the seat-lock endpoint requires, so the waiting room cannot be skipped."

**On-screen text**
- `POST /auth/register`
- `POST /auth/verify-otp`
- Production-safe OTP logging

---

## Scene 3 — Customer Flow: Queue, Seats, Split-Pay (1:20–3:15)

**Actions**
1. Open `/customer/dashboard`. Show the event card list.
2. Open the seeded event (`Test Concert` if using `backend/tests/fixtures/seed.sql`).
3. Choose a section.
4. On `/customer/waiting-room/[eventId]`, show:
   - queue position,
   - tier and loyalty score,
   - CAPTCHA challenge when required.
5. Verify the CAPTCHA (`test_pass` with the mock service in `TEST_MODE=true`).
6. On the seat map, select 3–4 adjacent seats.
7. Click **Lock & Continue**.
8. On the payment page, choose **Split Pay**.
9. Fill each member's user ID (the leader's seat is pre-filled).
10. Click **Konfirmasi Pembayaran**.
11. Show the result screen with one payment obligation per seat and the payment
    links returned by the backend.
12. Open **Lihat Tiket Saya** and show the issued tickets and fallback PIN.

**Narration**
> "The waiting room returns a tier and a queue token. When the fan score is
> below the challenge threshold, the CAPTCHA step is required.
>
> Seat selection sends one atomic lock request. The backend locks the whole
> group in Redis, so the group either gets all seats or none of them. If another
> buyer wins the race, the UI clears the selection and reloads the seat map.
>
> Split-pay creates a separate payment obligation per seat. Each member can pay
> independently, and tickets are issued only after all obligations settle."

**On-screen text**
- Atomic group lock
- `X-Queue-Token` required
- Split-pay obligations
- Fallback PIN shown on My Tickets

---

## Scene 4 — Gate Scanner and PWA (3:15–4:20)

**Actions**
1. Open `/gate/scanner` in a mobile viewport.
2. Show the **QR Scan** and **PIN Entry** modes.
3. Enter a ticket ID in QR mode and submit. Show the validation response.
4. Switch to **PIN Entry** and submit a fallback PIN.
5. Open `/gate/sync`. Click **Sync Now** and show the sync result.
6. Open DevTools → Application → Service Workers. Show the registered worker.
7. Go offline, reload `/gate/scanner`, and show the cached page or the
   `/~offline` fallback.

**Narration**
> "Gate staff can validate with a QR payload or a fallback PIN. Every scan uses
> a stable idempotency key, so retries do not double-count.
>
> The sync screen demonstrates the offline sync endpoint. Important: the local
> scan queue is not implemented yet; this screen sends one sample scan. What is
> implemented is the PWA shell: the service worker caches pages and serves an
> offline fallback, so the scanner route is installable and survives a reload
> without network."

**On-screen text**
- QR + PIN validation
- Idempotent scan IDs
- PWA installable
- Offline fallback page
- Demo sync only (no IndexedDB queue yet)

---

## Scene 5 — Admin Console (4:20–5:20)

**Actions**
1. Open `/admin/ledger`. Enter a 2FA token and click **Muat Ulang**.
2. Show ledger entries and the summary.
3. Open `/admin/users`. Show search/filter and the suspend action.
4. Open `/admin/blacklist`. Add a short-lived test entry, then remove it.
5. Open `/admin/force-release`. Show stuck locks and the release action.
6. Open `/admin/emergency/[eventId]`. Show the bulk-cancel confirmation dialog
   without confirming a destructive action unless using a disposable event.

**Narration**
> "Admin money movement is protected by a 2FA header and idempotency keys.
> The ledger, user management, blacklist, force-release, and emergency
> bulk-cancel tools all call the same audited backend."

**On-screen text**
- 2FA-gated admin actions
- Ledger audit trail
- Force release and bulk cancel

---

## Scene 6 — Event Organizer (5:20–5:55)

**Actions**
1. Open `/eo/dashboard/[eventId]`.
2. Show sales, gate status, anti-bot metrics, and recent activity.
3. Open `/eo/analytics/[eventId]`.
4. Open `/eo/post-event/[eventId]` and click **Download CSV**.

**Narration**
> "The organizer dashboard shows live sales and gate status. The analytics and
> post-event pages turn scans and attendance into a report that can be exported
> as CSV."

---

## Scene 7 — Engineering Proof (5:55–6:40)

**Visual**
- Show `README.md` architecture and tech stack.
- Cut to terminal:

  ```bash
  cd backend && go build ./... && go test ./internal/... -cover -count=1
  cd ../frontend && npx tsc --noEmit && npm run lint && npm run build
  ```

- Show the final numbers from the latest audit.

**Narration**
> "The backend builds and its unit suite passes. The frontend type-checks,
> lints, and builds. The PWA build emits a service worker with an offline
> fallback. The remaining gaps are documented honestly in the technical debt
> register: load testing, security scanning, chaos drills, and the last few
> screens such as resale and bulk assignment."

**On-screen text**
- `go build` PASS
- `go test ./internal/...` PASS
- `tsc`, `lint`, `build` PASS
- Remaining gaps tracked in `docs/TECHNICAL_DEBT.txt`

---

## Scene 8 — Closing (6:40–7:00)

**Visual**
- Landing page, then a final card with repository and documentation links.

**Narration**
> "E-Tix Flash is a local-first, production-shaped ticketing system: fair
> queueing, atomic group locks, split-pay, auditable admin tools, and a gate
> app that keeps working when the page is cached. The code and the audit are in
> the repository. Thanks for watching."

---

## Claims to Avoid

Do not say any of the following unless the missing feature is implemented
before recording:

- "All 24 screens are complete." Current state: 18 fully implemented, 2
  partial, 4 missing (see `docs/AUDIT FASE 9.md`).
- "Scans are stored in IndexedDB and synced automatically." The current sync
  screen sends one sample scan.
- "Deployed to production." Epic E is intentionally deferred.
- "Coverage is above 85%." The latest measured backend unit total is 83.40%.
- "E2E, load, security, chaos, and accessibility suites passed." They are
  present or planned, but were not executed in the final polish session.
