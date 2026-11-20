-- The elimination category is no longer tied to running (SC-API-132):
-- run_elimination becomes elimination. Same behaviour, new name.
--
-- The two composite foreign keys to categories (id, challenge_id, type)
-- carry the type and do not cascade updates: they are dropped around the
-- UPDATEs, then recreated as they were, which checks every row again.
-- No compatibility shim: until the API and the worker are deployed, right
-- after the migration, the previous version can no longer create a
-- challenge (categories_type_check) nor recognize its elimination category.
-- A brief window, accepted for a rename.

ALTER TABLE elimination DROP CONSTRAINT elimination_category_fkey;
ALTER TABLE result_snapshot DROP CONSTRAINT result_snapshot_category_fkey;
ALTER TABLE categories DROP CONSTRAINT categories_type_check;
ALTER TABLE elimination DROP CONSTRAINT elimination_category_type_check;

UPDATE categories SET type = 'elimination' WHERE type = 'run_elimination';
UPDATE elimination SET category_type = 'elimination' WHERE category_type = 'run_elimination';
UPDATE result_snapshot SET category_type = 'elimination' WHERE category_type = 'run_elimination';

ALTER TABLE categories
    ADD CONSTRAINT categories_type_check CHECK (type IN ('cumulative_time', 'cumulative_distance', 'elimination'));
-- Constant: with the composite key below, only the elimination category of
-- the challenge can be referenced.
ALTER TABLE elimination ALTER COLUMN category_type SET DEFAULT 'elimination';
ALTER TABLE elimination
    ADD CONSTRAINT elimination_category_type_check CHECK (category_type = 'elimination');
ALTER TABLE elimination
    ADD CONSTRAINT elimination_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type);
ALTER TABLE result_snapshot
    ADD CONSTRAINT result_snapshot_category_fkey FOREIGN KEY (category_id, challenge_id, category_type)
        REFERENCES categories (id, challenge_id, type);
