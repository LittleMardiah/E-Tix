-- 005_seat_lock_queue.down.sql
-- P9a-12 TD-F9-17: drop the durable seat-lock queue.
DROP POLICY IF EXISTS "Admins can view all queued seat locks" ON seat_lock_queue;
DROP POLICY IF EXISTS "Users can view own queued seat locks" ON seat_lock_queue;
DROP INDEX IF EXISTS idx_seat_lock_queue_created_at;
DROP TABLE IF EXISTS seat_lock_queue;
