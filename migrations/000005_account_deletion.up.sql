-- Account deletion (SC-API-039). The deleted account's row stays, anonymized,
-- so that results are not changed; these tables make the deletion final.

-- The SHA-256 of the auth subject of every deleted account, never the
-- subject itself: a later login with that subject (an access token still
-- valid, or an identity not yet deleted at Auth0) is refused instead of
-- creating a new account.
CREATE TABLE deleted_subjects (
    fingerprint bytea       PRIMARY KEY,
    deleted_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deleted_subjects_fingerprint_check CHECK (length(fingerprint) = 32)
);

-- The Auth0 deletions that failed, replayed by the worker until they succeed
-- (task retry-auth0-deletions), then removed. The subject is kept in clear
-- only until then: Auth0 needs it.
CREATE TABLE pending_auth0_deletions (
    auth_subject text        PRIMARY KEY,
    created_at   timestamptz NOT NULL DEFAULT now()
);
