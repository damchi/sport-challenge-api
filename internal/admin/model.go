// Package admin holds the cross-module reads of the administration of the
// app (README §2.12): the figures of the dashboard and every challenge,
// deleted ones included, for the accounts with the admin permission of their
// access token. The admin changes are use cases of each module, not of
// this one.
package admin

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
)

// recentWindow is "the last 7 days" of the dashboard: 7 × 24 hours before
// the instant of the figures, the same for every figure.
const recentWindow = 7 * 24 * time.Hour

// workerTasks are the tasks of the worker, in the order of the dashboard
// (and of the enum of the contract), each listed even when it never ran.
var workerTasks = [...]string{"close-weeks", "finalize-challenges", "retry-auth0-deletions"}

// Stats are the figures of the dashboard at ComputedAt.
type Stats struct {
	ComputedAt time.Time
	Figures
	// WorkerTasks holds one entry per task of workerTasks, in that order.
	WorkerTasks []WorkerTask
	Build       buildinfo.Info
}

// Figures are the counts of the dashboard, read in one statement.
type Figures struct {
	// Deleted accounts count in none of the account figures.
	AccountsTotal  int64
	AccountsNew    int64
	AccountsActive int64
	// The statuses count the challenges not deleted, Deleted the others.
	Upcoming, Ongoing, Finished, Deleted int64
	// Activities recorded in the window, deleted ones and those of deleted
	// accounts excluded.
	Activities            int64
	PendingAuth0Deletions int64
}

// WorkerTask is the last run and the last failure of a worker task, nil when
// it has none.
type WorkerTask struct {
	Task        string
	LastRun     *WorkerRun
	LastFailure *WorkerRun
}

// WorkerRun is a run of a worker task. EndedAt and Outcome are nil while it
// goes on, or when it was stopped before it could end its row; ErrorCode is
// set for a failure only.
type WorkerRun struct {
	StartedAt time.Time
	EndedAt   *time.Time
	Outcome   *string
	ErrorCode *string
}

// The statuses of a challenge, from its dates (statusAt).
const (
	StatusUpcoming = "upcoming"
	StatusOngoing  = "ongoing"
	StatusFinished = "finished"
)

// statusAt is the status of a challenge at now, the bounds of the status
// filter of ListChallenges: upcoming before starts_at, finished from ends_at.
func statusAt(now, startsAt, endsAt time.Time) string {
	switch {
	case now.Before(startsAt):
		return StatusUpcoming
	case now.Before(endsAt):
		return StatusOngoing
	default:
		return StatusFinished
	}
}

// ChallengeQuery selects a page of challenges. The zero value of a filter
// keeps every challenge: Search "" any title, Status and Visibility ""
// any, Deleted nil deleted or not.
type ChallengeQuery struct {
	Search     string
	Status     string
	Visibility string
	Deleted    *bool
	Limit      int
	// After is the position of the last challenge of the previous page.
	After *ChallengePosition
	Now   time.Time
}

// ChallengePosition is the key of the order of the list, most recently
// created first, encoded in the cursor.
type ChallengePosition struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// ChallengeRow is a challenge of the list, as read.
type ChallengeRow struct {
	ID               string
	Title            string
	Visibility       string
	Timezone         string
	StartsAt         time.Time
	EndsAt           time.Time
	CreatedAt        time.Time
	DeletedAt        *time.Time
	ParticipantCount int
}

// UserRef is an account named in a challenge, as a creator sees it; each
// name is nil while the account has not set it.
type UserRef struct {
	ID        string
	Pseudo    *string
	FirstName *string
	LastName  *string
}

// ChallengeSummary is a challenge of the list with its status and
// creators.
type ChallengeSummary struct {
	ChallengeRow
	Status   string
	Creators []UserRef
}

// ChallengePage is a page of the list; Next is nil on the last page.
type ChallengePage struct {
	Items []ChallengeSummary
	Next  *ChallengePosition
}

// ChallengeRecord is a challenge with every setting, deleted or not.
type ChallengeRecord struct {
	ID               string
	Title            string
	Description      *string
	Visibility       string
	MembersCanInvite bool
	Timezone         string
	ReferenceSport   string
	StartsAt         time.Time
	EndsAt           time.Time
	CreatedAt        time.Time
	DeletedAt        *time.Time
	ParticipantCount int
}

// CategoryRow is a category of a challenge; Params is the JSON object of
// categories.params, the shape of the contract's CategoryParams.
type CategoryRow struct {
	Type     string
	Position int
	Params   []byte
}

// EliminationRow is a participant eliminated at WeekNumber.
type EliminationRow struct {
	User           UserRef `gorm:"embedded;embeddedPrefix:user_"`
	WeekNumber     int
	DistanceMeters int
	RequiredMeters int
}

// InvitationRow is an invitation link, its token never kept.
type InvitationRow struct {
	ID        string
	InvitedBy UserRef `gorm:"embedded;embeddedPrefix:invited_by_"`
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	// AuthorCanInvite tells whether its author may still invite: a creator,
	// or an active participant when members may invite.
	AuthorCanInvite bool
}

// ChallengeData is everything stored about a challenge, read in one
// snapshot. ClosedWeeks gives when the worker judged each week it judged.
type ChallengeData struct {
	Challenge       ChallengeRecord
	Categories      []CategoryRow
	Creators        []UserRef
	ClosedWeeks     map[int]time.Time
	Eliminations    []EliminationRow
	ResultsFrozenAt *time.Time
	Invitations     []InvitationRow
}

// The states of an invitation link (invitationStatus).
const (
	InvitationActive    = "active"
	InvitationExpired   = "expired"
	InvitationRevoked   = "revoked"
	InvitationCancelled = "cancelled"
)

// invitationStatus is the state of a link at now: revoked once revoked,
// else expired from expires_at (a link is valid while expires_at > now),
// else cancelled when its author may no longer invite (README §5.7), else
// active.
func invitationStatus(inv InvitationRow, now time.Time) string {
	switch {
	case inv.RevokedAt != nil:
		return InvitationRevoked
	case !now.Before(inv.ExpiresAt):
		return InvitationExpired
	case !inv.AuthorCanInvite:
		return InvitationCancelled
	default:
		return InvitationActive
	}
}

// Week is a week of a challenge and how it was judged: Locked once LocksAt
// is past, ClosedAt when the worker judged it, nil while it has not.
type Week struct {
	Number   int
	StartsAt time.Time
	EndsAt   time.Time
	LocksAt  time.Time
	Locked   bool
	ClosedAt *time.Time
}

// Invitation is an invitation link with its state at the instant of the
// detail.
type Invitation struct {
	InvitationRow
	Status string
}

// ChallengeDetail is what an admin sees of a challenge.
type ChallengeDetail struct {
	Challenge       ChallengeRecord
	Categories      []CategoryRow
	Status          string
	Creators        []UserRef
	Weeks           []Week
	Eliminations    []EliminationRow
	ResultsFrozenAt *time.Time
	Invitations     []Invitation
}
