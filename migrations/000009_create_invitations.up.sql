-- Invitations to a challenge (SC-API-065): a link, reusable by several
-- people until it expires at the end of week 1 (the registration deadline)
-- or is revoked. No foreign key cascades: a challenge is only soft-deleted
-- and an account is anonymized, never removed. created_at has no default:
-- the service sets it from platform/clock, which the expiry depends on.
CREATE TABLE invitations (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_id uuid        NOT NULL REFERENCES challenges (id),
    invited_by   uuid        NOT NULL REFERENCES users (id),
    -- The SHA-256 of the token, never the token itself: a leak of the table
    -- gives no usable link. Its unique index serves the lookups by token.
    token_hash   bytea       NOT NULL,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    CONSTRAINT invitations_token_hash_key UNIQUE (token_hash),
    CONSTRAINT invitations_token_hash_check CHECK (length(token_hash) = 32),
    CONSTRAINT invitations_expires_after_created_check CHECK (expires_at > created_at),
    CONSTRAINT invitations_revoked_after_created_check CHECK (revoked_at >= created_at)
);

-- The users who opened an invitation while logged in: a private challenge is
-- read through this recorded link to a valid invitation (Membership.Invited),
-- never through the token itself. Linked once per invitation and user.
CREATE TABLE invitation_recipients (
    invitation_id uuid        NOT NULL REFERENCES invitations (id),
    user_id       uuid        NOT NULL REFERENCES users (id),
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (invitation_id, user_id)
);

-- The invitations linked to a user, read by user_id with their membership in
-- a challenge (the primary key starts with invitation_id).
CREATE INDEX invitation_recipients_user_id_idx ON invitation_recipients (user_id);
