-- ============================================================================
-- Migration: 001_initial_schema (UP)
-- Project:   E-Tix Flash
-- Source:    docs/DATABASE SCHEMA.txt v2.9-FINAL
-- Task:      E3-S2-T14a — ENUMs + 12 tables + constraints + indexes
-- Scope:     NO RLS (T14b), NO triggers (T14c), NO seeds (T14d)
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================================
-- ENUMS (11)
-- ============================================================================

CREATE TYPE user_role_enum AS ENUM ('customer', 'event_organizer', 'admin', 'gate_staff');

CREATE TYPE user_status_enum AS ENUM ('ACTIVE', 'SUSPENDED', 'FROZEN', 'DELETED');

CREATE TYPE ticket_status_enum AS ENUM (
  'AVAILABLE',
  'LOCKED',
  'PENDING_SPLIT_PAY',
  'CONFIRMED',
  'RESALE_PENDING',
  'RESOLD',
  'CANCELLED',
  'USED'
);

CREATE TYPE payment_status_enum AS ENUM ('PENDING', 'PAID', 'EXPIRED', 'FAILED', 'REFUNDED', 'CANCELLED');

CREATE TYPE payment_mode_enum AS ENUM ('SPLIT', 'BULK');

CREATE TYPE payment_method_enum AS ENUM ('VA', 'E_WALLET', 'CREDIT_CARD');

CREATE TYPE resale_status_enum AS ENUM ('PENDING', 'COMPLETED', 'CANCELLED', 'PENDING_PAYOUT');

CREATE TYPE scan_status_enum AS ENUM ('VALID', 'INVALID', 'EXPIRED', 'ALREADY_USED');

CREATE TYPE admin_action_type_enum AS ENUM (
  'BULK_CANCEL',
  'MANUAL_PAID',
  'MANUAL_REFUND',
  'FORCE_RELEASE_SEAT',
  'UPDATE_EVENT_PRICE',
  'SUSPEND_USER',
  'UNSUSPEND_USER',
  'BLACKLIST_IP',
  'REMOVE_BLACKLIST_IP'
);

CREATE TYPE event_status_enum AS ENUM ('DRAFT', 'PUBLISHED', 'ON_SALE', 'SOLD_OUT', 'COMPLETED', 'CANCELLED');

CREATE TYPE insight_type_enum AS ENUM ('GATE_STAFFING', 'PRICE_RECOMMENDATION', 'BOT_BLACKLIST', 'PEAK_TIME');

-- ============================================================================
-- TABLE DEFINITIONS (12)
-- ============================================================================

-- 1. USERS
CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email VARCHAR(255) UNIQUE NOT NULL,
  phone VARCHAR(20) UNIQUE NOT NULL,
  name VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role user_role_enum NOT NULL DEFAULT 'customer',
  status user_status_enum NOT NULL DEFAULT 'ACTIVE',

  base_score INTEGER DEFAULT 50,
  total_purchases INTEGER DEFAULT 0,
  first_purchase_at TIMESTAMPTZ,
  last_purchase_at TIMESTAMPTZ,

  profile_photo_url TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  deleted_at TIMESTAMPTZ,

  CONSTRAINT email_valid CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
  CONSTRAINT phone_valid CHECK (phone ~* '^\+?[0-9]{10,15}$'),
  CONSTRAINT base_score_valid CHECK (base_score BETWEEN 0 AND 100)
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_phone ON users(phone);
CREATE INDEX idx_users_role ON users(role);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_users_purchase_count ON users(total_purchases DESC);
CREATE INDEX idx_users_base_score ON users(base_score DESC);

-- 2. EVENTS
CREATE TABLE IF NOT EXISTS events (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  organizer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  name VARCHAR(255) NOT NULL,
  description TEXT,
  venue_name VARCHAR(255) NOT NULL,
  venue_address TEXT NOT NULL,

  event_date DATE NOT NULL,
  event_start_time TIME NOT NULL,
  event_end_time TIME,

  seat_layout_version INTEGER DEFAULT 1,

  status event_status_enum NOT NULL DEFAULT 'DRAFT',

  banner_url TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  published_at TIMESTAMPTZ,

  CONSTRAINT event_date_valid CHECK (event_date >= CURRENT_DATE)
);

CREATE INDEX idx_events_organizer ON events(organizer_id);
CREATE INDEX idx_events_status ON events(status);
CREATE INDEX idx_events_date ON events(event_date);

-- 3. SECTIONS
CREATE TABLE IF NOT EXISTS sections (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,

  name VARCHAR(100) NOT NULL,
  price NUMERIC(15,2) NOT NULL,

  total_seats INTEGER NOT NULL,
  bitmask_key VARCHAR(255) NOT NULL,
  layout_version INTEGER NOT NULL,

  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),

  CONSTRAINT price_positive CHECK (price > 0),
  CONSTRAINT total_seats_positive CHECK (total_seats > 0),
  UNIQUE(event_id, name, layout_version)
);

CREATE INDEX idx_sections_event ON sections(event_id);
CREATE INDEX idx_sections_bitmask_key ON sections(bitmask_key);

-- 4. BOOKINGS
CREATE TABLE IF NOT EXISTS bookings (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  section_id UUID NOT NULL REFERENCES sections(id) ON DELETE CASCADE,

  leader_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  seat_positions JSONB NOT NULL,
  total_seats INTEGER NOT NULL,
  total_amount NUMERIC(15,2) NOT NULL,

  payment_mode payment_mode_enum NOT NULL DEFAULT 'SPLIT',
  payment_status payment_status_enum NOT NULL DEFAULT 'PENDING',

  expires_at TIMESTAMPTZ NOT NULL,

  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),

  CONSTRAINT total_seats_positive CHECK (total_seats > 0),
  CONSTRAINT total_amount_positive CHECK (total_amount > 0)
);

CREATE INDEX idx_bookings_event ON bookings(event_id);
CREATE INDEX idx_bookings_section ON bookings(section_id);
CREATE INDEX idx_bookings_leader ON bookings(leader_user_id);
CREATE INDEX idx_bookings_payment_status ON bookings(payment_status);
CREATE INDEX idx_bookings_expires_at ON bookings(expires_at) WHERE payment_status = 'PENDING';

-- 5. PAYMENT_OBLIGATIONS
CREATE TABLE IF NOT EXISTS payment_obligations (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,

  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  seat_position INTEGER,

  amount NUMERIC(15,2) NOT NULL,
  status payment_status_enum NOT NULL DEFAULT 'PENDING',

  payment_method payment_method_enum,
  payment_link VARCHAR(255) UNIQUE,

  external_transaction_id VARCHAR(255),
  webhook_received_at TIMESTAMPTZ,

  idempotency_key VARCHAR(255) UNIQUE,

  refunded_at TIMESTAMPTZ,
  refund_transaction_id VARCHAR(255),

  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),

  CONSTRAINT amount_positive CHECK (amount > 0),
  CONSTRAINT seat_position_valid CHECK (seat_position IS NULL OR seat_position > 0)
);

CREATE INDEX idx_payment_obligations_booking ON payment_obligations(booking_id);
CREATE INDEX idx_payment_obligations_user ON payment_obligations(user_id);
CREATE INDEX idx_payment_obligations_status ON payment_obligations(status);
CREATE INDEX idx_payment_obligations_payment_link ON payment_obligations(payment_link);
CREATE INDEX idx_payment_obligations_idempotency ON payment_obligations(idempotency_key);
CREATE INDEX idx_payment_obligations_seat ON payment_obligations(booking_id, seat_position) WHERE seat_position IS NOT NULL;

-- 6. TICKETS
CREATE TABLE IF NOT EXISTS tickets (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  payment_obligation_id UUID REFERENCES payment_obligations(id) ON DELETE SET NULL,

  owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  section_id UUID NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
  seat_position INTEGER NOT NULL,

  status ticket_status_enum NOT NULL DEFAULT 'CONFIRMED',

  is_assigned BOOLEAN DEFAULT FALSE,

  qr_secret_key VARCHAR(255) NOT NULL,
  last_qr_generated_at TIMESTAMPTZ DEFAULT NOW(),

  fallback_pin VARCHAR(12) UNIQUE NOT NULL,
  fallback_pin_used_at TIMESTAMPTZ,
  fallback_pin_used BOOLEAN DEFAULT FALSE,

  original_owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  resale_transaction_id UUID,

  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_tickets_booking_seat_active
ON tickets(booking_id, seat_position)
WHERE status NOT IN ('RESOLD', 'CANCELLED');

CREATE INDEX idx_tickets_booking ON tickets(booking_id);
CREATE INDEX idx_tickets_owner ON tickets(owner_user_id);
CREATE INDEX idx_tickets_status ON tickets(status);
CREATE INDEX idx_tickets_fallback_pin ON tickets(fallback_pin) WHERE fallback_pin_used = FALSE;
CREATE INDEX idx_tickets_section_seat ON tickets(section_id, seat_position);
CREATE INDEX idx_tickets_is_assigned ON tickets(is_assigned) WHERE is_assigned = FALSE;

-- 7. TICKET_SCANS
CREATE TABLE IF NOT EXISTS ticket_scans (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,

  gate_id VARCHAR(50) NOT NULL,
  scanner_device_id UUID,
  scanner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,

  scan_status scan_status_enum NOT NULL,
  scan_method VARCHAR(20) NOT NULL,

  scanned_at TIMESTAMPTZ DEFAULT NOW(),
  latitude NUMERIC(10,8),
  longitude NUMERIC(11,8),
  ip_address INET,

  is_peak_time BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_ticket_scans_ticket ON ticket_scans(ticket_id);
CREATE INDEX idx_ticket_scans_gate ON ticket_scans(gate_id);
CREATE INDEX idx_ticket_scans_status ON ticket_scans(scan_status);
CREATE INDEX idx_ticket_scans_scanned_at ON ticket_scans(scanned_at DESC);

-- 8. RESALE_TRANSACTIONS
CREATE TABLE IF NOT EXISTS resale_transactions (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,

  seller_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  buyer_user_id UUID REFERENCES users(id) ON DELETE SET NULL,

  original_price NUMERIC(15,2) NOT NULL,
  resale_price NUMERIC(15,2) NOT NULL,
  platform_fee NUMERIC(15,2) DEFAULT 0,
  seller_payout NUMERIC(15,2) NOT NULL,

  status resale_status_enum NOT NULL DEFAULT 'PENDING',

  payment_transaction_id VARCHAR(255),
  payout_transaction_id VARCHAR(255),

  created_at TIMESTAMPTZ DEFAULT NOW(),
  completed_at TIMESTAMPTZ,
  cancelled_at TIMESTAMPTZ,

  CONSTRAINT resale_price_valid CHECK (resale_price <= original_price * 1.10),
  CONSTRAINT resale_price_positive CHECK (resale_price > 0),
  CONSTRAINT seller_not_buyer CHECK (seller_user_id != buyer_user_id OR buyer_user_id IS NULL)
);

CREATE INDEX idx_resale_ticket ON resale_transactions(ticket_id);
CREATE INDEX idx_resale_seller ON resale_transactions(seller_user_id);
CREATE INDEX idx_resale_buyer ON resale_transactions(buyer_user_id);
CREATE INDEX idx_resale_status ON resale_transactions(status);
CREATE INDEX idx_resale_event ON resale_transactions(event_id);
CREATE INDEX idx_resale_event_status ON resale_transactions(event_id, status);

CREATE UNIQUE INDEX idx_resale_single_active ON resale_transactions(ticket_id) WHERE status = 'PENDING';

-- 9. ADMIN_ACTIONS
CREATE TABLE IF NOT EXISTS admin_actions (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  admin_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  action_type admin_action_type_enum NOT NULL,
  target_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  target_resource_type VARCHAR(50),
  target_resource_id UUID,

  description TEXT,
  reason TEXT NOT NULL,

  changes_before JSONB,
  changes_after JSONB,

  ip_address INET,
  user_agent TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_admin_actions_admin ON admin_actions(admin_id);
CREATE INDEX idx_admin_actions_type ON admin_actions(action_type);
CREATE INDEX idx_admin_actions_target ON admin_actions(target_resource_type, target_resource_id);
CREATE INDEX idx_admin_actions_created ON admin_actions(created_at DESC);

-- 10. INSIGHTS
CREATE TABLE IF NOT EXISTS insights (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,

  insight_type insight_type_enum NOT NULL,
  title VARCHAR(255) NOT NULL,
  description TEXT NOT NULL,
  recommendation JSONB NOT NULL,

  is_applied BOOLEAN DEFAULT FALSE,
  applied_at TIMESTAMPTZ,
  applied_by UUID REFERENCES users(id) ON DELETE SET NULL,

  generated_at TIMESTAMPTZ DEFAULT NOW(),
  expires_at TIMESTAMPTZ,

  CONSTRAINT insight_expires_valid CHECK (expires_at > generated_at)
);

CREATE INDEX idx_insights_event ON insights(event_id);
CREATE INDEX idx_insights_type ON insights(insight_type);
CREATE INDEX idx_insights_applied ON insights(is_applied);

-- 11. BLACKLIST
CREATE TABLE IF NOT EXISTS blacklist (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  type VARCHAR(20) NOT NULL,
  value VARCHAR(255) NOT NULL,

  reason TEXT,
  detected_by_insight_id UUID REFERENCES insights(id) ON DELETE SET NULL,

  added_by UUID REFERENCES users(id) ON DELETE SET NULL,
  added_at TIMESTAMPTZ DEFAULT NOW(),

  expires_at TIMESTAMPTZ,

  UNIQUE(type, value)
);

CREATE INDEX idx_blacklist_type_value ON blacklist(type, value);
CREATE INDEX idx_blacklist_expires ON blacklist(expires_at) WHERE expires_at IS NOT NULL;

-- 12. SYSTEM_CONFIG
CREATE TABLE IF NOT EXISTS system_config (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key VARCHAR(255) UNIQUE NOT NULL,
  value JSONB NOT NULL,
  description TEXT,
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- ============================================================================
-- END OF MIGRATION 001 (UP)
-- ============================================================================
