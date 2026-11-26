-- Indexes for the reads of the admin and the recompute (SC-API-191), which
-- filter or sort on columns indexed otherwise, so far read in full each time.
--
-- Plain CREATE INDEX, in the implicit transaction of the file, as in 000011:
-- each one blocks the writes to its table (not the reads) until the commit,
-- a few milliseconds at the sizes of production. Checked with EXPLAIN ANALYZE
-- on a local base the size of the leaderboard benchmark (20,000 accounts,
-- 2,000 challenges, 300,000 activities, 24,000 eliminations, 10,000
-- invitations): each query below uses its index, from a sequential scan.
-- activities first, the largest, so that the small tables are locked only
-- for their own few milliseconds.

-- The dashboard (Figures): the activities recorded in the last 7 days, never
-- those marked deleted (WHERE deleted_at IS NULL, as the queries). The other
-- indexes of activities lead with user_id.
CREATE INDEX activities_created_at_idx ON activities (created_at) WHERE deleted_at IS NULL;

-- The recompute (ClearResults, under the global lock), the admin card and the
-- preview of a correction (readEliminations): WHERE challenge_id. The primary
-- key leads with category_id, elimination_user_id_challenge_id_idx with
-- user_id.
CREATE INDEX elimination_challenge_id_idx ON elimination (challenge_id);

-- The expiry moved by an admin change of the registration deadline
-- (Reschedule) and the invitations of the admin card: WHERE challenge_id.
CREATE INDEX invitations_challenge_id_idx ON invitations (challenge_id);

-- The admin lists, most recently created first, by the cursor (created_at, id);
-- for users, also the accounts created in the last 7 days of the dashboard.
CREATE INDEX challenges_created_at_id_idx ON challenges (created_at, id);
CREATE INDEX users_created_at_id_idx ON users (created_at, id);
