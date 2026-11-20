-- Back to run_elimination, constraints recreated as in 000004, 000007 and
-- 000008. Fails on categories_type_check once a later version has stored
-- another category type.

ALTER TABLE result_snapshot DROP CONSTRAINT result_snapshot_category_fkey;
ALTER TABLE elimination DROP CONSTRAINT elimination_category_fkey;
ALTER TABLE elimination DROP CONSTRAINT elimination_category_type_check;
ALTER TABLE categories DROP CONSTRAINT categories_type_check;

UPDATE result_snapshot SET category_type = 'run_elimination' WHERE category_type = 'elimination';
UPDATE elimination SET category_type = 'run_elimination' WHERE category_type = 'elimination';
UPDATE categories SET type = 'run_elimination' WHERE type = 'elimination';

ALTER TABLE elimination
    ADD CONSTRAINT elimination_category_type_check CHECK (category_type = 'run_elimination');
ALTER TABLE elimination ALTER COLUMN category_type SET DEFAULT 'run_elimination';
ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'run_elimination'));
ALTER TABLE result_snapshot
    ADD CONSTRAINT result_snapshot_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type);
ALTER TABLE elimination
    ADD CONSTRAINT elimination_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type);
