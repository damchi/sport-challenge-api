-- Repetitions of an activity, one value per exercise of the catalogue
-- (SC-API-213, README §2.5): they count for the elimination by repetitions
-- only. The entry comes with SC-API-216.
CREATE TABLE activity_repetitions (
    -- Cascade: the repetitions belong to the activity, and the deletion of
    -- an account erases its activities (activity.Repository.EraseUser).
    activity_id   uuid    NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    exercise_code text    NOT NULL REFERENCES exercises (code),
    repetitions   integer NOT NULL,
    -- One value per exercise and activity. Leading with activity_id, the
    -- primary key also serves the weekly totals by exercise (SC-API-217):
    -- that read starts from the activities of the participants in the
    -- window (activities_user_started_at_idx) and looks up each one's
    -- repetitions of the exercise here, by (activity_id, exercise_code);
    -- and the cascade above. No other index: no read starts from an
    -- exercise (the catalogue is only rolled back by 000024's down).
    PRIMARY KEY (activity_id, exercise_code),
    CONSTRAINT activity_repetitions_repetitions_check CHECK (repetitions BETWEEN 0 AND 10000)
);
