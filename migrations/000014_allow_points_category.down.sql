-- Back to three category types in positions 1 to 3, as in 000012. Fails on
-- the constraints once a points category is stored.
ALTER TABLE categories DROP CONSTRAINT categories_position_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_position_check CHECK (position BETWEEN 1 AND 3);
ALTER TABLE categories DROP CONSTRAINT categories_type_check;
ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'elimination'));
