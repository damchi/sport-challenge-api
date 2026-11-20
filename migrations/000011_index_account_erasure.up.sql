-- Indexes for the deletion of an account (SC-API-117), which erases the rows
-- of the user in tables indexed otherwise, so far read in full each time.
--
-- Plain CREATE INDEX, in the implicit transaction of the file: each one
-- blocks the writes to its table (not the reads) until the commit. Measured
-- on a local base of 2 million activities, 125,000 participations, 25,000
-- eliminations and 10,000 creator roles and invitations: 0.45 s for
-- activities, built first so that the small tables are locked only for their
-- own few milliseconds; about 0.5 s in all. Production holds far less.
-- Each CREATE INDEX first waits for the transactions writing to its table,
-- and the writes after it wait behind: the API's transactions are short, so
-- the wait is too. CONCURRENTLY would need one file per index (it cannot
-- run in a transaction), and a failure would leave an invalid index behind a
-- dirty version: worth it on a large table, not at these sizes.

-- DELETE FROM activities WHERE user_id, those marked deleted included:
-- activities_user_started_at_idx leaves them out (WHERE deleted_at IS NULL).
CREATE INDEX activities_user_id_idx ON activities (user_id);

-- DELETE FROM challenge_creators WHERE user_id, and the challenges an account
-- is a creator of (LastCreatorChallenges). The primary key leads with
-- challenge_id.
CREATE INDEX challenge_creators_user_id_idx ON challenge_creators (user_id);

-- DELETE FROM elimination WHERE user_id, then the check of
-- elimination_participant_fkey (challenge_id, user_id) for each participation
-- deleted after it. The primary key leads with category_id.
CREATE INDEX elimination_user_id_challenge_id_idx ON elimination (user_id, challenge_id);

-- DELETE FROM invitations WHERE invited_by, and the links to them.
CREATE INDEX invitations_invited_by_idx ON invitations (invited_by);
