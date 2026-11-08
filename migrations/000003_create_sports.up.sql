-- Catalogue of sports. Labels are not stored: the apps translate the codes.
-- The rows below are reference data: changing a mode or a suggested factor
-- later takes a new migration with an explicit UPDATE (ON CONFLICT DO NOTHING
-- never touches an existing row). Challenges keep their own frozen copy of
-- the factors, so such a change only affects challenges created afterwards.
CREATE TABLE sports (
    code             text         PRIMARY KEY,
    -- Whether an activity of this sport has a distance: required (run, ride...),
    -- none (yoga...) or optional (other, decision D9).
    distance_mode    text         NOT NULL,
    -- Coefficient suggested for the distance category; the creator of a
    -- challenge may change it, then the challenge keeps its own copy (frozen).
    -- Null for sports without distance.
    suggested_factor numeric(4,2),
    CONSTRAINT sports_code_check CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT sports_distance_mode_check CHECK (distance_mode IN ('required', 'none', 'optional')),
    CONSTRAINT sports_suggested_factor_check CHECK (suggested_factor BETWEEN 0.1 AND 2),
    CONSTRAINT sports_suggested_factor_by_mode CHECK ((distance_mode = 'none') = (suggested_factor IS NULL))
);

-- Reference data. Idempotent: an existing code is left as it is.
INSERT INTO sports (code, distance_mode, suggested_factor) VALUES
    ('run',      'required', 1),
    ('walk',     'required', 1),
    ('hike',     'required', 1),
    ('swim',     'required', 1),
    ('ride',     'required', 0.3),
    ('rowing',   'required', 0.5),
    ('roller',   'required', 0.5),
    ('xc_ski',   'required', 0.5),
    ('yoga',     'none',     NULL),
    ('climbing', 'none',     NULL),
    ('strength', 'none',     NULL),
    ('other',    'optional', 1)
ON CONFLICT (code) DO NOTHING;
