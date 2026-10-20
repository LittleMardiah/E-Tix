-- Test fixtures (E3-S3-T17) — NOT production seed.

INSERT INTO users (id, email, phone, name, password_hash, role, status, base_score)
VALUES
  ('11111111-1111-1111-1111-111111111111', 'customer@test.local', '+6281111111111', 'Test Customer', '$2a$12$dummy_hash_for_test_only_not_real', 'customer', 'ACTIVE', 50),
  ('22222222-2222-2222-2222-222222222222', 'eo@test.local', '+6282222222222', 'Test Organizer', '$2a$12$dummy_hash_for_test_only_not_real', 'event_organizer', 'ACTIVE', 50)
ON CONFLICT (id) DO NOTHING;

INSERT INTO events (id, organizer_id, name, venue_name, venue_address, event_date, event_start_time, event_end_time, status, seat_layout_version)
VALUES
  ('33333333-3333-3333-3333-333333333333', '22222222-2222-2222-2222-222222222222', 'Test Concert', 'Test Venue', 'Test Address', CURRENT_DATE + INTERVAL '30 days', '19:00', '23:00', 'ON_SALE', 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO sections (id, event_id, name, price, total_seats, bitmask_key, layout_version)
VALUES
  ('44444444-4444-4444-4444-444444444444', '33333333-3333-3333-3333-333333333333', 'VIP', 2000000, 100, 'seats:3333:4444:v1', 1),
  ('55555555-5555-5555-5555-555555555555', '33333333-3333-3333-3333-333333333333', 'CAT 1', 1000000, 200, 'seats:3333:5555:v1', 1),
  ('66666666-6666-6666-6666-666666666666', '33333333-3333-3333-3333-333333333333', 'CAT 2', 500000, 500, 'seats:3333:6666:v1', 1)
ON CONFLICT (id) DO NOTHING;

-- FASE 7 P4b Bug #23: CONFIRMED tickets for resale integration tests.
-- Fixed UUIDs (ON CONFLICT DO NOTHING) = idempotent across runs.
INSERT INTO users (id, email, phone, name, password_hash, role, status, base_score)
VALUES ('aaaaaaaa-0001-0001-0001-000000000001',
        'resale-seller-seed@e-tix.internal', '+628000000001',
        'Resale Seller Seed',
        '$2a$12$dummyhashnotusedinprod', 'customer', 'ACTIVE', 50)
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, email, phone, name, password_hash, role, status, base_score)
VALUES ('aaaaaaaa-0002-0002-0002-000000000002',
        'resale-buyer-seed@e-tix.internal', '+628000000002',
        'Resale Buyer Seed',
        '$2a$12$dummyhashnotusedinprod', 'customer', 'ACTIVE', 50)
ON CONFLICT (id) DO NOTHING;

INSERT INTO bookings (id, event_id, section_id, leader_user_id,
    seat_positions, total_seats, total_amount, payment_mode, payment_status, expires_at)
SELECT 'bbbbbbbb-0001-0001-0001-000000000001',
       s.event_id, s.id, 'aaaaaaaa-0001-0001-0001-000000000001',
       '[1]'::jsonb, 1, s.price, 'BULK', 'PAID', NOW() + INTERVAL '1 day'
FROM sections s ORDER BY id LIMIT 1
ON CONFLICT (id) DO NOTHING;

INSERT INTO tickets (id, booking_id, owner_user_id, section_id, seat_position,
    status, is_assigned, qr_secret_key, fallback_pin)
SELECT 'cccccccc-0001-0001-0001-000000000001',
       'bbbbbbbb-0001-0001-0001-000000000001',
       'aaaaaaaa-0001-0001-0001-000000000001',
       s.id, 1, 'CONFIRMED', FALSE,
       uuid_generate_v4()::text,
       UPPER(SUBSTRING(REPLACE(uuid_generate_v4()::text, '-', ''), 1, 12))
FROM sections s ORDER BY id LIMIT 1
ON CONFLICT (id) DO NOTHING;

INSERT INTO bookings (id, event_id, section_id, leader_user_id,
    seat_positions, total_seats, total_amount, payment_mode, payment_status, expires_at)
SELECT 'eeeeeeee-0001-0001-0001-000000000001',
       s.event_id, s.id, 'aaaaaaaa-0001-0001-0001-000000000001',
       '[2]'::jsonb, 1, s.price, 'BULK', 'PAID', NOW() + INTERVAL '1 day'
FROM sections s ORDER BY id OFFSET 1 LIMIT 1
ON CONFLICT (id) DO NOTHING;

INSERT INTO tickets (id, booking_id, owner_user_id, section_id, seat_position,
    status, is_assigned, qr_secret_key, fallback_pin)
SELECT 'dddddddd-0002-0002-0002-000000000002',
       'eeeeeeee-0001-0001-0001-000000000001',
       'aaaaaaaa-0001-0001-0001-000000000001',
       s.id, 2, 'CONFIRMED', FALSE,
       uuid_generate_v4()::text,
       UPPER(SUBSTRING(REPLACE(uuid_generate_v4()::text, '-', ''), 1, 12))
FROM sections s ORDER BY id OFFSET 1 LIMIT 1
ON CONFLICT (id) DO NOTHING;

-- Bug #53: idempotent reset — re-running seed restores CONFIRMED status.
-- The resale lifecycle trigger fires on INSERT/UPDATE but NOT on DELETE,
-- so test cleanup leaves tickets stuck at RESALE_PENDING. This UPDATE
-- is a no-op on first run (already CONFIRMED) and fixes stale state on
-- re-runs.
UPDATE tickets SET status='CONFIRMED', updated_at=NOW()
WHERE id IN ('cccccccc-0001-0001-0001-000000000001',
             'dddddddd-0002-0002-0002-000000000002');
