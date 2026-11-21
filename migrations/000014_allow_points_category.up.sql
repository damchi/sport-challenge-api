-- The points category (SC-API-136): a fourth category type, in position 4
-- (time 1, distance 2, elimination 3, points 4). Its settings (criteria,
-- podium, minimum session) are in params, checked by the service. Its points
-- are never stored: computed from the activities, frozen in result_snapshot
-- like the others, whose foreign key follows categories.
ALTER TABLE categories DROP CONSTRAINT categories_type_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'elimination', 'points'));
ALTER TABLE categories DROP CONSTRAINT categories_position_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_position_check CHECK (position BETWEEN 1 AND 4);
