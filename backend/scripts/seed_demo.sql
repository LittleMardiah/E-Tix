-- ============================================================================
-- Demo seed — E-Tix Flash
-- Run:  psql "$DATABASE_URL" -f backend/scripts/seed_demo.sql
-- Login: eo@demo.com / customer@demo.com  (password: password123)
-- Idempotent: deletes the two demo events and their cascade first.
-- ============================================================================

-- 0. Hapus data lama (idempotent). Children cascade from events/users.
DELETE FROM tickets WHERE booking_id IN (
  SELECT id FROM bookings WHERE event_id IN (
    '11111111-1111-1111-1111-111111111111',
    '22222222-2222-2222-2222-222222222222'
  )
);
DELETE FROM bookings WHERE event_id IN (
  '11111111-1111-1111-1111-111111111111',
  '22222222-2222-2222-2222-222222222222'
);
DELETE FROM sections WHERE event_id IN (
  '11111111-1111-1111-1111-111111111111',
  '22222222-2222-2222-2222-222222222222'
);
DELETE FROM events WHERE id IN (
  '11111111-1111-1111-1111-111111111111',
  '22222222-2222-2222-2222-222222222222'
);
DELETE FROM users
WHERE email IN ('eo@demo.com', 'customer@demo.com')
   OR id IN (
     '99999999-9999-9999-9999-999999999999',
     '88888888-8888-8888-8888-888888888888'
   );

-- 1. Users (password: password123, bcrypt cost 12)
INSERT INTO users (id, email, phone, name, password_hash, role, status) VALUES
('99999999-9999-9999-9999-999999999999', 'eo@demo.com', '+6281111111199', 'Demo EO', '$2a$12$uC9ZcItOL3CzsYPNjtxnQecBXrHCeiKX8V1K01TM6yONXccX/ZR9m', 'event_organizer', 'ACTIVE'),
('88888888-8888-8888-8888-888888888888', 'customer@demo.com', '+6282222222299', 'Demo Customer', '$2a$12$uC9ZcItOL3CzsYPNjtxnQecBXrHCeiKX8V1K01TM6yONXccX/ZR9m', 'customer', 'ACTIVE');

-- 2. Event 1 — Standar / ASSIGNED seating
INSERT INTO events (
  id, organizer_id, name, description, venue_name, venue_address,
  event_date, event_start_time, status, seating_mode
) VALUES (
  '11111111-1111-1111-1111-111111111111',
  '99999999-9999-9999-9999-999999999999',
  'Rock Concert 2026',
  'Konser rock tahunan.',
  'GBK',
  'Jakarta',
  '2026-12-31',
  '19:00',
  'ON_SALE',
  'ASSIGNED'
);

INSERT INTO sections (id, event_id, name, price, total_seats, bitmask_key, layout_version) VALUES
('aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa', '11111111-1111-1111-1111-111111111111', 'CAT 1', 500000, 100, 'bitmask:aaaa1111', 1);

-- 3. Event 2 — Premium / ZONED seating + custom interactive map
--    NOTE: layout_zones lives on events (migration 006), not on sections.
INSERT INTO events (
  id, organizer_id, name, description, venue_name, venue_address,
  event_date, event_start_time, status, seating_mode,
  layout_image_url, layout_image_width, layout_image_height, layout_zones
) VALUES (
  '22222222-2222-2222-2222-222222222222',
  '99999999-9999-9999-9999-999999999999',
  'Blackpink World Tour',
  'Konser Blackpink.',
  'Gelora Bung Karno',
  'Jakarta',
  '2027-10-20',
  '18:00',
  'ON_SALE',
  'ZONED',
  '/images/map-blackpink.jpg',
  1200,
  800,
  '[
    {
      "id": "z1",
      "name": "VIP (Front Stage)",
      "price": 2000000,
      "quota": 50,
      "polygon_points": "300,200 900,200 900,500 300,500",
      "pov_image_url": "https://placehold.co/400x200/333333/FFFFFF?text=POV+VIP",
      "distance": "5 meter"
    },
    {
      "id": "z2",
      "name": "CAT 1",
      "price": 1000000,
      "quota": 200,
      "polygon_points": "200,550 1000,550 1000,750 200,750",
      "pov_image_url": "https://placehold.co/400x200/333333/FFFFFF?text=POV+CAT+1",
      "distance": "15 meter"
    }
  ]'::jsonb
);

INSERT INTO sections (id, event_id, name, price, total_seats, bitmask_key, layout_version) VALUES
('bbbb1111-bbbb-bbbb-bbbb-bbbbbbbbbbbb', '22222222-2222-2222-2222-222222222222', 'VIP (Front Stage)', 2000000, 50, 'bitmask:bbbb1111', 1),
('bbbb2222-bbbb-bbbb-bbbb-bbbbbbbbbbbb', '22222222-2222-2222-2222-222222222222', 'CAT 1', 1000000, 200, 'bitmask:bbbb2222', 1);
