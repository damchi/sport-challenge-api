-- Weekly closure of a challenge and the eliminations it records. No foreign
-- key cascades: results stay when a user is anonymized, and a challenge is
-- only soft-deleted, so nothing is ever removed from under them.

-- One row per closed week: the worker inserts it once the week is judged, so
-- a rerun finds the work done (idempotent). closed_at has no default: the
-- worker sets it from platform/clock.
CREATE TABLE week_closure (
    challenge_id uuid        NOT NULL REFERENCES challenges (id),
    week_number  smallint    NOT NULL,
    closed_at    timestamptz NOT NULL,
    PRIMARY KEY (challenge_id, week_number),
    CONSTRAINT week_closure_week_number_check CHECK (week_number >= 1)
);

-- A participant eliminated from a category: at most once, definitively.
-- week_number is the week that was judged; distance_meters is the distance
-- actually run that week and required_meters the minimum it had to reach.
CREATE TABLE elimination (
    challenge_id    uuid     NOT NULL REFERENCES challenges (id),
    category_id     uuid     NOT NULL REFERENCES categories (id),
    user_id         uuid     NOT NULL REFERENCES users (id),
    week_number     smallint NOT NULL,
    distance_meters integer  NOT NULL,
    required_meters integer  NOT NULL,
    PRIMARY KEY (category_id, user_id),
    CONSTRAINT elimination_week_number_check CHECK (week_number >= 1),
    CONSTRAINT elimination_distance_meters_check CHECK (distance_meters >= 0),
    CONSTRAINT elimination_required_meters_check CHECK (required_meters >= 0)
);
