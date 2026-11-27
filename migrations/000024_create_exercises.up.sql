-- Catalogue of exercises (SC-API-210, README §2.5): the exercises of the
-- elimination by repetitions (§2.3), each with a stable code the apps
-- translate and the sport it belongs to. Reference data, like the sports
-- (000003, 000020): a later sport or exercise comes with a new migration.
-- position is the display order within a sport; the unique constraint also
-- serves the read of the exercises of a sport in that order.
CREATE TABLE exercises (
    code       text    PRIMARY KEY,
    sport_code text    NOT NULL REFERENCES sports (code),
    position   integer NOT NULL,
    CONSTRAINT exercises_code_check CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT exercises_position_check CHECK (position > 0),
    CONSTRAINT exercises_sport_position_key UNIQUE (sport_code, position)
);

-- Reference data. Idempotent: an existing code is left as it is.
INSERT INTO exercises (code, sport_code, position) VALUES
    ('burpee',           'crossfit', 1),
    ('wall_ball',        'crossfit', 2),
    ('air_squat',        'crossfit', 3),
    ('push_up',          'crossfit', 4),
    ('pull_up',          'crossfit', 5),
    ('box_jump',         'crossfit', 6),
    ('double_under',     'crossfit', 7),
    ('kettlebell_swing', 'crossfit', 8),
    ('thruster',         'crossfit', 9),
    ('sit_up',           'crossfit', 10)
ON CONFLICT (code) DO NOTHING;
