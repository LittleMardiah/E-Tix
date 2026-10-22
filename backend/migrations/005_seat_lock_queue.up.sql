-- 005_seat_lock_queue.up.sql
-- P9a-12 TD-F9-17: durable FIFO queue of seat-lock requests parked while the
-- Redis circuit breaker is open (degraded mode).
--
-- LOGIC FLOW §9.4: "Redis down > 100ms -> Circuit Breaker OPEN, rate limit
-- softened, bitmask lock postponed, all requests go into the FIFO PG queue.
-- Redis recovers -> CB CLOSED -> process the FIFO queue."
--
-- The queue must be durable: a Redis outage must not lose a booking request
-- (ROADMAP FASE 9 B1-T13: "zero data loss"). The existing tables cannot hold
-- that payload safely:
--   * system_config / blacklist are world-readable (RLS SELECT true) -> the
--     queued payload would leak the buyer identity to every client.
--   * bookings rows are payable (payment_status='PENDING' creates tickets via
--     the check_booking_payment_complete trigger) -> parking a seat lock there
--     would let the booking be paid before the seats are actually locked.
--   * admin_actions.action_type is a fixed enum without a suitable value.
-- Hence this dedicated table.

CREATE TABLE IF NOT EXISTS seat_lock_queue (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Queue-token jti: the dedup key. The same token must never be parked
    -- twice (the user may retry with the same token while Redis is down).
    token_id        TEXT UNIQUE NOT NULL,
    event_id        UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    section_id      UUID NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
    leader_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    seat_positions  JSONB NOT NULL,
    total_seats     INTEGER NOT NULL CHECK (total_seats > 0),
    total_amount    NUMERIC(12,2) NOT NULL DEFAULT 0,
    payment_mode    TEXT NOT NULL CHECK (payment_mode IN ('SPLIT','BULK')),
    -- Seat-hold deadline of the parked request. A request that stayed in the
    -- queue longer than the hold TTL (180s) is stale and must not be
    -- replayed (the client has to re-queue).
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '180 seconds'),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- FIFO drain order.
CREATE INDEX IF NOT EXISTS idx_seat_lock_queue_created_at ON seat_lock_queue(created_at);

-- RLS: the queue is an internal worker table. Only the buyer (own row) and
-- admins may read it; writes go through the service role (RLS bypassed) and
-- are never exposed to end users.
ALTER TABLE seat_lock_queue ENABLE ROW LEVEL SECURITY;

CREATE POLICY "Users can view own queued seat locks" ON seat_lock_queue
    FOR SELECT USING (leader_user_id = auth.uid());

CREATE POLICY "Admins can view all queued seat locks" ON seat_lock_queue
    FOR SELECT USING ((auth.jwt() ->> 'role') IN ('admin', 'event_organizer'));
