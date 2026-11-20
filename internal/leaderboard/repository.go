package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// Participants returns every participant of challenge challengeID, active or
// abandoned, in order of arrival. A deleted (anonymized) account is left out:
// it is no longer ranked anywhere, even in a challenge that is over.
func (r *Repository) Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error) {
	var rows []struct {
		UserID string
		Status string
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT p.user_id, p.status FROM participants p
		JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
		WHERE p.challenge_id = @id ORDER BY p.joined_at, p.user_id`,
		map[string]any{"id": challengeID},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read participants: %w", err)
	}
	out := make([]scoring.Participant, len(rows))
	for i, row := range rows {
		out[i] = scoring.Participant{UserID: row.UserID, Abandoned: row.Status == statusAbandoned}
	}
	return out, nil
}

// Activities returns, in one query, the totals of the activities not deleted
// of the active participants of a challenge, started in the window w (those
// of an abandoned participant or a deleted account no longer count), never
// each activity:
//   - for weeklySport, the sport of the elimination category, per participant
//     and week, each total as one activity starting at the start of its week;
//   - for the other sports, per participant and sport, as one activity
//     starting at the start of the window.
//
// Contract: the scorers only sum durations and distances, per participant and
// sport, and per week for the elimination sport, so these totals rank exactly
// as the raw activities do; the rankings are still computed from them, never
// stored. A scorer that would read one activity (a best run, say) needs the
// raw activities instead. The weeks are cut in Go (Window.WeekBounds) and
// given to the database as their starts: one rule for both.
func (r *Repository) Activities(ctx context.Context, challengeID string, w scoring.Window, weeklySport string) ([]scoring.Activity, error) {
	starts := make([]time.Time, w.Weeks())
	literal := make([]string, len(starts))
	for n := range starts {
		starts[n], _ = w.WeekBounds(n + 1)
		literal[n] = starts[n].Format(time.RFC3339Nano)
	}
	var rows []struct {
		UserID          string
		SportCode       string
		Week            int
		DurationSeconds int
		DistanceMeters  *int
	}
	// width_bucket gives the week: the number of starts at or before the
	// activity (0 for the other sports, summed over the window). SUM of a
	// distance is null when none of the group has one.
	err := r.db.WithContext(ctx).Raw(
		`SELECT a.user_id, a.sport_code,
			CASE WHEN a.sport_code = @weekly THEN width_bucket(a.started_at, CAST(@starts AS timestamptz[])) ELSE 0 END AS week,
			SUM(a.duration_seconds) AS duration_seconds, SUM(a.distance_meters) AS distance_meters
		FROM participants p
		JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
		JOIN activities a ON a.user_id = p.user_id
		WHERE p.challenge_id = @id AND p.status = 'active'
			AND a.deleted_at IS NULL AND a.started_at >= @from AND a.started_at < @to
		GROUP BY 1, 2, 3`,
		map[string]any{
			"id": challengeID, "from": w.Start, "to": w.End, "weekly": weeklySport,
			"starts": "{" + strings.Join(literal, ",") + "}",
		},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read activities: %w", err)
	}
	out := make([]scoring.Activity, len(rows))
	for i, row := range rows {
		startedAt := w.Start
		if row.Week > 0 {
			// The WHERE keeps the activities in the window, so in its weeks:
			// anything else means the two cuttings disagree.
			if row.Week > len(starts) {
				return nil, fmt.Errorf("read activities: week %d outside the %d weeks of the window", row.Week, len(starts))
			}
			startedAt = starts[row.Week-1]
		}
		out[i] = scoring.Activity{
			UserID: row.UserID, SportCode: row.SportCode, StartedAt: startedAt,
			DurationSeconds: row.DurationSeconds, DistanceMeters: row.DistanceMeters,
		}
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
