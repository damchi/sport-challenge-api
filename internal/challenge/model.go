package challenge

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Category types, one of each per challenge, in this position order.
const (
	CategoryCumulativeTime     = "cumulative_time"
	CategoryCumulativeDistance = "cumulative_distance"
	CategoryRunElimination     = "run_elimination"
)

// Challenge is a row of the challenges table with its categories.
type Challenge struct {
	ID               string `gorm:"->"`
	Title            string
	Description      *string
	Visibility       string
	MembersCanInvite bool
	Timezone         string
	StartsAt         time.Time
	EndsAt           time.Time
	CreatedBy        string
	DeletedAt        *time.Time
	CreatedAt        time.Time `gorm:"->"`
	UpdatedAt        time.Time `gorm:"->"`
	// Categories are not a column: the repository loads them alongside.
	Categories []Category `gorm:"-"`
}

// Category is a row of the categories table: a category of a challenge
// with its settings frozen at creation.
type Category struct {
	ID          string `gorm:"->"`
	ChallengeID string
	Type        string
	Position    int
	Params      Params
}

// Params are the frozen settings of a category (column categories.params).
type Params struct {
	// Factors is the distance factor by sport code (cumulative_distance).
	Factors map[string]float64 `json:"factors,omitempty"`
	// Sport and StepMeters are the run elimination settings.
	Sport      string `json:"sport,omitempty"`
	StepMeters int    `json:"step_meters,omitempty"`
}

// Value implements driver.Valuer: params are stored as a JSON object.
func (p Params) Value() (driver.Value, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode category params: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (p *Params) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("category params: not a JSON value")
	}
	if err := json.Unmarshal(b, p); err != nil {
		return fmt.Errorf("decode category params: %w", err)
	}
	return nil
}

// creator is a row of the challenge_creators table.
type creator struct {
	ChallengeID string `gorm:"primaryKey"`
	UserID      string `gorm:"primaryKey"`
}

func (creator) TableName() string { return "challenge_creators" }

// participant is a row of the participants table.
type participant struct {
	ChallengeID string `gorm:"primaryKey"`
	UserID      string `gorm:"primaryKey"`
	Status      ParticipantStatus
	JoinedAt    time.Time
	AbandonedAt *time.Time
}

func (participant) TableName() string { return "participants" }

// Scopes of a listing.
const (
	ScopePublic = "public"
	ScopeMine   = "mine"
)

// Statuses of a challenge, from the current instant.
const (
	StatusUpcoming = "upcoming"
	StatusOngoing  = "ongoing"
	StatusFinished = "finished"
)

// ListQuery selects a page of challenges for an actor. Status is empty for
// every status.
type ListQuery struct {
	ActorID string
	Scope   string
	Status  string
	Now     time.Time
	Limit   int
	// After is the position of the last item of the previous page.
	After *Position
}

// Position is the place of a challenge in a listing: the pagination key
// (starts_at, id), encoded in the cursor.
type Position struct {
	StartsAt time.Time `json:"s"`
	ID       string    `json:"i"`
}

// Summary is a challenge in a listing.
type Summary struct {
	ID               string
	Title            string
	Visibility       string
	Timezone         string
	StartsAt         time.Time
	EndsAt           time.Time
	ParticipantCount int
	// MyStatus is the actor's participation, nil when not a participant.
	MyStatus *string
}

// Page is a page of a listing; Next is nil on the last page.
type Page struct {
	Items []Summary
	Next  *Position
}

// Detail is a challenge as one user sees it, with the fields the server
// computes from the current instant.
type Detail struct {
	Challenge
	Role        Role
	Participant ParticipantStatus
	Status      string
	CanJoin     bool
	// RegistrationDeadline is the end of week 1.
	RegistrationDeadline time.Time
	// CurrentWeek is 0, and WeekEndsAt zero, outside the window.
	CurrentWeek int
	WeekEndsAt  time.Time
}
