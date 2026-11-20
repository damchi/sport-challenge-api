-- First and last name, set by the user (required for a complete profile, seen
-- by the other users), and the e-mail, copied from the access token and only
-- ever shown to its owner. Nullable: existing users have none yet, and the
-- anonymization of an account erases them. Adding nullable columns without a
-- default does not rewrite the table.
ALTER TABLE users
    ADD COLUMN first_name text,
    ADD COLUMN last_name  text,
    ADD COLUMN email      text,
    ADD CONSTRAINT users_first_name_check CHECK (char_length(first_name) BETWEEN 1 AND 50),
    ADD CONSTRAINT users_last_name_check CHECK (char_length(last_name) BETWEEN 1 AND 50),
    ADD CONSTRAINT users_email_check CHECK (char_length(email) BETWEEN 3 AND 254);
