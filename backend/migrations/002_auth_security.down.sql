-- Rollback migration 002.

-- Restore original (weak) UPDATE policy (no WITH CHECK).
DROP POLICY IF EXISTS "Users can update own data" ON users;

CREATE POLICY "Users can update own data" ON users
  FOR UPDATE USING (id = auth.uid());

-- Restore the placeholder hash.
UPDATE users
SET password_hash = '$2a$12$SYSTEM_USER_NO_LOGIN_ACCOUNT_DUMMY_HASH'
WHERE id = '00000000-0000-0000-0000-000000000000'
  AND password_hash = '!disabled-system-user!';

