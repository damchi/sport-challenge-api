package challenge

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Category types, one of each per challenge, in this position order.
const (
	CategoryCumulativeTime     = "cumulative_time"
	CategoryCumulativeDistance = "cumulative_distance"
	CategoryElimination        = "elimination"
)

// Challenge is a row of the challenges table with its categories.
type Challenge struct {
	ID               string `gorm:"->"`
	Title            string
	Description      *string
	Visibility       string
	MembersCanInvite bool
	Timezone         string
	// ReferenceSport is the sport the distances are brought back to and the
	// elimination is run in, frozen at creation.
	ReferenceSport string
	StartsAt       time.Time
	EndsAt         time.Time
	CreatedBy      string
	DeletedAt      *time.Time
	CreatedAt      time.Time `gorm:"->"`
	UpdatedAt      time.Time `gorm:"->"`
	// Categories are not a column: the repository loads them alongside.
	Categories []Category `gorm:"-"`
	// ParticipantCount is not a column either: the repository counts every
	// participation, abandoned included, but those of deleted accounts, when
	// it reads the challenge with a membership.
	ParticipantCount int `gorm:"-"`
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
	// Sport and StepMeters are the elimination settings.
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
	// Creator and Invited are the rest of the actor's membership, read by
	// the query; the service turns the membership into Role.
	Creator bool
	Invited bool
	// Role and Status are computed by the service, Status at the instant of
	// the request.
	Role   Role   `gorm:"-"`
	Status string `gorm:"-"`
	// MyWeek is where the actor stands in the challenge, from the
	// leaderboard: only in their own list, while the challenge runs and they
	// are an active participant; nil otherwise.
	MyWeek *Standing `gorm:"-"`
}

// Standing is where a participant stands in a challenge during its weeks:
// the current week, and their line in each category, in category order.
type Standing struct {
	Week       int
	WeekEndsAt time.Time
	Rankings   []Ranking
}

// Ranking is the line of a participant in a category: their rank, how many
// are ranked, and the live situation in the elimination category (nil in
// the others).
type Ranking struct {
	Type        string
	Rank        int
	RankedCount int
	Progress    *scoring.Progress
}

// Page is a page of a listing; Next is nil on the last page.
type Page struct {
	Items []Summary
	Next  *Position
}

// Participant is a participation in a listing, with the public name of the
// participant (empty when the account was deleted after the listing was
// read).
type Participant struct {
	UserID    string
	Status    ParticipantStatus
	IsCreator bool
	JoinedAt  time.Time
	Name      Name
}

// Name is what a user shows the others: never the e-mail. Each field is nil
// when the account was deleted between the read of the list and that of the
// names, and for a name not set yet.
type Name struct {
	Pseudo    *string
	FirstName *string
	LastName  *string
}

// ParticipantPosition is the place of a participant in the listing: the
// pagination key (joined_at, user_id), encoded in the cursor.
type ParticipantPosition struct {
	JoinedAt time.Time `json:"j"`
	UserID   string    `json:"u"`
}

// ParticipantPage is a page of participants; Next is nil on the last page.
type ParticipantPage struct {
	Items []Participant
	Next  *ParticipantPosition
}

// UserChallenge is a challenge a user joined, with their membership in it:
// what an activity of theirs needs to know where it counts, and how much.
type UserChallenge struct {
	ID       string
	Title    string
	Timezone string
	StartsAt time.Time
	EndsAt   time.Time
	// Factors are those of the cumulative distance category: what an
	// activity brings to it.
	Factors map[string]float64
	Membership
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
