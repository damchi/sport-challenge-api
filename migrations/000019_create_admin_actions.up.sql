-- Journal of the admin changes (SC-API-159, README §2.12, §5.4): one row per
-- change, written in the transaction of the change, with its reason. Read by
-- the admins only. created_at has no default: it comes from platform/clock.
CREATE TABLE admin_actions (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Accounts are anonymized, never removed, so an admin row always stays.
    admin_id      uuid        NOT NULL REFERENCES users (id),
    created_at    timestamptz NOT NULL,
    -- A stable code of the use case (challenge.update...), never free text.
    action        text        NOT NULL,
    -- The target is one of several tables, so no foreign key: a participant
    -- is the id of its account, its challenge in challenge_ids.
    target_type   text        NOT NULL,
    target_id     uuid        NOT NULL,
    -- Every challenge the change touches (an activity counts in several).
    challenge_ids uuid[]      NOT NULL DEFAULT '{}',
    old_value     jsonb,
    new_value     jsonb,
    reason        text        NOT NULL,
    CONSTRAINT admin_actions_action_check CHECK (action ~ '^[a-z][a-z0-9_.]*$' AND char_length(action) <= 64),
    CONSTRAINT admin_actions_target_type_check
        CHECK (target_type IN ('challenge', 'participant', 'activity', 'user')),
    CONSTRAINT admin_actions_challenge_ids_check CHECK (array_position(challenge_ids, NULL) IS NULL),
    CONSTRAINT admin_actions_old_value_check CHECK (jsonb_typeof(old_value) = 'object'),
    CONSTRAINT admin_actions_new_value_check CHECK (jsonb_typeof(new_value) = 'object'),
    CONSTRAINT admin_actions_reason_check CHECK (char_length(reason) BETWEEN 3 AND 500)
);

-- The journal, most recent first (GET /admin/actions), and its filters.
CREATE INDEX admin_actions_created_at_idx ON admin_actions (created_at DESC, id DESC);
-- target_id alone (or with target_type): an id names one target, whatever its type.
CREATE INDEX admin_actions_target_idx ON admin_actions (target_id, created_at DESC, id DESC);
CREATE INDEX admin_actions_admin_idx ON admin_actions (admin_id, created_at DESC, id DESC);
-- The actions that touch a challenge (filter challenge_id, its detail).
CREATE INDEX admin_actions_challenge_ids_idx ON admin_actions USING gin (challenge_ids);
