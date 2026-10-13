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
