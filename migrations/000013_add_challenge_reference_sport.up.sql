-- The reference sport of a challenge (SC-API-133), chosen at creation and
-- frozen: the distances of the cumulative distance category are brought back
-- to it, and the elimination is run in it. The service only accepts a sport
-- whose distance is required; the foreign key ties it to the catalogue.
-- Run, the reference so far, for the existing challenges: a constant default,
-- so the column is added without rewriting the table.
ALTER TABLE challenges
    ADD COLUMN reference_sport text NOT NULL DEFAULT 'run' REFERENCES sports (code);
