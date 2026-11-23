-- Elevation gain of an activity (SC-API-149, README §2.5): optional, entered
-- for the sports with a distance where it makes sense, display only (no
-- category, no badge counts it).

-- Whether an activity of the sport may have an elevation gain. A sport
-- without distance never has one. A constant default: the column is added
-- without rewriting the table; the UPDATE below sets the reference data.
ALTER TABLE sports
    ADD COLUMN elevation_allowed boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT sports_elevation_allowed_by_mode CHECK (NOT (distance_mode = 'none' AND elevation_allowed));

-- Reference data: idempotent, swimming and rowing keep false.
UPDATE sports SET elevation_allowed = true
WHERE code IN ('run', 'walk', 'hike', 'ride', 'roller', 'xc_ski', 'other');

-- Elevation gain in meters; null when not entered. Whether the sport takes
-- one, and the activity has a distance, is checked by the service
-- (sports.elevation_allowed), as for the distance. Nullable without default:
-- no table rewrite.
ALTER TABLE activities
    ADD COLUMN elevation_gain_meters integer,
    ADD CONSTRAINT activities_elevation_gain_meters_check CHECK (elevation_gain_meters >= 0);
