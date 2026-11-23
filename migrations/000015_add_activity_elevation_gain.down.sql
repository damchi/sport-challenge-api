ALTER TABLE activities
    DROP CONSTRAINT activities_elevation_gain_meters_check,
    DROP COLUMN elevation_gain_meters;

ALTER TABLE sports
    DROP CONSTRAINT sports_elevation_allowed_by_mode,
    DROP COLUMN elevation_allowed;
