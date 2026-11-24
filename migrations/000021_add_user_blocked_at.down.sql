-- Unblocks every account: the rollback forgets who was blocked, the admin
-- journal (admin_actions) keeps the trace of each block.
ALTER TABLE users DROP COLUMN blocked_at;
