-- Trigger function shared by every table with an updated_at column.
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
