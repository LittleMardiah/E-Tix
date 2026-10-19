-- 004_resale_updated_at.up.sql
-- S1-1 CRITICAL fix: resale_transactions was missing updated_at column in migration 001.
-- Mega V3 (ba12015) code writes/reads it → every resale DB write fails at runtime.

ALTER TABLE resale_transactions
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Auto-maintain updated_at on UPDATE (matches pattern of other tables)
CREATE OR REPLACE FUNCTION set_resale_transactions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_resale_transactions_updated_at ON resale_transactions;
CREATE TRIGGER trg_resale_transactions_updated_at
    BEFORE UPDATE ON resale_transactions
    FOR EACH ROW
    EXECUTE FUNCTION set_resale_transactions_updated_at();
