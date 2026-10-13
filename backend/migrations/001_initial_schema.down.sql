-- ============================================================================
-- Migration: 001_initial_schema (DOWN)
-- Project:   E-Tix Flash
-- Reverses:  001_initial_schema.up.sql
-- Order:     DROP tables (FK children first) -> DROP types -> DROP extension
-- ============================================================================


-- ============================================================================
-- TRIGGERS (DOWN) - drop triggers then functions (before DROP TABLE)
-- ============================================================================

DROP TRIGGER IF EXISTS trigger_check_booking_payment ON payment_obligations;
DROP TRIGGER IF EXISTS trigger_update_loyalty_score ON tickets;
DROP TRIGGER IF EXISTS trigger_generate_post_event_report ON events;
DROP TRIGGER IF EXISTS trigger_handle_resale_lifecycle ON resale_transactions;
DROP TRIGGER IF EXISTS trigger_update_ticket_status_on_scan ON ticket_scans;
DROP TRIGGER IF EXISTS trigger_handle_ticket_cancellation ON payment_obligations;
DROP TRIGGER IF EXISTS trigger_handle_booking_cancellation ON bookings;

DROP FUNCTION IF EXISTS check_booking_payment_complete() CASCADE;
DROP FUNCTION IF EXISTS update_user_loyalty_score() CASCADE;
DROP FUNCTION IF EXISTS generate_post_event_report() CASCADE;
DROP FUNCTION IF EXISTS handle_resale_lifecycle() CASCADE;
DROP FUNCTION IF EXISTS update_ticket_status_on_scan() CASCADE;
DROP FUNCTION IF EXISTS handle_ticket_cancellation_on_refund() CASCADE;
DROP FUNCTION IF EXISTS handle_booking_cancellation() CASCADE;

DROP TABLE IF EXISTS blacklist CASCADE;
DROP TABLE IF EXISTS ticket_scans CASCADE;
DROP TABLE IF EXISTS resale_transactions CASCADE;
DROP TABLE IF EXISTS admin_actions CASCADE;
DROP TABLE IF EXISTS insights CASCADE;
DROP TABLE IF EXISTS tickets CASCADE;
DROP TABLE IF EXISTS payment_obligations CASCADE;
DROP TABLE IF EXISTS bookings CASCADE;
DROP TABLE IF EXISTS sections CASCADE;
DROP TABLE IF EXISTS system_config CASCADE;
DROP TABLE IF EXISTS events CASCADE;
DROP TABLE IF EXISTS users CASCADE;

DROP TYPE IF EXISTS user_role_enum;
DROP TYPE IF EXISTS user_status_enum;
DROP TYPE IF EXISTS ticket_status_enum;
DROP TYPE IF EXISTS payment_status_enum;
DROP TYPE IF EXISTS payment_mode_enum;
DROP TYPE IF EXISTS payment_method_enum;
DROP TYPE IF EXISTS resale_status_enum;
DROP TYPE IF EXISTS scan_status_enum;
DROP TYPE IF EXISTS admin_action_type_enum;
DROP TYPE IF EXISTS event_status_enum;
DROP TYPE IF EXISTS insight_type_enum;

DROP EXTENSION IF EXISTS "uuid-ossp";

-- ============================================================================
-- END OF MIGRATION 001 (DOWN)
-- ============================================================================
