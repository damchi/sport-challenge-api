-- CrossFit, Hyrox and paragliding join the catalogue (SC-API-174, README
-- §2.4 and §2.5). Reference data, idempotent: an existing code is left as it
-- is. None takes an elevation gain. Existing challenges froze their factors
-- at creation: the two sports with a distance count there for the cumulative
-- time only (README §2.4).
INSERT INTO sports (code, distance_mode, suggested_factor, elevation_allowed) VALUES
    ('crossfit',    'none',     NULL, false),
    ('hyrox',       'required', 1,    false),
    ('paragliding', 'required', 0.3,  false)
ON CONFLICT (code) DO NOTHING;
