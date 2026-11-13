# Business Model

E-Tix Flash earns only when an organizer sells. There is no listing fee, no subscription required to publish an event, and no charge for traffic that never converts.

## Revenue Streams

| Stream | Rate | Charged on | Who pays |
|---|---|---|---|
| **Primary platform fee** | **2.5%** of the ticket price | Successful ticket sale only | Event organizer |
| **Resale platform fee** | **2.5%** of the resale price | Completed controlled-resale transaction | Deducted from the seller's payout |

Both rates are configuration, not hard-coded constants: the resale rate comes from `RESALE_PLATFORM_FEE_RATE` (default `0.025`), and the resale price cap comes from `system_config.resale_price_cap_percent` (default `10`, i.e. +10% over the original price).

## Money Flow

```text
Buyer
  |  (1) pays via payment gateway
  v
Payment Gateway
  |  (2) settles gross amount to platform account
  v
Platform ledger
  |  (3) deducts platform fee  (2.5% primary / 2.5% resale)
  |  (4) records seller payout (resale_price - platform_fee)
  v
Organizer (primary payout)  /  Seller (resale payout)
```

1. **Checkout.** The buyer pays a single obligation, or their own share when the booking uses Split-Pay. No ticket is issued for a Split-Pay booking until every member's obligation is confirmed.
2. **Gateway settlement.** The payment gateway settles the gross amount to the platform account and fires a webhook; the booking is reconciled against that webhook, not against the browser.
3. **Fee deduction.** The platform fee is taken from the settled amount and written to the ledger.
4. **Payout.** The remainder is paid out to the organizer, or to the reseller as `resale_price - platform_fee` on a completed resale.

Example, a Rp 1,000,000 ticket:

```text
Buyer pays                 Rp 1,000,000.00
Platform fee (2.5%)        Rp    25,000.00
Organizer payout           Rp   975,000.00
```

Example, a Rp 1,100,000 resale against a Rp 1,000,000 original (+10% cap):

```text
Buyer pays                 Rp 1,100,000.00
Platform fee (2.5%)        Rp    27,500.00
Seller payout              Rp 1,072,500.00
```

## Cost Structure

Managed services only — no self-hosted cluster to operate.

| Component | Service | Role | Cost behaviour |
|---|---|---|---|
| Backend (Go monolith) | **Render** | API, worker, webhook receiver | Fixed instance size; scales with replica count |
| PostgreSQL | **Supabase** | Source of truth, daily backups, PITR | Scales with storage and compute tier |
| Redis | **Upstash** | Seat locks, zone quota, rate limits, idempotency, token blacklist | Per-command pricing; near-zero when idle |
| RabbitMQ | **CloudAMQP** | Async jobs: split-pay expiry, resale expiry, reconciliation, reports | Plan-based message and connection limits |
| Frontend | **Cloudflare Workers** (OpenNext) | Next.js PWA, edge-cached assets | Free/low tier for portfolio traffic |

Fee revenue is transaction-linked while most infrastructure is plan-linked, so margin improves with ticket volume on the same event footprint. The dominant variable costs are payment-gateway fees, refund handling, and on-site gate support during a flash sale.
