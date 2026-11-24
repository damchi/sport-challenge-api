-- Removes the three sports only where nothing names them: a sport of an
-- activity (deleted ones included), the reference of a challenge or a factor
-- frozen in a category (an admin change of the challenge checks its factors
-- against the catalogue). The rollback then never fails on a foreign key,
-- never erases a user's data and never leaves a challenge an admin can no
-- longer change.
DELETE FROM sports s
WHERE s.code IN ('crossfit', 'hyrox', 'paragliding')
  AND NOT EXISTS (SELECT 1 FROM activities a WHERE a.sport_code = s.code)
  AND NOT EXISTS (SELECT 1 FROM challenges c WHERE c.reference_sport = s.code)
  AND NOT EXISTS (SELECT 1 FROM categories cat WHERE cat.params -> 'factors' ? s.code);
