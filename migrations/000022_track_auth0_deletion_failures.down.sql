ALTER TABLE pending_auth0_deletions
    DROP CONSTRAINT pending_auth0_deletions_blocked_check,
    DROP CONSTRAINT pending_auth0_deletions_attempts_check,
    DROP COLUMN blocked,
    DROP COLUMN last_error_code,
    DROP COLUMN attempts;
