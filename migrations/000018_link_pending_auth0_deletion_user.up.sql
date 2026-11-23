-- The account of a pending Auth0 deletion (SC-API-158, README §2.12), so
-- that the admin sees which deleted account still waits for it. The deletion
-- anonymizes users.auth_subject, which no longer finds the row. Nullable:
-- the rows recorded before have no account to link (they show as deleted),
-- and the row, its subject in clear included, is removed as soon as Auth0
-- deleted the identity, so the link lasts no longer than the subject. No
-- cascade: an account row is never deleted (anonymized). The constraint
-- holds one row per account, and its index serves the lookup by account.
-- The table is small (a row lives until the next run of the worker): adding
-- a nullable column without default and its index is instant.
ALTER TABLE pending_auth0_deletions
    ADD COLUMN user_id uuid REFERENCES users (id),
    ADD CONSTRAINT pending_auth0_deletions_user_id_key UNIQUE (user_id);
