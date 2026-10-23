# Competitive Analysis

## Scope

This comparison focuses on ticketing capabilities that matter during a flash sale and at the gate: fairness, group booking, payment flexibility, entry validation, and resale control. Competitor features can change over time; this document reflects the product assumptions used while building E-Tix Flash.

## Feature Comparison

| Feature | Loket | Tiket.com | E-Tix Flash |
|---|---|---|---|
| Atomic group locking | ❌ | ❌ | ✅ |
| Split-Pay (bayar sendiri) | ❌ | ❌ | ✅ |
| Dynamic QR (anti-screenshot) | ❌ | ❌ | ✅ |
| Fallback PIN (offline) | ❌ | ❌ | ✅ |
| Controlled resale (+10% cap) | ❌ | ❌ | ✅ |
| Offline gate sync | ❌ | ❌ | ✅ |
| Admin ledger audit + 2FA | Partial | Partial | ✅ |
| Waiting room / anti-bot ingress | Partial | Partial | ✅ |

## Why Switch to E-Tix Flash?

### 1. Fairness during flash sales

Most platforms rely on a first-come queue and rate limiting. E-Tix Flash combines a waiting room, anti-bot ingress, and atomic seat locking so a burst of traffic does not turn into overselling or random seat loss.

### 2. Group booking that actually stays together

The platform locks a group's seats in one atomic operation. If any seat in the group cannot be locked, the whole group fails cleanly instead of leaving friends split across sections.

### 3. Payment that fits real friend groups

Split-Pay lets each member pay their own share. This removes the "one person pays, everyone promises to transfer later" problem that makes group purchases fragile.

### 4. A gate flow that survives bad connectivity

Dynamic QR reduces screenshot sharing, while fallback PIN and offline synchronization keep entry moving when the venue network is unstable. Late scans are reconciled deterministically rather than silently duplicated.

### 5. Resale without a scalper premium

Controlled resale caps the resale price (for example, +10%) and applies a transparent platform fee. The goal is to protect fans, not to create a second speculative market.

## Positioning

E-Tix Flash targets mid-to-large event organizers (5,000–50,000 attendees) who need flash-sale reliability, group-friendly checkout, and a dependable gate operation. It is intentionally opinionated: fairness and operational clarity over an unlimited feature checklist.
