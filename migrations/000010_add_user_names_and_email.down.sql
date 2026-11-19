-- Dropping the columns drops their constraints.
ALTER TABLE users
    DROP COLUMN email,
    DROP COLUMN last_name,
    DROP COLUMN first_name;
