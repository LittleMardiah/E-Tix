-- ============================================================================
-- Migration: 006_dual_mode_ticketing (UP)
-- Project:   E-Tix Flash
-- Task:      FASE 10 — Epic 1 (Database Migration) — Dual-Mode Ticketing
--            (Zoned vs Assigned) + Custom Map
-- Source:    docs/ROADMAP FASE 10.txt (Version 1.0-FINAL, 2026-10-24)
-- Scope:     Add dual-mode seating + custom map columns to the events table.
--            ZONED uses Redis DECR for zone quota.
--            ASSIGNED keeps Redis Bitmask + Lua (FASE 3 regression preserved).
-- ============================================================================

-- 1. Custom map (denah) image URL
ALTER TABLE events
  ADD COLUMN layout_image_url TEXT;

-- 2. Zoned zones (SVG polygon coordinates) stored as JSONB
ALTER TABLE events
  ADD COLUMN layout_zones JSONB;

-- 3. Dual-mode seating
--    ZONED  => Zona/area admission (progress bar + zone quota via Redis DECR)
--    ASSIGNED => Specific seat (Redis bitmask + Lua, FASE 3)
ALTER TABLE events
  ADD COLUMN seating_mode VARCHAR(20) NOT NULL DEFAULT 'ASSIGNED'
  CONSTRAINT events_seating_mode_check
    CHECK (seating_mode IN ('ZONED', 'ASSIGNED'));
