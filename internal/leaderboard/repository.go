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

// Participants returns every participant of challenge challengeID, active or
// abandoned, in order of arrival.
func (r *Repository) Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error) {
	var rows []struct {
		UserID string
		Status string
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT user_id, status FROM participants WHERE challenge_id = @id ORDER BY joined_at, user_id`,
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

// Activities returns, in one query, the activities not deleted of the active
// participants of challenge challengeID started in [from, to): those of an
// abandoned participant no longer count.
func (r *Repository) Activities(ctx context.Context, challengeID string, from, to time.Time) ([]scoring.Activity, error) {
	var rows []struct {
		UserID          string
		SportCode       string
		StartedAt       time.Time
		DurationSeconds int
		DistanceMeters  *int
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT a.user_id, a.sport_code, a.started_at, a.duration_seconds, a.distance_meters
		FROM participants p
		JOIN activities a ON a.user_id = p.user_id
		WHERE p.challenge_id = @id AND p.status = 'active'
			AND a.deleted_at IS NULL AND a.started_at >= @from AND a.started_at < @to`,
		map[string]any{"id": challengeID, "from": from, "to": to},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read activities: %w", err)
	}
	out := make([]scoring.Activity, len(rows))
	for i, row := range rows {
		out[i] = scoring.Activity{
			UserID: row.UserID, SportCode: row.SportCode, StartedAt: row.StartedAt.UTC(),
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
