-- 004_resale_updated_at.down.sql
DROP TRIGGER IF EXISTS trg_resale_transactions_updated_at ON resale_transactions;
DROP FUNCTION IF EXISTS set_resale_transactions_updated_at();
ALTER TABLE resale_transactions DROP COLUMN IF EXISTS updated_at;
