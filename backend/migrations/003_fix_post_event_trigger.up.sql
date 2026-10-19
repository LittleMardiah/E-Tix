-- 003_fix_post_event_trigger.up.sql
-- Fix SQLSTATE 42702 ambiguous column reference in generate_post_event_report().
--
-- Root cause: PL/pgSQL DECLARE variable `gate_utilization JSONB` is unused but
-- collides with CTE column alias `gate_agg.gate_utilization` when the outer
-- SELECT references the column inside a scalar subquery. Under modern
-- PostgreSQL (plpgsql.variable_conflict = error), this raises SQLSTATE 42702
-- on any `UPDATE events SET status='COMPLETED'`.
--
-- Fix: delete the unused DECLARE variable. Trigger body otherwise unchanged.

CREATE OR REPLACE FUNCTION generate_post_event_report()
RETURNS TRIGGER AS $$
DECLARE
  report_data JSONB;
BEGIN
  IF NEW.status = 'COMPLETED' THEN
    WITH gate_stats AS (
      SELECT
        ts.gate_id,
        COUNT(*) as total_scans,
        SUM(CASE WHEN ts.scan_status = 'VALID' THEN 1 ELSE 0 END) as valid_scans
      FROM ticket_scans ts
      JOIN tickets t ON t.id = ts.ticket_id
      JOIN bookings b ON b.id = t.booking_id
      WHERE b.event_id = NEW.id
      GROUP BY ts.gate_id
    ),
    gate_agg AS (
      SELECT jsonb_object_agg(
        gate_id,
        jsonb_build_object('total', total_scans, 'valid', valid_scans)
      ) as gate_utilization
      FROM gate_stats
    ),
    ticket_stats AS (
      SELECT
        COUNT(*) as total_tickets,
        SUM(CASE WHEN status = 'USED' THEN 1 ELSE 0 END) as used_tickets,
        SUM(CASE WHEN status = 'CONFIRMED' THEN 1 ELSE 0 END) as unused_tickets
      FROM tickets t
      JOIN bookings b ON b.id = t.booking_id
      WHERE b.event_id = NEW.id
    ),
    loyal_fans AS (
      SELECT COUNT(DISTINCT u.id) as loyal_customers
      FROM users u
      JOIN tickets t ON t.owner_user_id = u.id
      JOIN bookings b ON b.id = t.booking_id
      WHERE b.event_id = NEW.id
      AND u.total_purchases > 1
    )
    SELECT
      jsonb_build_object(
        'event_id', NEW.id,
        'event_name', NEW.name,
        'total_tickets', (SELECT total_tickets FROM ticket_stats),
        'used_tickets', (SELECT used_tickets FROM ticket_stats),
        'unused_tickets', (SELECT unused_tickets FROM ticket_stats),
        'no_show_rate',
          CASE
            WHEN (SELECT total_tickets FROM ticket_stats) > 0
            THEN ROUND(((SELECT unused_tickets FROM ticket_stats)::NUMERIC / (SELECT total_tickets FROM ticket_stats)::NUMERIC) * 100, 2)
            ELSE 0
          END,
        'total_scans', COALESCE((SELECT SUM(total_scans) FROM gate_stats), 0),
        'valid_scans', COALESCE((SELECT SUM(valid_scans) FROM gate_stats), 0),
        'gate_utilization', COALESCE((SELECT gate_utilization FROM gate_agg), '{}'::jsonb),
        'loyal_customers', (SELECT loyal_customers FROM loyal_fans),
        'generated_at', NOW()
      ) INTO report_data;

    INSERT INTO insights (
      event_id,
      insight_type,
      title,
      description,
      recommendation
    ) VALUES (
      NEW.id,
      'PEAK_TIME',
      'Post-Event Report - ' || NEW.name,
      'Auto-generated report after event completion',
      report_data
    );
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
