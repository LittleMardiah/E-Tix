# Competitive Analysis

E-Tix Flash compared against the two dominant Indonesian ticketing platforms on the five capabilities that decide whether a flash sale is fair, group-friendly, and operationally safe.

> Competitor rows reflect public product behaviour assumed at authoring time, not a live teardown. Re-verify before any external publication. E-Tix Flash rows are backed by this repository.

## Feature Comparison

| Capability | E-Tix Flash | Tiket.com | Loket |
|---|---|---|---|
| **Anti-Bot Ingress** | Captcha + fan-score gating + signed queue token + per-IP rate limit + idempotency lock | Waiting room, no published fan-score gating | Waiting room, no published fan-score gating |
| **Custom Interactive Map** | SVG polygon zones over the organizer's own venue image, with zone POV, distance, and live quota | Fixed seat/section picker | Fixed seat/section picker |
| **Zoned Ticketing** | First-class `ZONED` seating mode: free-form areas, per-zone quota, atomic zone decrement | Category-based sections | Category-based sections |
| **Split-Pay** | Each group member pays their own obligation; tickets issue only when all shares settle | Single payer per order | Single payer per order |
| **Controlled Resale** | Official resale capped at +10% of original price, transparent 2.5% platform fee, automatic seller payout | Limited fan-to-fan resale, no published price cap | Limited fan-to-fan resale, no published price cap |

## Why E-Tix Flash for Mid-Size Event Organizers

For an organizer running 5,000–50,000 attendees, the expensive failures are not missing features — they are oversold seats, split friend groups, chargebacks from a single payer, and scalper-priced resale. E-Tix Flash addresses those four failures directly: ingress is gated before a seat is ever locked, the interactive map and `ZONED` mode let an organizer sell the venue they actually booked rather than a generic section grid, group seats lock atomically so a party either gets all its seats or none, Split-Pay removes the "one friend fronts the money" bottleneck, and resale stays inside a +10% cap with a published fee instead of becoming a second speculative market. Tiket.com and Loket win on reach and brand trust for national-scale consumer events; E-Tix Flash wins on the operational controls a mid-size organizer needs and cannot configure elsewhere.
