-- ============================================================================
-- Migration: 006_dual_mode_ticketing (DOWN)
-- Project:   E-Tix Flash
-- Task:      FASE 10 — Epic 1 (Database Migration) — Reverse dual-mode columns.
-- ============================================================================

-- 1. Drop the CHECK constraint first
ALTER TABLE events
  DROP CONSTRAINT IF EXISTS events_seating_mode_check;

-- 2. Drop the added columns
ALTER TABLE events
  DROP COLUMN IF EXISTS seating_mode,
  DROP COLUMN IF EXISTS layout_zones,
  DROP COLUMN IF EXISTS layout_image_url;
