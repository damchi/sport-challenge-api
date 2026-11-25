-- The failed replays of a pending Auth0 deletion (SC-API-188, README §2.12):
-- attempts counts them, last_error_code names the last one (a stable code,
-- never the subject nor a text of Auth0), and blocked tells that Auth0
-- refused it for good (a 4xx other than 401, 403, 404, 408 and 429, which
-- are not proper to the row): the worker still replays such a row, in case
-- the cause goes away, but it no longer fails the run, and the admin
-- dashboard counts it apart.
-- The table is small (a row lives until the next successful replay): adding
-- columns with a constant default is instant (no rewrite).
ALTER TABLE pending_auth0_deletions
    ADD COLUMN attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN last_error_code text,
    ADD COLUMN blocked boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT pending_auth0_deletions_attempts_check CHECK (attempts >= 0),
    ADD CONSTRAINT pending_auth0_deletions_blocked_check CHECK (NOT blocked OR last_error_code IS NOT NULL);
