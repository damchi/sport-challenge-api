-- Weekly closure of a challenge and the eliminations it records. No foreign
-- key cascades: results stay when a user is anonymized, and a challenge is
-- only soft-deleted, so nothing is ever removed from under them.

-- Target of the composite foreign key of elimination: a category is only
-- referenced together with its challenge and its type.
ALTER TABLE categories
    ADD CONSTRAINT categories_id_challenge_type_key UNIQUE (id, challenge_id, type);

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

-- A participant eliminated from the run elimination category: at most once,
-- definitively. week_number is the week that was judged; distance_meters is
-- the distance actually run that week and required_meters the minimum it had
-- to reach: the record of the judgement, never read to rank (the leaderboard
-- is computed from the activities).
CREATE TABLE elimination (
    challenge_id    uuid     NOT NULL,
    category_id     uuid     NOT NULL,
    user_id         uuid     NOT NULL,
    -- Constant: with the composite key below, only the run elimination
    -- category of the challenge can be referenced.
    category_type   text     NOT NULL DEFAULT 'run_elimination',
    week_number     smallint NOT NULL,
    distance_meters integer  NOT NULL,
    required_meters integer  NOT NULL,
    PRIMARY KEY (category_id, user_id),
    CONSTRAINT elimination_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type),
    -- Only a participant of the challenge can be eliminated (which also
    -- ties user_id to users).
    CONSTRAINT elimination_participant_fkey FOREIGN KEY (challenge_id, user_id)
        REFERENCES participants (challenge_id, user_id),
    CONSTRAINT elimination_category_type_check CHECK (category_type = 'run_elimination'),
    CONSTRAINT elimination_week_number_check CHECK (week_number >= 1),
    CONSTRAINT elimination_distance_meters_check CHECK (distance_meters >= 0),
    CONSTRAINT elimination_required_meters_check CHECK (required_meters >= 0)
);
