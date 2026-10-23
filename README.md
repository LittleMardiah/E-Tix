# E-Tix Flash ⚡

**Smart, fair & scalp-proof ticketing platform.**

## The Story Behind This Project

> "I was at a concert with 3 friends. We booked tickets together, but at checkout, 1 seat was gone. We ended up sitting separately. It ruined the experience."

This project started from that frustration. Buying tickets should not feel like a lottery against bots, and a group booking should never scatter friends across the venue. E-Tix Flash is built around three promises: fair access, atomic group seating, and a gate experience that still works when the network does not.

## Why I Built This

1. **Bots and scalpers eat the best seats.** A normal queue is not enough when automated clients can hit the API thousands of times per second.
2. **Group checkout is fragile.** Without atomic seat locking, two members can lose the same seat while the group is still deciding.
3. **Flash sales break ordinary stacks.** Traffic spikes, payment retries, and offline gate devices expose every weak point in the booking lifecycle.

E-Tix Flash is my attempt to solve those problems end-to-end: from waiting room and seat locking to split-pay, controlled resale, dynamic QR, fallback PIN, and offline gate synchronization.

## What Makes This Different

| Capability | Why it matters |
|---|---|
| **Anti-bot ingress** | Two-stage sync + async pipeline absorbs traffic spikes without overselling. |
| **Atomic group locking** | Redis Lua/bitmask locking keeps a group's seats together in one atomic operation. |
| **Split-Pay** | Each member can pay their own share instead of one person fronting the whole booking. |
| **Dynamic QR + fallback PIN** | QR entry with a rotating payload, plus a PIN fallback when a scanner cannot read the code. |
| **Offline gate sync** | Scans are queued and reconciled later, with deterministic conflict resolution. |
| **Controlled resale** | Price cap and transparent fees reduce scalping in the secondary market. |
| **Ledger audit + 2FA** | Admin money movement and emergency actions are traceable. |

## Tech Stack

- **Backend:** Go (Gin), PostgreSQL, Redis (bitmask seat locking), RabbitMQ (delayed queue / async work)
- **Frontend:** Next.js 15 App Router, React 18, TypeScript, Tailwind CSS, Zustand, Axios, PWA
- **Testing:** Go unit + integration tests, Playwright E2E, k6 load-test plan
- **Infrastructure:** Docker Compose for local PostgreSQL, Redis, and RabbitMQ

## Live Demo

**Coming soon.** Production deployment is in progress; the repository currently targets local development and staging-style verification. See [docs/LOCAL_SETUP.md](docs/LOCAL_SETUP.md) for the local runbook.

## Project Structure

```text
.
├── backend/            # Go API, auth, booking, payment, gate, admin, resilience
├── frontend/           # Next.js app (landing, auth, customer, EO, admin, gate)
│   ├── app/
│   ├── components/ui/
│   ├── lib/
│   └── tests/e2e/      # Playwright specs
├── docs/               # API contract, schema, roadmap, rules, technical debt
├── docker/             # Container support files
└── docker-compose.yml  # Local PostgreSQL + Redis + RabbitMQ
```

## Getting Started

### Prerequisites

- Go 1.26+ (see `backend/go.mod`)
- Node.js 20+ and npm
- Docker Compose v2

### Backend

```bash
cd backend
./scripts/dev_setup.sh   # starts containers, runs migrations, seeds fixtures
go run ./cmd/api
```

Health probes:

- Liveness: `GET http://localhost:8080/healthz/liveness`
- Readiness: `GET http://localhost:8080/healthz/readiness`

### Frontend

```bash
cd frontend
cp .env.local.example .env.local
npm install
npm run dev
```

Open `http://localhost:3000`.

### Tests

```bash
cd frontend
npx tsc --noEmit
npm run lint
npx playwright test --list
```

Gate routes:

- `/gate/scanner` — QR scan / PIN fallback entry
- `/gate/sync` — offline batch synchronization

## What I Learned

- **Idempotency is a product feature.** Retries are inevitable; `X-Idempotency-Key` and stable `scan_id` values make them safe.
- **Atomicity needs a clear owner.** Redis locking is only trustworthy when the Lua script owns the whole compare-and-set decision.
- **Offline is a conflict-resolution problem.** A scan that arrives late still needs a deterministic winner: earliest `scanned_at`, then smallest `scan_id`.
- **UI state must be honest.** Loading, empty, error, and duplicate states are part of the flow, not decoration.
- **Docs drift fast.** The API contract and the code have to be verified together, especially around field names and enum values.

## Contributing

1. Read [docs/TEAM_CONTRACT.txt](docs/TEAM_CONTRACT.txt) and [docs/API%20CONTRACT.txt](docs/API%20CONTRACT.txt).
2. Keep changes scoped and verify with raw command output.
3. Run type-check, lint, and build before opening a pull request.
4. Never commit secrets or local `.env` files.

## License

No license file has been added yet. Until one is published, treat this repository as **all rights reserved**. Contact the maintainer before reuse or redistribution.
