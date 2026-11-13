-- ============================================================================
-- Seed data E2E / CI — E-Tix Flash
-- File:    backend/tests/fixtures/seed_ci.sql
-- Tujuan:  mengisi data minimal yang dibutuhkan 7 test Playwright di CI.
--          BUKAN production seed. Hanya untuk database E2E.
--
-- Semua ID sengaja fixed karena di-hardcode di frontend/e2e/*.spec.ts.
-- Password kedua user: password123
-- Hash bcrypt cost 12, sudah diverifikasi cocok (python3 bcrypt.checkpw -> True).
--
-- Catatan schema (backend/migrations/001_initial_schema.up.sql):
--   users    : phone, name, password_hash NOT NULL; phone punya CHECK format
--   events   : venue_name, venue_address, event_date, event_start_time NOT NULL
--   sections : total_seats, bitmask_key, layout_version NOT NULL
--   bookings : seat_positions, total_seats, total_amount, expires_at NOT NULL
--   tickets  : qr_secret_key, fallback_pin NOT NULL (fallback_pin UNIQUE)
--
-- Idempotent: ON CONFLICT (id) DO NOTHING, aman dijalankan berulang.
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ----------------------------------------------------------------------------
-- 1. USERS
--    1111... = customer/EO untuk journey 1, 2, 3, 6
--    2222... = admin untuk journey 5
-- ----------------------------------------------------------------------------
INSERT INTO users (id, email, phone, name, password_hash, role, status)
VALUES
    (
        '11111111-1111-1111-1111-111111111111',
        'm.arif250602@gmail.com',
        '+6281200000001',
        'M. Arif Aulia',
        '$2a$12$AiSz71ZSbWzUrspAsiuulOQ3URBfMNf2nWGoe4A7NlGhQUhN3rz8S',
        'event_organizer',
        'ACTIVE'
    ),
    (
        '22222222-2222-2222-2222-222222222222',
        'admin@e-tix.local',
        '+6281200000002',
        'E-Tix Admin',
        '$2a$12$AiSz71ZSbWzUrspAsiuulOQ3URBfMNf2nWGoe4A7NlGhQUhN3rz8S',
        'admin',
        'ACTIVE'
    )
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 2. EVENT
--    ZONED + layout_zones supaya journey 6 (EO map) dan journey 7 (zoned
--    checkout) punya polygon 'VIP' yang bisa diklik.
-- ----------------------------------------------------------------------------
INSERT INTO events (
    id,
    organizer_id,
    name,
    description,
    venue_name,
    venue_address,
    event_date,
    event_start_time,
    event_end_time,
    status,
    seating_mode,
    layout_image_url,
    layout_zones
)
VALUES (
    '33333333-3333-3333-3333-333333333333',
    '11111111-1111-1111-1111-111111111111',
    'Test Concert',
    'Event seed untuk E2E CI',
    'Test Venue',
    'Jl. Test No. 1, Jakarta',
    CURRENT_DATE + 30,
    '19:00',
    '22:00',
    'ON_SALE',
    'ZONED',
    'https://placehold.co/1920x1080/202020/FFFFFF?text=My+Custom+Map',
    '[{"id":"z1","name":"VIP","price":1000000,"quota":10,"polygon_points":"500,500 1500,500 1500,900 500,900","pov_image_url":"https://placehold.co/400","distance":"10m"}]'::jsonb
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 3. SECTION
--    name = 'VIP' harus sama dengan zone_name karena
--    backend/internal/booking/zoned.go mencocokkan sections.name = zone_name.
-- ----------------------------------------------------------------------------
INSERT INTO sections (
    id,
    event_id,
    name,
    price,
    total_seats,
    bitmask_key,
    layout_version
)
VALUES (
    '44444444-4444-4444-4444-444444444444',
    '33333333-3333-3333-3333-333333333333',
    'VIP',
    1000000.00,
    10,
    'e2e:seat_bitmap:44444444-4444-4444-4444-444444444444',
    1
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 4. BOOKING (sudah PAID supaya tiket langsung CONFIRMED)
-- ----------------------------------------------------------------------------
INSERT INTO bookings (
    id,
    event_id,
    section_id,
    leader_user_id,
    seat_positions,
    total_seats,
    total_amount,
    payment_mode,
    payment_status,
    expires_at
)
VALUES (
    '66666666-6666-6666-6666-666666666666',
    '33333333-3333-3333-3333-333333333333',
    '44444444-4444-4444-4444-444444444444',
    '11111111-1111-1111-1111-111111111111',
    '[]'::jsonb,
    1,
    1000000.00,
    'BULK',
    'PAID',
    NOW() + INTERVAL '30 days'
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 5. TICKET (dipakai journey 3 — resale listing)
-- ----------------------------------------------------------------------------
INSERT INTO tickets (
    id,
    booking_id,
    owner_user_id,
    section_id,
    seat_position,
    status,
    is_assigned,
    qr_secret_key,
    fallback_pin
)
VALUES (
    '55555555-5555-5555-5555-555555555555',
    '66666666-6666-6666-6666-666666666666',
    '11111111-1111-1111-1111-111111111111',
    '44444444-4444-4444-4444-444444444444',
    1,
    'CONFIRMED',
    TRUE,
    'e2e-qr-secret-55555555-5555-5555-5555-555555555555',
    'E2EPIN5555'
)
ON CONFLICT (id) DO NOTHING;
