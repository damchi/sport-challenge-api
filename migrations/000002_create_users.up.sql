CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Identifier of the user at the auth provider (JWT "sub"); replaced on anonymization.
    auth_subject  text        NOT NULL,
    pseudo        text,
    avatar_key    text,
    language      text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    anonymized_at timestamptz,
    CONSTRAINT users_auth_subject_key UNIQUE (auth_subject),
    CONSTRAINT users_language_check CHECK (language IN ('fr', 'en'))
);

-- Pseudos are unique regardless of case (decision D5).
CREATE UNIQUE INDEX users_pseudo_lower_key ON users (lower(pseudo)) WHERE pseudo IS NOT NULL;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
