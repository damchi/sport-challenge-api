package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository reads the tables of every module for the admin reads, and
// writes the journal of the admin changes (admin_actions), its only write.
// Raw SQL, always parameterized.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Figures counts, in one statement (one snapshot), the accounts, the
// challenges at now, the activities recorded since since and the pending
// Auth0 deletions. A deleted account (anonymized_at) counts nowhere, its
// activities neither (they are erased with it).
func (r *Repository) Figures(ctx context.Context, now, since time.Time) (Figures, error) {
	var out Figures
	err := r.db.WithContext(ctx).Raw(`SELECT
		(SELECT count(*) FROM users WHERE anonymized_at IS NULL) AS accounts_total,
		(SELECT count(*) FROM users WHERE anonymized_at IS NULL AND created_at >= @since) AS accounts_new,
		(SELECT count(DISTINCT a.user_id) FROM activities a JOIN users u ON u.id = a.user_id
			WHERE u.anonymized_at IS NULL AND a.deleted_at IS NULL AND a.created_at >= @since) AS accounts_active,
		c.upcoming, c.ongoing, c.finished, c.deleted,
		(SELECT count(*) FROM activities a JOIN users u ON u.id = a.user_id
			WHERE u.anonymized_at IS NULL AND a.deleted_at IS NULL AND a.created_at >= @since) AS activities,
		(SELECT count(*) FROM pending_auth0_deletions) AS pending_auth0_deletions
		FROM (SELECT
			count(*) FILTER (WHERE deleted_at IS NULL AND starts_at > @now) AS upcoming,
			count(*) FILTER (WHERE deleted_at IS NULL AND starts_at <= @now AND ends_at > @now) AS ongoing,
			count(*) FILTER (WHERE deleted_at IS NULL AND ends_at <= @now) AS finished,
			count(*) FILTER (WHERE deleted_at IS NOT NULL) AS deleted
			FROM challenges) c`,
		map[string]any{"now": now, "since": since},
	).Scan(&out).Error
	if err != nil {
		return Figures{}, fmt.Errorf("count dashboard figures: %w", err)
	}
	return out, nil
}

// TaskRun is a run of a task: its last run, or its last failure when
// Failure.
type TaskRun struct {
	Task    string
	Failure bool
	Run     WorkerRun `gorm:"embedded"`
}

// LastWorkerRuns returns the last run of each task that ran, whatever its
// outcome (one still going on included), and the last failure of each task
// that failed. Both follow the order of the trimming of the worker
// (started_at, then id), on the index worker_runs_task_started_at_idx.
func (r *Repository) LastWorkerRuns(ctx context.Context) ([]TaskRun, error) {
	var out []TaskRun
	err := r.db.WithContext(ctx).Raw(`(SELECT DISTINCT ON (task) task, false AS failure, started_at, ended_at, outcome, error_code
			FROM worker_runs ORDER BY task, started_at DESC, id DESC)
		UNION ALL
		(SELECT DISTINCT ON (task) task, true AS failure, started_at, ended_at, outcome, error_code
			FROM worker_runs WHERE outcome = 'failed' ORDER BY task, started_at DESC, id DESC)`,
	).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("read last worker runs: %w", err)
	}
	return out, nil
}

// participantCountColumn counts the participations of challenge c,
// abandoned included, but those of deleted accounts: the count of module
// challenge.
const participantCountColumn = `(SELECT count(*) FROM participants p JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
	WHERE p.challenge_id = c.id)`

// likePattern makes search a pattern of ILIKE … ESCAPE '\' that matches
// the texts holding it: its wildcards are plain characters.
func likePattern(search string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search) + "%"
}

// ListChallenges returns at most q.Limit challenges of q, deleted ones
// included, filtered in SQL, most recently created first; the key
// (created_at, id) makes the order total. The status bounds are those of
// statusAt.
func (r *Repository) ListChallenges(ctx context.Context, q ChallengeQuery) ([]ChallengeRow, error) {
	var query strings.Builder
	query.WriteString(`SELECT c.id, c.title, c.visibility, c.timezone, c.starts_at, c.ends_at, c.created_at, c.deleted_at,
		` + participantCountColumn + ` AS participant_count
		FROM challenges c WHERE true`)
	args := map[string]any{"now": q.Now, "limit": q.Limit}
	if q.Search != "" {
		query.WriteString(` AND c.title ILIKE @search ESCAPE '\'`)
		args["search"] = likePattern(q.Search)
	}
	switch q.Status {
	case StatusUpcoming:
		query.WriteString(" AND c.starts_at > @now")
	case StatusOngoing:
		query.WriteString(" AND c.starts_at <= @now AND c.ends_at > @now")
	case StatusFinished:
		query.WriteString(" AND c.ends_at <= @now")
	}
	if q.Visibility != "" {
		query.WriteString(" AND c.visibility = @visibility")
		args["visibility"] = q.Visibility
	}
	if q.Deleted != nil {
		if *q.Deleted {
			query.WriteString(" AND c.deleted_at IS NOT NULL")
		} else {
			query.WriteString(" AND c.deleted_at IS NULL")
		}
	}
	if q.After != nil {
		query.WriteString(" AND (c.created_at, c.id) < (@after_created, @after_id)")
		args["after_created"], args["after_id"] = q.After.CreatedAt, q.After.ID
	}
	query.WriteString(" ORDER BY c.created_at DESC, c.id DESC LIMIT @limit")

	var out []ChallengeRow
	if err := r.db.WithContext(ctx).Raw(query.String(), args).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("list challenges: %w", err)
	}
	return out, nil
}

// creatorRow is a creator of a challenge.
type creatorRow struct {
	ChallengeID string
	UserRef     `gorm:"embedded"`
}

// creatorsQuery reads the creators of the challenges @ids, oldest role
// first. A deleted account has no creator role left (README §2.2).
const creatorsQuery = `SELECT cc.challenge_id, u.id, u.pseudo, u.first_name, u.last_name
	FROM challenge_creators cc JOIN users u ON u.id = cc.user_id
	WHERE cc.challenge_id IN @ids ORDER BY cc.challenge_id, cc.created_at, u.id`

// CreatorsOf returns the creators of each of ids, oldest role first, in one
// query.
func (r *Repository) CreatorsOf(ctx context.Context, ids []string) (map[string][]UserRef, error) {
	out := make(map[string][]UserRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	creators, err := readCreators(r.db.WithContext(ctx), ids)
	if err != nil {
		return nil, err
	}
	for _, c := range creators {
		out[c.ChallengeID] = append(out[c.ChallengeID], c.UserRef)
	}
	return out, nil
}

func readCreators(tx *gorm.DB, ids []string) ([]creatorRow, error) {
	var rows []creatorRow
	if err := tx.Raw(creatorsQuery, map[string]any{"ids": ids}).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read creators: %w", err)
	}
	return rows, nil
}

// errChallengeNotFound means no challenge with this id, deleted or not.
var errChallengeNotFound = errors.New("challenge not found")

// closedWeek is a week the worker judged.
type closedWeek struct {
	WeekNumber int
	ClosedAt   time.Time
}

// Challenge reads everything stored about challenge id, deleted or not, in
// one read-only REPEATABLE READ transaction, so that every part comes from
// the same snapshot. errChallengeNotFound when there is none.
func (r *Repository) Challenge(ctx context.Context, id string) (ChallengeData, error) {
	var out ChallengeData
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		args := map[string]any{"id": id}
		var found []ChallengeRecord
		if err := tx.Raw(`SELECT c.id, c.title, c.description, c.visibility, c.members_can_invite, c.timezone,
			c.reference_sport, c.starts_at, c.ends_at, c.created_at, c.deleted_at,
			`+participantCountColumn+` AS participant_count
			FROM challenges c WHERE c.id = @id`, args).Scan(&found).Error; err != nil {
			return fmt.Errorf("read challenge: %w", err)
		}
		if len(found) == 0 {
			return errChallengeNotFound
		}
		out.Challenge = found[0]

		if err := tx.Raw(`SELECT type, position, params FROM categories WHERE challenge_id = @id ORDER BY position`, args).
			Scan(&out.Categories).Error; err != nil {
			return fmt.Errorf("read categories: %w", err)
		}
		creators, err := readCreators(tx, []string{id})
		if err != nil {
			return err
		}
		for _, c := range creators {
			out.Creators = append(out.Creators, c.UserRef)
		}

		var weeks []closedWeek
		if err := tx.Raw(`SELECT week_number, closed_at FROM week_closure WHERE challenge_id = @id`, args).
			Scan(&weeks).Error; err != nil {
			return fmt.Errorf("read closed weeks: %w", err)
		}
		out.ClosedWeeks = make(map[int]time.Time, len(weeks))
		for _, w := range weeks {
			out.ClosedWeeks[w.WeekNumber] = w.ClosedAt
		}

		if err := tx.Raw(`SELECT u.id AS user_id, u.pseudo AS user_pseudo, u.first_name AS user_first_name,
			u.last_name AS user_last_name, e.week_number, e.distance_meters, e.required_meters
			FROM elimination e JOIN users u ON u.id = e.user_id
			WHERE e.challenge_id = @id ORDER BY e.week_number, u.id`, args).Scan(&out.Eliminations).Error; err != nil {
			return fmt.Errorf("read eliminations: %w", err)
		}
		var frozen struct{ FrozenAt *time.Time }
		if err := tx.Raw(`SELECT min(created_at) AS frozen_at FROM result_snapshot WHERE challenge_id = @id`, args).
			Scan(&frozen).Error; err != nil {
			return fmt.Errorf("read frozen results: %w", err)
		}
		out.ResultsFrozenAt = frozen.FrozenAt
		recent, err := readActions(tx, ActionQuery{ChallengeID: id, Limit: recentActions})
		if err != nil {
			return err
		}
		out.RecentActions = recent
		// The rule of the invitation links (module invitation, linkQuery).
		if err := tx.Raw(`SELECT i.id, u.id AS invited_by_id, u.pseudo AS invited_by_pseudo,
			u.first_name AS invited_by_first_name, u.last_name AS invited_by_last_name,
			i.created_at, i.expires_at, i.revoked_at,
			(EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = i.invited_by)
				OR (c.members_can_invite AND EXISTS (SELECT 1 FROM participants
					WHERE challenge_id = c.id AND user_id = i.invited_by AND status = 'active'))) AS author_can_invite
			FROM invitations i JOIN challenges c ON c.id = i.challenge_id JOIN users u ON u.id = i.invited_by
			WHERE i.challenge_id = @id ORDER BY i.created_at DESC, i.id DESC`, args).Scan(&out.Invitations).Error; err != nil {
			return fmt.Errorf("read invitations: %w", err)
		}
		return nil
	}, opts)
	if err != nil {
		return ChallengeData{}, err
	}
	return out, nil
}

// userColumns reads an account of users u as an admin sees it. Its state
// follows the deletion: active until anonymized_at, then pending while
// pending_auth0_deletions holds its row (removed once Auth0 deleted the
// identity), deleted after. Its challenges are those it joined or creates,
// deleted ones included (ListUserChallenges).
const userColumns = `u.id, u.pseudo, u.first_name, u.last_name, u.email, u.created_at, u.anonymized_at AS deleted_at,
	CASE WHEN u.anonymized_at IS NULL THEN 'active'
		WHEN EXISTS (SELECT 1 FROM pending_auth0_deletions d WHERE d.user_id = u.id) THEN 'auth0_deletion_pending'
		ELSE 'deleted' END AS state,
	(SELECT max(a.created_at) FROM activities a WHERE a.user_id = u.id AND a.deleted_at IS NULL) AS last_activity_at,
	(SELECT count(*) FROM (SELECT challenge_id FROM participants WHERE user_id = u.id
		UNION SELECT challenge_id FROM challenge_creators WHERE user_id = u.id) m) AS challenge_count`

// ListUsers returns at most q.Limit accounts of q, deleted ones included,
// most recently created first; the key (created_at, id) makes the order
// total. The search looks in the pseudo, the names and the e-mail.
func (r *Repository) ListUsers(ctx context.Context, q UserQuery) ([]UserRow, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + userColumns + ` FROM users u WHERE true`)
	args := map[string]any{"limit": q.Limit}
	if q.Search != "" {
		query.WriteString(` AND (u.pseudo ILIKE @search ESCAPE '\' OR u.first_name ILIKE @search ESCAPE '\'
			OR u.last_name ILIKE @search ESCAPE '\' OR u.email ILIKE @search ESCAPE '\')`)
		args["search"] = likePattern(q.Search)
	}
	if q.After != nil {
		query.WriteString(" AND (u.created_at, u.id) < (@after_created, @after_id)")
		args["after_created"], args["after_id"] = q.After.CreatedAt, q.After.ID
	}
	query.WriteString(" ORDER BY u.created_at DESC, u.id DESC LIMIT @limit")

	var out []UserRow
	if err := r.db.WithContext(ctx).Raw(query.String(), args).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

// errUserNotFound means no account with this id, deleted or not.
var errUserNotFound = errors.New("user not found")

// User reads account id, deleted or not; errUserNotFound when there is
// none.
func (r *Repository) User(ctx context.Context, id string) (UserRow, error) {
	var found []UserRow
	if err := r.db.WithContext(ctx).Raw(`SELECT `+userColumns+` FROM users u WHERE u.id = @id`, map[string]any{"id": id}).
		Scan(&found).Error; err != nil {
		return UserRow{}, fmt.Errorf("read user: %w", err)
	}
	if len(found) == 0 {
		return UserRow{}, errUserNotFound
	}
	return found[0], nil
}

// ofUser runs read in one read-only transaction once account id is known
// to exist, so that an unknown account (errUserNotFound) is told from one
// with an empty list.
func (r *Repository) ofUser(ctx context.Context, id string, read func(tx *gorm.DB) error) error {
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var exists bool
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM users WHERE id = @id)`, map[string]any{"id": id}).
			Scan(&exists).Error; err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		if !exists {
			return errUserNotFound
		}
		return read(tx)
	}, opts)
}

// ListUserChallenges returns at most q.Limit challenges account id joined
// or creates, deleted ones included, most recently created first, with its
// role in each; errUserNotFound when there is no such account.
func (r *Repository) ListUserChallenges(ctx context.Context, id string, q PageQuery) ([]UserChallengeRow, error) {
	var query strings.Builder
	query.WriteString(`SELECT c.id, c.title, c.visibility, c.starts_at, c.ends_at, c.created_at, c.deleted_at,
		cc.user_id IS NOT NULL AS is_creator, p.status AS participant_status, p.joined_at, p.abandoned_at
		FROM (SELECT challenge_id FROM participants WHERE user_id = @id
			UNION SELECT challenge_id FROM challenge_creators WHERE user_id = @id) m
		JOIN challenges c ON c.id = m.challenge_id
		LEFT JOIN participants p ON p.challenge_id = c.id AND p.user_id = @id
		LEFT JOIN challenge_creators cc ON cc.challenge_id = c.id AND cc.user_id = @id
		WHERE true`)
	args := map[string]any{"id": id, "limit": q.Limit}
	if q.After != nil {
		query.WriteString(" AND (c.created_at, c.id) < (@after_created, @after_id)")
		args["after_created"], args["after_id"] = q.After.CreatedAt, q.After.ID
	}
	query.WriteString(" ORDER BY c.created_at DESC, c.id DESC LIMIT @limit")

	var out []UserChallengeRow
	err := r.ofUser(ctx, id, func(tx *gorm.DB) error {
		if err := tx.Raw(query.String(), args).Scan(&out).Error; err != nil {
			return fmt.Errorf("list user challenges: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListUserActivities returns at most q.Limit activities of account id,
// deleted ones excluded, the most recent session first; the key
// (started_at, id) makes the order total, on the index
// activities_user_started_at_idx. errUserNotFound when there is no such
// account.
func (r *Repository) ListUserActivities(ctx context.Context, id string, q ActivityQuery) ([]ActivityRow, error) {
	var query strings.Builder
	query.WriteString(`SELECT id, sport_code, sport_label, started_at, duration_seconds, distance_meters,
		elevation_gain_meters, source, created_at
		FROM activities WHERE user_id = @id AND deleted_at IS NULL`)
	args := map[string]any{"id": id, "limit": q.Limit}
	if q.After != nil {
		query.WriteString(" AND (started_at, id) < (@after_started, @after_id)")
		args["after_started"], args["after_id"] = q.After.StartedAt, q.After.ID
	}
	query.WriteString(" ORDER BY started_at DESC, id DESC LIMIT @limit")

	var out []ActivityRow
	err := r.ofUser(ctx, id, func(tx *gorm.DB) error {
		if err := tx.Raw(query.String(), args).Scan(&out).Error; err != nil {
			return fmt.Errorf("list user activities: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var _ journalStore = (*Repository)(nil)

// InsertAction writes a to the journal, in the transaction ctx carries
// (db.InTx). Its challenges travel as an array literal of uuids, cast by
// PostgreSQL: each one is checked here, so that none can add an element.
func (r *Repository) InsertAction(ctx context.Context, a ActionRecord) error {
	for _, id := range a.ChallengeIDs {
		if _, err := uuid.Parse(id); err != nil || strings.ContainsAny(id, ",{}\" \\") {
			return fmt.Errorf("admin action challenge id %q is not a uuid", id)
		}
	}
	err := db.Conn(ctx, r.db).Exec(`INSERT INTO admin_actions
		(admin_id, created_at, action, target_type, target_id, challenge_ids, old_value, new_value, reason)
		VALUES (@admin_id, @created_at, @action, @target_type, @target_id, CAST(@challenge_ids AS uuid[]),
			CAST(@old_value AS jsonb), CAST(@new_value AS jsonb), @reason)`,
		map[string]any{
			"admin_id": a.AdminID, "created_at": a.CreatedAt, "action": a.Action,
			"target_type": a.TargetType, "target_id": a.TargetID,
			"challenge_ids": "{" + strings.Join(a.ChallengeIDs, ",") + "}",
			"old_value":     jsonParam(a.OldValue), "new_value": jsonParam(a.NewValue), "reason": a.Reason,
		},
	).Error
	if err != nil {
		return fmt.Errorf("insert admin action: %w", err)
	}
	return nil
}

// jsonParam is a JSON value as a parameter: its text, nil (NULL) for none.
func jsonParam(v []byte) any {
	if v == nil {
		return nil
	}
	return string(v)
}

// ListActions returns at most q.Limit actions of the journal of q, most
// recent first; the key (created_at, id) makes the order total.
func (r *Repository) ListActions(ctx context.Context, q ActionQuery) ([]Action, error) {
	return readActions(r.db.WithContext(ctx), q)
}

// actionRow is an action as read: its challenges as a JSON array.
type actionRow struct {
	ID           string
	Admin        UserRef `gorm:"embedded;embeddedPrefix:admin_"`
	CreatedAt    time.Time
	Action       string
	TargetType   string
	TargetID     string
	ChallengeIDs []byte
	OldValue     []byte
	NewValue     []byte
	Reason       string
}

// readActions reads at most q.Limit actions of q on tx, filtered in SQL.
// The admin is named as a creator sees an account: a deleted one is never
// named (its names are erased with it).
func readActions(tx *gorm.DB, q ActionQuery) ([]Action, error) {
	var query strings.Builder
	query.WriteString(`SELECT a.id, u.id AS admin_id, u.pseudo AS admin_pseudo, u.first_name AS admin_first_name,
		u.last_name AS admin_last_name, a.created_at, a.action, a.target_type, a.target_id,
		array_to_json(a.challenge_ids) AS challenge_ids, a.old_value, a.new_value, a.reason
		FROM admin_actions a JOIN users u ON u.id = a.admin_id WHERE true`)
	args := map[string]any{"limit": q.Limit}
	if q.TargetType != "" {
		query.WriteString(" AND a.target_type = @target_type")
		args["target_type"] = q.TargetType
	}
	if q.TargetID != "" {
		query.WriteString(" AND a.target_id = @target_id")
		args["target_id"] = q.TargetID
	}
	if q.AdminID != "" {
		query.WriteString(" AND a.admin_id = @admin_id")
		args["admin_id"] = q.AdminID
	}
	if q.ChallengeID != "" {
		// @> on the GIN index admin_actions_challenge_ids_idx.
		query.WriteString(" AND a.challenge_ids @> ARRAY[CAST(@challenge_id AS uuid)]")
		args["challenge_id"] = q.ChallengeID
	}
	if q.After != nil {
		query.WriteString(" AND (a.created_at, a.id) < (@after_created, @after_id)")
		args["after_created"], args["after_id"] = q.After.CreatedAt, q.After.ID
	}
	query.WriteString(" ORDER BY a.created_at DESC, a.id DESC LIMIT @limit")

	var rows []actionRow
	if err := tx.Raw(query.String(), args).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read admin actions: %w", err)
	}
	out := make([]Action, len(rows))
	for i, r := range rows {
		var ids []string
		if err := json.Unmarshal(r.ChallengeIDs, &ids); err != nil {
			return nil, fmt.Errorf("admin action challenges: %w", err)
		}
		out[i] = Action{
			ID: r.ID, Admin: r.Admin, CreatedAt: r.CreatedAt, Action: r.Action,
			TargetType: r.TargetType, TargetID: r.TargetID, ChallengeIDs: ids,
			OldValue: r.OldValue, NewValue: r.NewValue, Reason: r.Reason,
		}
	}
	return out, nil
}
