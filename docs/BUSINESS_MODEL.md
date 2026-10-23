# Business Model

## Revenue Streams

| Stream | Rate | Who pays | Notes |
|---|---|---|---|
| Platform fee | **2.5%** per ticket sold | Event organizer | Charged on successful sales only. |
| Resale fee | **1%** of resale transaction | Seller / buyer flow | Applied when a controlled resale completes. |
| Enterprise plan | Custom | Large organizers | Dedicated infrastructure, white-label, and integration support. |

## Target Market

- Mid-to-large event organizers with **5,000–50,000 attendees**.
- Concerts, festivals, and sports events with high flash-sale intensity.
- Organizers who care about group seating integrity, gate throughput, and resale control.

## Pricing Logic

The free tier covers small events. The Pro tier uses a **2.5% success fee** because it aligns platform revenue with actual ticket sales and avoids charging organizers for traffic that never converts. Enterprise pricing is custom because infrastructure, support, and compliance requirements vary significantly.

## Illustrative Break-Even Analysis

Assumptions:

- 10,000 tickets sold at **Rp 1,000,000** each.
- Platform fee: 2.5% of gross ticket revenue.
- Event-scoped infrastructure and operations budget: **~Rp 1,000,000** for this illustrative case.

Calculation:

```text
Gross ticket revenue = 10,000 × Rp 1,000,000
                     = Rp 10,000,000,000

Platform revenue     = 2.5% × Rp 10,000,000,000
                     = Rp 250,000,000

Illustrative infra   = Rp 1,000,000
Contribution margin  = Rp 249,000,000
```

At these assumptions the contribution margin is above 99%. This is a **portfolio-level illustration**, not a financial forecast: real infrastructure, payment-gateway, support, and marketing costs will vary by event and deployment model.

## Unit Economics Sensitivity

| Scenario | Tickets | Avg. price | Platform revenue (2.5%) |
|---|---:|---:|---:|
| Small event | 2,000 | Rp 500,000 | Rp 25,000,000 |
| Mid-size event | 10,000 | Rp 1,000,000 | Rp 250,000,000 |
| Large event | 30,000 | Rp 1,500,000 | Rp 1,125,000,000 |

## Cost Drivers

- Payment-gateway fees and refunds.
- Redis, PostgreSQL, RabbitMQ, and observability infrastructure.
- On-site gate support and incident response.
- Customer support during flash sales.

## Go-to-Market

1. Start with organizers who already run high-demand events and feel the pain of overselling.
2. Offer a paid pilot for a single event with a clear success metric: zero oversell and gate throughput under load.
3. Expand to multi-event contracts with analytics, controlled resale, and enterprise support.
