-- Activities belong to the user, not to a challenge (ADR 0001): an activity
-- counts in every challenge where its author is an active participant and
-- whose window contains started_at, computed when read. Speed is never
-- stored: it is computed from the duration and the distance.
CREATE TABLE activities (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- No cascade: an anonymized account keeps its activities, so that results
    -- are not changed.
    user_id          uuid        NOT NULL REFERENCES users (id),
    sport_code       text        NOT NULL REFERENCES sports (code),
    -- Free label of a sport typed by hand (sport "other" only, decision D9).
    sport_label      text,
    started_at       timestamptz NOT NULL,
    duration_seconds integer     NOT NULL,
    -- Whether a sport has a distance is checked by the service (sports.distance_mode).
    distance_meters  integer,
    -- manual in V1; the providers (V2) extend this check in their own migration.
    source           text        NOT NULL DEFAULT 'manual',
    -- Identifier of the activity at its provider; null for a manual activity.
    external_id      text,
    -- Key sent by the client so that a repeated creation gives the same activity.
    idempotency_key  text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    CONSTRAINT activities_duration_seconds_check CHECK (duration_seconds > 0),
    CONSTRAINT activities_distance_meters_check CHECK (distance_meters >= 0),
    CONSTRAINT activities_source_check CHECK (source IN ('manual')),
    CONSTRAINT activities_sport_label_by_sport CHECK ((sport_code = 'other') = (sport_label IS NOT NULL)),
    CONSTRAINT activities_sport_label_check CHECK (char_length(sport_label) BETWEEN 2 AND 40)
);

-- Activities of a user in a window (leaderboards, lock, listing).
CREATE INDEX activities_user_started_at_idx ON activities (user_id, started_at) WHERE deleted_at IS NULL;

-- An activity imported twice from a provider is stored once.
CREATE UNIQUE INDEX activities_source_external_id_key ON activities (source, external_id) WHERE external_id IS NOT NULL;

-- A creation repeated with the same key gives the same activity.
CREATE UNIQUE INDEX activities_user_idempotency_key_key ON activities (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TRIGGER activities_set_updated_at
    BEFORE UPDATE ON activities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
