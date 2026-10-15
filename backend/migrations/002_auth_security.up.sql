-- Migration 002: Auth security hardening (retro-audit FASE 0-4)
--
-- Fixes:
--   A6: users UPDATE RLS policy lacks WITH CHECK → self role/status escalation.
--   A7: SYSTEM_USER password_hash is not a valid bcrypt hash; replace with
--       a bcrypt-incompatible sentinel that always fails comparison.
--   A8: 001 migration header comment is misleading (says "NO RLS/triggers/
--       seeds" but includes all three). 001 is LOCKED; document correction
--       here instead.
--
-- Idempotent: uses DROP IF EXISTS / conditional updates.

-- ---------- A6: users UPDATE WITH CHECK ----------
DROP POLICY IF EXISTS "Users can update own data" ON users;

CREATE POLICY "Users can update own data" ON users
  FOR UPDATE
  USING (id = auth.uid())
  WITH CHECK (
    id = auth.uid()
    AND role = (auth.jwt() ->> 'role')::user_role_enum
    AND status = 'ACTIVE'
  );

-- ---------- A7: SYSTEM_USER invalid hash sentinel ----------
-- '$2a$12$SYSTEM_USER_NO_LOGIN_ACCOUNT_DUMMY_HASH' looks bcrypt-ish but is
-- NOT a valid bcrypt hash (salt+hash portion is not base64). Replace with a
-- clearly-invalid value that bcrypt.CompareHashAndPassword will always reject,
-- and that cannot accidentally match a future auth path.
UPDATE users
SET password_hash = '!disabled-system-user!'
WHERE id = '00000000-0000-0000-0000-000000000000'
  AND password_hash = '$2a$12$SYSTEM_USER_NO_LOGIN_ACCOUNT_DUMMY_HASH';

