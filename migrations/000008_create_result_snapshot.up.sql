-- The final ranking of each category, frozen when the challenge ends: the
-- only stored result (rankings are otherwise computed from the raw
-- activities). One row per category, written once by the worker, all the
-- categories of a challenge in one transaction. No foreign key cascades: a
-- finished challenge keeps its results, and a challenge is only
-- soft-deleted. created_at has no default: the worker sets it from
-- platform/clock.
CREATE TABLE result_snapshot (
    category_id   uuid        PRIMARY KEY,
    challenge_id  uuid        NOT NULL REFERENCES challenges (id),
    category_type text        NOT NULL,
    -- The ranking by user id, never by pseudo: an account anonymized later
    -- shows under a neutral name.
    payload       jsonb       NOT NULL,
    created_at    timestamptz NOT NULL,
    CONSTRAINT result_snapshot_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type)
);

CREATE INDEX result_snapshot_challenge_id_idx ON result_snapshot (challenge_id);
