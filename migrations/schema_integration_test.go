//go:build integration

package migrations

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

const (
	userID      = "00000000-0000-0000-0000-000000000001"
	challengeID = "00000000-0000-0000-0000-0000000000c1"
	categoryID  = "00000000-0000-0000-0000-0000000000ca"
	// Another category of the fixture challenge, another user and another challenge.
	timeCategoryID   = "00000000-0000-0000-0000-0000000000cb"
	otherUserID      = "00000000-0000-0000-0000-000000000002"
	otherChallengeID = "00000000-0000-0000-0000-0000000000c2"
	deadID           = "00000000-0000-0000-0000-00000000dead"
	// Two invitations of the fixture challenge.
	invitationID      = "00000000-0000-0000-0000-0000000000a1"
	otherInvitationID = "00000000-0000-0000-0000-0000000000a2"
)

// fixture is a user and a two-week challenge the cases build on.
const fixture = `
INSERT INTO users (id, auth_subject) VALUES ('` + userID + `', 'schema|1');
INSERT INTO challenges (id, title, visibility, timezone, starts_at, ends_at, created_by)
VALUES ('` + challengeID + `', 'Spring', 'public', 'America/Montreal',
        '2026-12-01 05:00Z', '2026-12-15 05:00Z', '` + userID + `');`

// eliminationFixture is the elimination category of the fixture challenge
// and its only participant.
const eliminationFixture = `
INSERT INTO categories (id, challenge_id, type, position) VALUES ('` + categoryID + `', '` + challengeID + `', 'elimination', 3);
INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL);`

// otherChallengeFixture is a second challenge of the same creator.
const otherChallengeFixture = `
INSERT INTO challenges (id, title, visibility, timezone, starts_at, ends_at, created_by)
VALUES ('` + otherChallengeID + `', 'Autumn', 'public', 'UTC', '2026-12-01Z', '2026-12-15Z', '` + userID + `');`

// snapshotRow is the insertion of the snapshot of a category.
func snapshotRow(category, challenge, categoryType string) string {
	return `INSERT INTO result_snapshot (category_id, challenge_id, category_type, payload, created_at) VALUES ('` + category + `', '` + challenge + `', '` + categoryType + `', '{"entries": []}', '2026-12-15Z');`
}

// invitationRow is the insertion of invitation id of the fixture challenge,
// created at its start, with SQL expressions for its token hash, its expiry
// and its revocation.
func invitationRow(id, tokenHash, expiresAt, revokedAt string) string {
	return `INSERT INTO invitations (id, challenge_id, invited_by, token_hash, expires_at, created_at, revoked_at) VALUES ('` + id + `', '` + challengeID + `', '` + userID + `', ` + tokenHash + `, ` + expiresAt + `, '2026-12-01Z', ` + revokedAt + `);`
}

// recipientRow is the link of user to the invitation of invitationRow.
func recipientRow(user string) string {
	return `INSERT INTO invitation_recipients VALUES ('` + invitationID + `', '` + user + `', '2026-12-02Z');`
}

// TestConstraints runs each statement on the fixture, in a transaction rolled
// back afterwards, and checks the constraint that refuses it (none: accepted).
func TestConstraints(t *testing.T) {
	pool := testdb.New(t)
	tests := []struct {
		name       string
		sql        string
		constraint string
	}{
		// sports (000003)
		{name: "factor for a sport without distance", sql: `INSERT INTO sports VALUES ('dance', 'none', 1)`, constraint: "sports_suggested_factor_by_mode"},
		{name: "no factor for a sport with distance", sql: `INSERT INTO sports VALUES ('canoe', 'required', NULL)`, constraint: "sports_suggested_factor_by_mode"},
		{name: "factor under 0.1", sql: `INSERT INTO sports VALUES ('canoe', 'required', 0.05)`, constraint: "sports_suggested_factor_check"},
		{name: "factor over 2", sql: `INSERT INTO sports VALUES ('canoe', 'required', 2.5)`, constraint: "sports_suggested_factor_check"},
		{name: "unknown distance mode", sql: `INSERT INTO sports VALUES ('canoe', 'sometimes', 1)`, constraint: "sports_distance_mode_check"},
		{name: "uppercase code", sql: `INSERT INTO sports VALUES ('Canoe', 'required', 1)`, constraint: "sports_code_check"},
		{name: "elevation gain for a sport without distance", sql: `INSERT INTO sports (code, distance_mode, suggested_factor, elevation_allowed) VALUES ('dance', 'none', NULL, true)`, constraint: "sports_elevation_allowed_by_mode"},
		{name: "factor bounds included", sql: `INSERT INTO sports VALUES ('canoe', 'required', 0.1), ('kayak', 'optional', 2)`},

		// challenges (000004)
		{name: "valid challenge", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'private', 'UTC', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`},
		{name: "other visibility", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'friends', 'UTC', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`, constraint: "challenges_visibility_check"},
		{name: "end before start", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-15Z', '2026-12-01Z', '` + userID + `')`, constraint: "challenges_window_check"},
		{name: "end at start", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-01Z', '2026-12-01Z', '` + userID + `')`, constraint: "challenges_window_check"},
		{name: "unknown creator", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-01Z', '2026-12-15Z', '00000000-0000-0000-0000-00000000dead')`, constraint: "challenges_created_by_fkey"},

		// challenges.reference_sport (000013)
		{name: "ride as the reference sport", sql: `INSERT INTO challenges (title, visibility, timezone, reference_sport, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', 'ride', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`},
		{name: "unknown reference sport", sql: `INSERT INTO challenges (title, visibility, timezone, reference_sport, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', 'curling', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`, constraint: "challenges_reference_sport_fkey"},

		// categories (000004)
		{name: "the 3 categories", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'cumulative_distance', 2), ('` + challengeID + `', 'elimination', 3)`},
		{name: "unknown category type", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'fastest', 1)`, constraint: "categories_type_check"},
		{name: "position 0", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 0)`, constraint: "categories_position_check"},
		{name: "params not an object", sql: `INSERT INTO categories (challenge_id, type, position, params) VALUES ('` + challengeID + `', 'cumulative_time', 1, '[]')`, constraint: "categories_params_check"},
		{name: "same type twice", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'cumulative_time', 2)`, constraint: "categories_challenge_type_key"},
		{name: "same position twice", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'elimination', 1)`, constraint: "categories_challenge_position_key"},

		// categories and elimination renamed (000012)
		{name: "former run elimination type", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'run_elimination', 3)`, constraint: "categories_type_check"},
		{name: "former run elimination type recorded", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, category_type, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 'run_elimination', 2, 4000, 6000)`, constraint: "elimination_category_type_check"},

		// points category (000014)
		{name: "the 4 categories", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'cumulative_distance', 2), ('` + challengeID + `', 'elimination', 3), ('` + challengeID + `', 'points', 4)`},
		{name: "position 5", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'points', 5)`, constraint: "categories_position_check"},

		// participants (000004)
		{name: "active participant", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL)`},
		{name: "abandoned participant", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-02Z', '2026-12-03Z')`},
		{name: "abandoned without a date", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-02Z', NULL)`, constraint: "participants_abandoned_at_check"},
		{name: "abandon date while active", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', '2026-12-03Z')`, constraint: "participants_abandoned_at_check"},
		{name: "abandoned before joining", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-03Z', '2026-12-02Z')`, constraint: "participants_abandoned_after_joined_check"},
		{name: "unknown status", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'excluded', '2026-12-02Z', NULL)`, constraint: "participants_status_check"},
		{name: "joined twice", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL), ('` + challengeID + `', '` + userID + `', 'active', '2026-12-04Z', NULL)`, constraint: "participants_pkey"},

		// deleted_subjects and pending_auth0_deletions (000005)
		{name: "fingerprint of a deleted subject", sql: `INSERT INTO deleted_subjects (fingerprint) VALUES (sha256('email|1'::bytea))`},
		{name: "fingerprint not a SHA-256", sql: `INSERT INTO deleted_subjects (fingerprint) VALUES ('email|1'::bytea)`, constraint: "deleted_subjects_fingerprint_check"},
		{name: "same fingerprint twice", sql: `INSERT INTO deleted_subjects (fingerprint) VALUES (sha256('email|1'::bytea)), (sha256('email|1'::bytea))`, constraint: "deleted_subjects_pkey"},
		{name: "same pending deletion twice", sql: `INSERT INTO pending_auth0_deletions (auth_subject) VALUES ('email|1'), ('email|1')`, constraint: "pending_auth0_deletions_pkey"},

		// activities (000006)
		{name: "valid run", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ('` + userID + `', 'run', '2026-12-02Z', 1800, 5000)`},
		{name: "yoga without distance", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 3600)`},
		{name: "zero distance", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ('` + userID + `', 'run', '2026-12-02Z', 1800, 0)`},
		{name: "other with a label", sql: `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ('` + userID + `', 'other', 'Padel', '2026-12-02Z', 3600)`},
		{name: "label bounds included", sql: `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ('` + userID + `', 'other', 'Go', '2026-12-02Z', 60), ('` + userID + `', 'other', repeat('a', 40), '2026-12-02Z', 60)`},
		{name: "zero duration", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 0)`, constraint: "activities_duration_seconds_check"},
		{name: "negative distance", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ('` + userID + `', 'run', '2026-12-02Z', 1800, -1)`, constraint: "activities_distance_meters_check"},
		{name: "negative elevation gain", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, elevation_gain_meters) VALUES ('` + userID + `', 'run', '2026-12-02Z', 1800, 5000, -1)`, constraint: "activities_elevation_gain_meters_check"},
		{name: "unknown source", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, source) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 60, 'fitbit')`, constraint: "activities_source_check"},
		{name: "other without a label", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('` + userID + `', 'other', '2026-12-02Z', 60)`, constraint: "activities_sport_label_by_sport"},
		{name: "label on a catalogue sport", sql: `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ('` + userID + `', 'yoga', 'Hatha', '2026-12-02Z', 60)`, constraint: "activities_sport_label_by_sport"},
		{name: "label too short", sql: `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ('` + userID + `', 'other', 'P', '2026-12-02Z', 60)`, constraint: "activities_sport_label_check"},
		{name: "label too long", sql: `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ('` + userID + `', 'other', repeat('a', 41), '2026-12-02Z', 60)`, constraint: "activities_sport_label_check"},
		{name: "unknown sport", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('` + userID + `', 'curling', '2026-12-02Z', 60)`, constraint: "activities_sport_code_fkey"},
		{name: "unknown user", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('00000000-0000-0000-0000-00000000dead', 'yoga', '2026-12-02Z', 60)`, constraint: "activities_user_id_fkey"},
		{name: "same idempotency key twice", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, idempotency_key) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 60, 'k1'), ('` + userID + `', 'yoga', '2026-12-03Z', 60, 'k1')`, constraint: "activities_user_idempotency_key_key"},
		{name: "same external id twice", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, external_id) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 60, 'x1'), ('` + userID + `', 'yoga', '2026-12-03Z', 60, 'x1')`, constraint: "activities_source_external_id_key"},
		{name: "keys optional", sql: `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ('` + userID + `', 'yoga', '2026-12-02Z', 60), ('` + userID + `', 'yoga', '2026-12-02Z', 60)`},

		// week_closure and elimination (000007)
		{name: "closed week", sql: `INSERT INTO week_closure VALUES ('` + challengeID + `', 1, '2026-12-08 05:00Z')`},
		{name: "week closed twice", sql: `INSERT INTO week_closure VALUES ('` + challengeID + `', 1, '2026-12-08 05:00Z'), ('` + challengeID + `', 1, '2026-12-08 06:00Z')`, constraint: "week_closure_pkey"},
		{name: "week 0 closed", sql: `INSERT INTO week_closure VALUES ('` + challengeID + `', 0, '2026-12-08 05:00Z')`, constraint: "week_closure_week_number_check"},
		{name: "closure of an unknown challenge", sql: `INSERT INTO week_closure VALUES ('00000000-0000-0000-0000-00000000dead', 1, '2026-12-08 05:00Z')`, constraint: "week_closure_challenge_id_fkey"},
		{name: "elimination", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 2, 4000, 6000)`},
		{name: "eliminated twice", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 2, 4000, 6000), ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 3, 0, 7000)`, constraint: "elimination_pkey"},
		{name: "elimination in week 0", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 0, 4000, 6000)`, constraint: "elimination_week_number_check"},
		{name: "elimination in week 1", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 1, 4000, 6000)`, constraint: "elimination_week_number_check"},
		{name: "negative distance run", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 2, -1, 6000)`, constraint: "elimination_distance_meters_check"},
		{name: "negative required distance", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 2, 0, -1)`, constraint: "elimination_required_meters_check"},
		{name: "elimination from an unknown category", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + deadID + `', '` + userID + `', 2, 4000, 6000)`, constraint: "elimination_category_fkey"},
		{name: "elimination of a non-participant", sql: eliminationFixture + `INSERT INTO users (id, auth_subject) VALUES ('` + otherUserID + `', 'schema|2');INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + otherUserID + `', 2, 4000, 6000)`, constraint: "elimination_participant_fkey"},
		{name: "category of another challenge", sql: eliminationFixture + otherChallengeFixture + `INSERT INTO participants VALUES ('` + otherChallengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL);INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + otherChallengeID + `', '` + categoryID + `', '` + userID + `', 2, 4000, 6000)`, constraint: "elimination_category_fkey"},
		{name: "category that is not an elimination", sql: eliminationFixture + `INSERT INTO categories (id, challenge_id, type, position) VALUES ('` + timeCategoryID + `', '` + challengeID + `', 'cumulative_time', 1);INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + timeCategoryID + `', '` + userID + `', 2, 4000, 6000)`, constraint: "elimination_category_fkey"},
		{name: "another category type recorded", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, category_type, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 'cumulative_time', 2, 4000, 6000)`, constraint: "elimination_category_type_check"},
		{name: "elimination of an unknown user", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + deadID + `', 2, 4000, 6000)`, constraint: "elimination_participant_fkey"},
		{name: "deleting a category with eliminations", sql: eliminationFixture + `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('` + challengeID + `', '` + categoryID + `', '` + userID + `', 2, 4000, 6000); DELETE FROM categories WHERE id = '` + categoryID + `'`, constraint: "elimination_category_fkey"},

		// result_snapshot (000008)
		{name: "snapshot of a category", sql: eliminationFixture + snapshotRow(categoryID, challengeID, "elimination")},
		{name: "category snapshotted twice", sql: eliminationFixture + snapshotRow(categoryID, challengeID, "elimination") + snapshotRow(categoryID, challengeID, "elimination"), constraint: "result_snapshot_pkey"},
		{name: "snapshot of an unknown category", sql: snapshotRow(deadID, challengeID, "elimination"), constraint: "result_snapshot_category_fkey"},
		{name: "snapshot with another challenge", sql: eliminationFixture + otherChallengeFixture + snapshotRow(categoryID, otherChallengeID, "elimination"), constraint: "result_snapshot_category_fkey"},
		{name: "snapshot with another type", sql: eliminationFixture + snapshotRow(categoryID, challengeID, "cumulative_time"), constraint: "result_snapshot_category_fkey"},
		{name: "deleting a category with a snapshot", sql: eliminationFixture + snapshotRow(categoryID, challengeID, "elimination") + `DELETE FROM categories WHERE id = '` + categoryID + `'`, constraint: "result_snapshot_category_fkey"},
		// invitations and invitation_recipients (000009)
		{name: "invitation", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL")},
		{name: "revoked invitation", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "'2026-12-02Z'")},
		{name: "token hash not a SHA-256", sql: invitationRow(invitationID, `'t1'::bytea`, "'2026-12-08 05:00Z'", "NULL"), constraint: "invitations_token_hash_check"},
		{name: "same token twice", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL") + invitationRow(otherInvitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL"), constraint: "invitations_token_hash_key"},
		{name: "expired at creation", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-01Z'", "NULL"), constraint: "invitations_expires_after_created_check"},
		{name: "revoked before creation", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "'2026-11-30Z'"), constraint: "invitations_revoked_after_created_check"},
		{name: "invitation to an unknown challenge", sql: `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at) VALUES ('` + deadID + `', '` + userID + `', sha256('t1'::bytea), '2026-12-08 05:00Z', '2026-12-01Z')`, constraint: "invitations_challenge_id_fkey"},
		{name: "invitation by an unknown user", sql: `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at) VALUES ('` + challengeID + `', '` + deadID + `', sha256('t1'::bytea), '2026-12-08 05:00Z', '2026-12-01Z')`, constraint: "invitations_invited_by_fkey"},
		{name: "recipient of an unknown invitation", sql: recipientRow(userID), constraint: "invitation_recipients_invitation_id_fkey"},
		{name: "recipient", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL") + recipientRow(userID)},
		{name: "recipient linked twice", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL") + recipientRow(userID) + recipientRow(userID), constraint: "invitation_recipients_pkey"},
		{name: "unknown recipient", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL") + recipientRow(deadID), constraint: "invitation_recipients_user_id_fkey"},
		{name: "deleting an invitation with recipients", sql: invitationRow(invitationID, `sha256('t1'::bytea)`, "'2026-12-08 05:00Z'", "NULL") + recipientRow(userID) + `DELETE FROM invitations WHERE id = '` + invitationID + `'`, constraint: "invitation_recipients_invitation_id_fkey"},

		// No cascade: a challenge is only soft-deleted, and the rows of a deleted
		// account are erased by the code, never by the database.
		{name: "deleting a challenge with participants", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL); DELETE FROM challenges WHERE id = '` + challengeID + `'`, constraint: "participants_challenge_id_fkey"},
		{name: "deleting a user who created a challenge", sql: `DELETE FROM users WHERE id = '` + userID + `'`, constraint: "challenges_created_by_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := inRolledBackTx(t, pool, fixture+tt.sql)
			var pgErr *pgconn.PgError
			switch {
			case tt.constraint == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tt.constraint == "":
			case !errors.As(err, &pgErr):
				t.Fatalf("error = %v, want constraint %s", err, tt.constraint)
			case pgErr.ConstraintName != tt.constraint:
				t.Fatalf("constraint %q (%s), want %s", pgErr.ConstraintName, pgErr.Message, tt.constraint)
			}
		})
	}
}

// TestNotNull checks the columns the service must always set.
func TestNotNull(t *testing.T) {
	pool := testdb.New(t)
	err := inRolledBackTx(t, pool, fixture+`INSERT INTO participants (challenge_id, user_id) VALUES ('`+challengeID+`', '`+userID+`')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != "joined_at" {
		t.Fatalf("error = %v, want joined_at NOT NULL (no default: set from platform/clock)", err)
	}
}

// TestReferenceSportDefault: a challenge written without its reference
// sport, as by the version deployed before 000013, gets run.
func TestReferenceSportDefault(t *testing.T) {
	pool := testdb.New(t)
	if _, err := pool.Exec(t.Context(), fixture); err != nil {
		t.Fatal(err)
	}
	var reference string
	if err := pool.QueryRow(t.Context(), "SELECT reference_sport FROM challenges WHERE id = $1", challengeID).Scan(&reference); err != nil {
		t.Fatal(err)
	}
	if reference != "run" {
		t.Fatalf("reference sport %q, want run by default", reference)
	}
}

// TestErasureIndexes checks that each query of the deletion of an account
// filtering by user can use the index made for it. The test tables are tiny,
// so sequential scans are disabled: the plan then shows whether an index
// serves the query at all.
func TestErasureIndexes(t *testing.T) {
	pool := testdb.New(t)
	tests := []struct {
		name  string
		query string
		index string
	}{
		{name: "activities", query: "DELETE FROM activities WHERE user_id = '" + userID + "'", index: "activities_user_id_idx"},
		{name: "creator roles", query: "DELETE FROM challenge_creators WHERE user_id = '" + userID + "'", index: "challenge_creators_user_id_idx"},
		{name: "challenges created (LastCreatorChallenges)", query: "SELECT challenge_id FROM challenge_creators WHERE user_id = '" + userID + "'", index: "challenge_creators_user_id_idx"},
		{name: "eliminations", query: "DELETE FROM elimination WHERE user_id = '" + userID + "'", index: "elimination_user_id_challenge_id_idx"},
		// The check of elimination_participant_fkey when a participation is deleted.
		{name: "elimination of a participation", query: "SELECT 1 FROM elimination WHERE challenge_id = '" + challengeID + "' AND user_id = '" + userID + "'", index: "elimination_user_id_challenge_id_idx"},
		{name: "invitations", query: "DELETE FROM invitations WHERE invited_by = '" + userID + "'", index: "invitations_invited_by_idx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err := tx.Exec(t.Context(), "SET LOCAL enable_seqscan = off"); err != nil {
				t.Fatal(err)
			}
			var plan string
			if err := tx.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+tt.query).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan, `"Index Name": "`+tt.index+`"`) {
				t.Fatalf("plan of %q does not use %s: %s", tt.query, tt.index, plan)
			}
		})
	}
}

// TestRenameElimination applies 000012 to rows recorded as run_elimination,
// then rolls it back: the category, its eliminations and its snapshot follow
// the type both ways.
func TestRenameElimination(t *testing.T) {
	pool, _ := testdb.NewEmpty(t)
	ups := migrationFiles(t, "up")
	i := slices.Index(ups, "000012_rename_elimination_category.up.sql")
	if i < 0 {
		t.Fatal("no 000012 up migration")
	}
	run(t, pool, ups[:i], false)
	if _, err := pool.Exec(t.Context(), fixture+strings.ReplaceAll(eliminationFixture, "'elimination'", "'run_elimination'")+
		`INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters) VALUES ('`+challengeID+`', '`+categoryID+`', '`+userID+`', 2, 4000, 6000);`+
		snapshotRow(categoryID, challengeID, "run_elimination")); err != nil {
		t.Fatal(err)
	}

	types := func() []string {
		t.Helper()
		var category, elimination, snapshot string
		if err := pool.QueryRow(t.Context(), `SELECT
			(SELECT type FROM categories WHERE id = $1),
			(SELECT category_type FROM elimination WHERE category_id = $1),
			(SELECT category_type FROM result_snapshot WHERE category_id = $1)`, categoryID).Scan(&category, &elimination, &snapshot); err != nil {
			t.Fatal(err)
		}
		return []string{category, elimination, snapshot}
	}

	run(t, pool, []string{ups[i]}, false)
	if got, want := types(), []string{"elimination", "elimination", "elimination"}; !slices.Equal(got, want) {
		t.Fatalf("types after up = %v, want %v", got, want)
	}
	run(t, pool, []string{"000012_rename_elimination_category.down.sql"}, false)
	if got, want := types(), []string{"run_elimination", "run_elimination", "run_elimination"}; !slices.Equal(got, want) {
		t.Fatalf("types after down = %v, want %v", got, want)
	}
}

// TestDownThenUp rolls every migration back, newest first, checks that
// nothing but the empty schema remains, then applies them again.
func TestDownThenUp(t *testing.T) {
	pool := testdb.New(t) // every up applied
	run(t, pool, migrationFiles(t, "down"), true)

	var tables, functions int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM pg_tables WHERE schemaname = 'public'),
		(SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public')`).Scan(&tables, &functions); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || functions != 0 {
		t.Fatalf("%d tables and %d functions left after every down, want none", tables, functions)
	}

	run(t, pool, migrationFiles(t, "up"), false)
	if err := inRolledBackTx(t, pool, fixture); err != nil {
		t.Fatalf("schema unusable after down then up: %v", err)
	}
}

func inRolledBackTx(t *testing.T, pool *pgxpool.Pool, sql string) error {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// Without arguments, pgx uses the simple protocol: several statements.
	_, err = tx.Exec(t.Context(), sql)
	return err
}

func migrationFiles(t *testing.T, direction string) []string {
	t.Helper()
	files, err := filepath.Glob("*." + direction + ".sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s migration: %v", direction, err)
	}
	slices.Sort(files)
	return files
}

func run(t *testing.T, pool *pgxpool.Pool, files []string, reverse bool) {
	t.Helper()
	if reverse {
		slices.Reverse(files)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f) //nolint:gosec // migrations of this repository
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}
