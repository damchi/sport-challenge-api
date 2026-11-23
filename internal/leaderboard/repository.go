package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Repository reads what the rankings are computed from. Raw SQL, always
// parameterized: it reads, never writes.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// statusAbandoned is the value of participants.status of a participant who
// left or was excluded.
const statusAbandoned = "abandoned"

// Participants is ParticipantsOf for challenge challengeID alone.
func (r *Repository) Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error) {
	byChallenge, err := r.ParticipantsOf(ctx, []string{challengeID})
	if err != nil {
		return nil, err
	}
	return byChallenge[challengeID], nil
}

// ParticipantsOf returns every participant of each of challengeIDs, active
// or abandoned, in order of arrival, in one query. A deleted (anonymized)
// account is left out: it is no longer ranked anywhere, even in a challenge
// that is over.
func (r *Repository) ParticipantsOf(ctx context.Context, challengeIDs []string) (map[string][]scoring.Participant, error) {
	out := make(map[string][]scoring.Participant, len(challengeIDs))
	if len(challengeIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ChallengeID string
		UserID      string
		Status      string
		AbandonedAt *time.Time
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT p.challenge_id, p.user_id, p.status, p.abandoned_at FROM participants p
		JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
		WHERE p.challenge_id IN @ids ORDER BY p.challenge_id, p.joined_at, p.user_id`,
		map[string]any{"ids": challengeIDs},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read participants: %w", err)
	}
	for _, row := range rows {
		p := scoring.Participant{UserID: row.UserID, Abandoned: row.Status == statusAbandoned}
		if p.Abandoned && row.AbandonedAt != nil {
			at := row.AbandonedAt.UTC()
			p.AbandonedAt = &at
		}
		out[row.ChallengeID] = append(out[row.ChallengeID], p)
	}
	return out, nil
}

// Scope is what the activities of a challenge are read for: its window, the
// sport of its elimination category, summed per week, its points category,
// nil without one, and whether its elevation gains are summed: for the
// cumulative elevation category, or the weekly elevation criterion of the
// points category.
type Scope struct {
	ChallengeID string
	Window      scoring.Window
	WeeklySport string
	Points      *PointsRead
	Elevation   bool
}

// PointsRead is what the points category needs read: every activity grouped
// per week, with the longest sessions and the count of those lasting at
// least MinSessionSeconds.
type PointsRead struct {
	MinSessionSeconds int
}

// Activities is ActivitiesOf for challenge challengeID alone, in window w
// with weeklySport summed per week, the groups of a points category whose
// minimum session is minSessionSeconds (0 without such a category), and the
// elevation gains summed when elevation (the cumulative elevation category,
// or the weekly elevation criterion of the points category).
func (r *Repository) Activities(ctx context.Context, challengeID string, w scoring.Window, weeklySport string, minSessionSeconds int, elevation bool) ([]scoring.Activity, error) {
	sc := Scope{ChallengeID: challengeID, Window: w, WeeklySport: weeklySport, Elevation: elevation}
	if minSessionSeconds > 0 {
		sc.Points = &PointsRead{MinSessionSeconds: minSessionSeconds}
	}
	byChallenge, err := r.ActivitiesOf(ctx, []Scope{sc})
	if err != nil {
		return nil, err
	}
	return byChallenge[challengeID], nil
}

// scopeRow is a Scope as the query of ActivitiesOf reads it (JSON).
type scopeRow struct {
	ChallengeID string      `json:"challenge_id"`
	From        time.Time   `json:"from_at"`
	To          time.Time   `json:"to_at"`
	Starts      []time.Time `json:"starts"`
	Weekly      string      `json:"weekly"`
	Points      bool        `json:"points"`
	MinSeconds  int         `json:"min_seconds"`
	// Elevation is not sent to the database: it only says, once read,
	// whether the totals of the challenge carry their elevation gains.
	Elevation bool `json:"-"`
}

// pointsColumns are the columns ActivitiesOf adds for the points category:
// the longest session of a group by duration and by distance, and how many
// of its sessions last at least the minimum.
const pointsColumns = `,
			MAX(a.duration_seconds) AS longest_seconds, MAX(a.distance_meters) AS longest_meters,
			COUNT(*) FILTER (WHERE a.duration_seconds >= w.min_seconds) AS sessions`

// elevationColumn is the column ActivitiesOf adds for the cumulative
// elevation category and the weekly elevation criterion of the points
// category: the sum of the elevation gains of a group, every session counted
// whatever its duration, null when none of it has one.
const elevationColumn = `,
			SUM(a.elevation_gain_meters) AS elevation_meters`

// ActivitiesOf returns, in one query, the totals of the activities not
// deleted of the active participants of each challenge, started in its
// window (those of an abandoned participant or a deleted account no longer
// count), never each activity:
//   - for the weekly sport, the sport of the elimination category, per
//     participant and week, each total as one activity starting at the start
//     of its week;
//   - for the other sports, per participant and sport, as one activity
//     starting at the start of the window.
//
// For a challenge with a points category (Scope.Points), every sport is
// grouped per participant, week and sport, and each group also carries its
// longest session by duration and by distance and how many of its sessions
// last at least the minimum (Activity.LongestSeconds, LongestMeters,
// Sessions; zero for the other challenges). For a challenge whose elevation
// gains are read (Scope.Elevation: the cumulative elevation category, or the
// weekly elevation criterion of the points category), each total or group
// also carries the sum of its elevation gains (Activity.ElevationMeters; nil
// for the other challenges). With a points category, the activities of the
// abandoned participants are read too, the points category keeping them in
// the weeks locked before they left; the other scorers do not count them.
// Those of the deleted accounts never are.
//
// Contract: the scorers only sum durations, distances and elevation gains,
// per participant and sport, and per week for the elimination sport, and the
// points category only reads the longest session, the count of a
// participant's sessions and the sum of their elevation gains per week and
// sport, so these groups rank exactly as the raw activities do (the
// equivalent distance of a session is its distance times the factor of its
// sport, so the longest of a sport is its longest distance); the
// rankings are still computed from them, never stored. A scorer that would
// read anything else of one activity needs the raw activities instead. The
// weeks are cut in Go (Window.WeekBounds) and given to the database as their
// starts: one rule for both. Each challenge has its own window and weeks,
// given to the database as rows.
func (r *Repository) ActivitiesOf(ctx context.Context, scopes []Scope) (map[string][]scoring.Activity, error) {
	out := make(map[string][]scoring.Activity, len(scopes))
	if len(scopes) == 0 {
		return out, nil
	}
	rowsIn := make([]scopeRow, len(scopes))
	byID := make(map[string]scopeRow, len(scopes))
	// The columns of the points and elevation categories are only read
	// when one of the challenges has it: without, the read is the one of the
	// totals alone.
	groupColumns, elevationColumns := "", ""
	for i, sc := range scopes {
		starts := make([]time.Time, sc.Window.Weeks())
		for n := range starts {
			starts[n], _ = sc.Window.WeekBounds(n + 1)
		}
		rowsIn[i] = scopeRow{ChallengeID: sc.ChallengeID, From: sc.Window.Start, To: sc.Window.End, Starts: starts, Weekly: sc.WeeklySport}
		if sc.Points != nil {
			rowsIn[i].Points, rowsIn[i].MinSeconds = true, sc.Points.MinSessionSeconds
			groupColumns = pointsColumns
		}
		if sc.Elevation {
			rowsIn[i].Elevation = true
			elevationColumns = elevationColumn
		}
		byID[sc.ChallengeID] = rowsIn[i]
	}
	windows, err := json.Marshal(rowsIn)
	if err != nil {
		return nil, fmt.Errorf("encode windows: %w", err)
	}
	var rows []struct {
		ChallengeID     string
		UserID          string
		SportCode       string
		Week            int
		DurationSeconds int
		DistanceMeters  *int
		LongestSeconds  int
		LongestMeters   *int
		Sessions        int
		ElevationMeters *int
	}
	// width_bucket gives the week: the number of starts at or before the
	// activity (0 for the other sports, summed over the window, without a
	// points category). SUM and MAX of a distance are null when none of the
	// group has one, as is SUM of an elevation gain. groupColumns and
	// elevationColumns are each one of two constants, never a value.
	err = r.db.WithContext(ctx).Raw(
		`SELECT w.challenge_id, a.user_id, a.sport_code,
			CASE WHEN w.points OR a.sport_code = w.weekly THEN width_bucket(a.started_at, w.starts) ELSE 0 END AS week,
			SUM(a.duration_seconds) AS duration_seconds, SUM(a.distance_meters) AS distance_meters`+groupColumns+elevationColumns+`
		FROM jsonb_to_recordset(CAST(@windows AS jsonb))
			AS w(challenge_id uuid, from_at timestamptz, to_at timestamptz, starts timestamptz[], weekly text,
				points boolean, min_seconds integer)
		JOIN participants p ON p.challenge_id = w.challenge_id AND (p.status = 'active' OR w.points)
		JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
		JOIN activities a ON a.user_id = p.user_id
		WHERE a.deleted_at IS NULL AND a.started_at >= w.from_at AND a.started_at < w.to_at
		GROUP BY 1, 2, 3, 4`,
		map[string]any{"windows": string(windows)},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read activities: %w", err)
	}
	for _, row := range rows {
		sc := byID[row.ChallengeID]
		startedAt := sc.From
		if row.Week > 0 {
			// The WHERE keeps the activities in the window, so in its weeks:
			// anything else means the two cuttings disagree.
			if row.Week > len(sc.Starts) {
				return nil, fmt.Errorf("read activities: week %d outside the %d weeks of the window", row.Week, len(sc.Starts))
			}
			startedAt = sc.Starts[row.Week-1]
		}
		a := scoring.Activity{
			UserID: row.UserID, SportCode: row.SportCode, StartedAt: startedAt,
			DurationSeconds: row.DurationSeconds, DistanceMeters: row.DistanceMeters,
		}
		if sc.Points {
			a.LongestSeconds, a.LongestMeters, a.Sessions = row.LongestSeconds, row.LongestMeters, row.Sessions
		}
		if sc.Elevation {
			a.ElevationMeters = row.ElevationMeters
		}
		out[row.ChallengeID] = append(out[row.ChallengeID], a)
	}
	return out, nil
}

// DeletedAccounts returns which of ids are deleted (anonymized) accounts.
func (r *Repository) DeletedAccounts(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(ids) == 0 {
		return out, nil
	}
	var deleted []string
	err := r.db.WithContext(ctx).Raw(
		`SELECT id FROM users WHERE id IN @ids AND anonymized_at IS NOT NULL`,
		map[string]any{"ids": ids},
	).Scan(&deleted).Error
	if err != nil {
		return nil, fmt.Errorf("read deleted accounts: %w", err)
	}
	for _, id := range deleted {
		out[id] = true
	}
	return out, nil
}

// Snapshots returns the frozen ranking of each category of challenge
// challengeID by category type, and when they were frozen; none until the
// worker has finalized the challenge. The time is the latest of the rows.
func (r *Repository) Snapshots(ctx context.Context, challengeID string) (map[string]json.RawMessage, time.Time, error) {
	var rows []struct {
		CategoryType string
		Payload      []byte
		CreatedAt    time.Time
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT category_type, payload, created_at FROM result_snapshot WHERE challenge_id = @id ORDER BY created_at`,
		map[string]any{"id": challengeID},
	).Scan(&rows).Error
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read result snapshots: %w", err)
	}
	out := make(map[string]json.RawMessage, len(rows))
	var createdAt time.Time
	for _, row := range rows {
		out[row.CategoryType] = row.Payload
		createdAt = row.CreatedAt.UTC()
	}
	return out, createdAt, nil
}

// SnapshotsOf returns the frozen ranking of each category of challengeIDs,
// by challenge ID then category type; nothing for a challenge the worker has
// not finalized yet.
func (r *Repository) SnapshotsOf(ctx context.Context, challengeIDs []string) (map[string]map[string]json.RawMessage, error) {
	out := make(map[string]map[string]json.RawMessage)
	if len(challengeIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ChallengeID  string
		CategoryType string
		Payload      []byte
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT challenge_id, category_type, payload FROM result_snapshot WHERE challenge_id IN @ids`,
		map[string]any{"ids": challengeIDs},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read result snapshots: %w", err)
	}
	for _, row := range rows {
		if out[row.ChallengeID] == nil {
			out[row.ChallengeID] = make(map[string]json.RawMessage)
		}
		out[row.ChallengeID][row.CategoryType] = row.Payload
	}
	return out, nil
}
