-- The cumulative elevation gain category (SC-API-169, README §2.3): a fifth
-- category type, in position 5 (time 1, distance 2, elimination 3, points 4,
-- elevation 5), added at the end so that no position stored, nor any frozen
-- result, changes. It has no settings. Its ranking is never stored:
-- computed from the activities, frozen in result_snapshot like the others,
-- whose foreign key follows categories.
ALTER TABLE categories DROP CONSTRAINT categories_type_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'elimination', 'points', 'cumulative_elevation'));
ALTER TABLE categories DROP CONSTRAINT categories_position_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_position_check CHECK (position BETWEEN 1 AND 5);
