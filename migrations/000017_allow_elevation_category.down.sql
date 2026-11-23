-- Back to four category types in positions 1 to 4, as in 000014. Fails on
-- the constraints once a cumulative elevation category is stored.
ALTER TABLE categories DROP CONSTRAINT categories_position_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_position_check CHECK (position BETWEEN 1 AND 4);
ALTER TABLE categories DROP CONSTRAINT categories_type_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'elimination', 'points'));
