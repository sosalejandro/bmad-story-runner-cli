DROP INDEX IF EXISTS checkpoints_idempotency_key_uniq;
DROP INDEX IF EXISTS dispatches_idempotency_key_uniq;

-- SQLite doesn't support DROP COLUMN before 3.35. modernc.org/sqlite ships
-- modern enough; safe to use here.
ALTER TABLE stories     DROP COLUMN claimed_by;
ALTER TABLE stories     DROP COLUMN claimed_at;
ALTER TABLE checkpoints DROP COLUMN idempotency_key;
ALTER TABLE dispatches  DROP COLUMN idempotency_key;
