-- Challenges, their creators, their 3 categories and their participants.
-- No foreign key cascades: results stay when a user is anonymized, and a
-- challenge is only soft-deleted (deleted_at), so nothing is ever removed
-- from under the results.

CREATE TABLE challenges (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    title              text        NOT NULL,
    description        text,
    visibility         text        NOT NULL,
    members_can_invite boolean     NOT NULL DEFAULT false,
    -- IANA time zone (America/Montreal) that cuts the weeks; checked by the service.
    timezone           text        NOT NULL,
    -- Window [starts_at, ends_at): a whole number of weeks in timezone, checked by the service.
    starts_at          timestamptz NOT NULL,
    ends_at            timestamptz NOT NULL,
    created_by         uuid        NOT NULL REFERENCES users (id),
    -- Soft deletion: the challenge disappears for everyone, its rows stay.
    deleted_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT challenges_visibility_check CHECK (visibility IN ('private', 'public')),
    CONSTRAINT challenges_window_check CHECK (ends_at > starts_at)
);

-- Listing of public challenges (home page), soft-deleted ones excluded.
CREATE INDEX challenges_visibility_starts_at_idx ON challenges (visibility, starts_at) WHERE deleted_at IS NULL;

CREATE TRIGGER challenges_set_updated_at
    BEFORE UPDATE ON challenges
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE challenge_creators (
    challenge_id uuid        NOT NULL REFERENCES challenges (id),
    user_id      uuid        NOT NULL REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (challenge_id, user_id)
);

-- Exactly 3 per challenge, one of each type (checked by the service, which
-- creates them with the challenge). params holds the frozen settings: the
-- distance factors of cumulative_distance, the sport and step of
-- run_elimination.
CREATE TABLE categories (
    id           uuid     PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_id uuid     NOT NULL REFERENCES challenges (id),
    type         text     NOT NULL,
    position     smallint NOT NULL,
    params       jsonb    NOT NULL DEFAULT '{}',
    CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'run_elimination')),
    CONSTRAINT categories_position_check CHECK (position BETWEEN 1 AND 3),
    CONSTRAINT categories_params_check CHECK (jsonb_typeof(params) = 'object'),
    -- Its index also serves the lookups by challenge_id.
    CONSTRAINT categories_challenge_type_key UNIQUE (challenge_id, type),
    CONSTRAINT categories_challenge_position_key UNIQUE (challenge_id, position)
);

-- joined_at and abandoned_at have no default: the service sets them from
-- platform/clock, which registration and abandonment rules depend on.
CREATE TABLE participants (
    challenge_id uuid        NOT NULL REFERENCES challenges (id),
    user_id      uuid        NOT NULL REFERENCES users (id),
    status       text        NOT NULL DEFAULT 'active',
    joined_at    timestamptz NOT NULL,
    abandoned_at timestamptz,
    PRIMARY KEY (challenge_id, user_id),
    CONSTRAINT participants_status_check CHECK (status IN ('active', 'abandoned')),
    CONSTRAINT participants_abandoned_at_check CHECK ((status = 'abandoned') = (abandoned_at IS NOT NULL)),
    CONSTRAINT participants_abandoned_after_joined_check CHECK (abandoned_at >= joined_at)
);

-- Challenges of a user.
CREATE INDEX participants_user_id_idx ON participants (user_id);
