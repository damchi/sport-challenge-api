package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/textx"
)

// The bounds of the reason of an admin change, in characters (README §2.12).
const (
	minReason = 3
	maxReason = 500
)

// The targets of an admin change (Entry.TargetType), those of the contract's
// AdminActionTargetType.
const (
	TargetChallenge   = "challenge"
	TargetParticipant = "participant"
	TargetActivity    = "activity"
	TargetUser        = "user"
)

// Entry is an admin change to record.
type Entry struct {
	AdminID string
	// Action is a stable code of the use case (challenge.update...).
	Action     string
	TargetType string
	// TargetID is the id of the target; that of the account for a
	// participant, whose challenge is in ChallengeIDs.
	TargetID string
	// ChallengeIDs are every challenge the change touches.
	ChallengeIDs []string
	// OldValue and NewValue hold the changed fields only; nil when the
	// change created (OldValue) or removed (NewValue) the target. Never a
	// token nor a secret: the journal is read back as is.
	OldValue map[string]any
	NewValue map[string]any
	Reason   string
}

// ActionRecord is an entry as stored: reason trimmed, values in JSON (nil
// for none), at CreatedAt.
type ActionRecord struct {
	AdminID      string
	CreatedAt    time.Time
	Action       string
	TargetType   string
	TargetID     string
	ChallengeIDs []string
	OldValue     []byte
	NewValue     []byte
	Reason       string
}

// profileFields are the journal fields that hold the profile of an
// account, erased with it (Journal.EraseUser): the pseudo, the names and the
// e-mail.
var profileFields = []string{"pseudo", "first_name", "last_name", "email"}

// journalStore writes the journal.
type journalStore interface {
	InsertAction(ctx context.Context, a ActionRecord) error
	EraseUserValues(ctx context.Context, userID string) error
}

// Journal records the admin changes (admin_actions).
type Journal struct {
	store journalStore
	clock clock.Clock
}

// now is the instant of the clock the journal dates its lines by.
func (j *Journal) now() time.Time { return j.clock.Now() }

// NewJournal returns a Journal writing to store.
func NewJournal(store journalStore, clk clock.Clock) *Journal {
	return &Journal{store: store, clock: clk}
}

// Record writes e to the journal, at the instant of the clock. It is called
// by every admin change inside its transaction (db.InTx: ctx carries it), so
// that the change and its line commit or roll back together. A reason out
// of its bounds (validReason) answers 400 validation_failed on reason; any
// other bad entry is a programming error.
func (j *Journal) Record(ctx context.Context, e Entry) error {
	reason, err := validReason(e.Reason)
	if err != nil {
		return err
	}
	adminID, err := uuid.Parse(e.AdminID)
	if err != nil {
		return fmt.Errorf("journal admin id: %w", err)
	}
	targetID, err := uuid.Parse(e.TargetID)
	if err != nil {
		return fmt.Errorf("journal target id: %w", err)
	}
	ids := make([]string, len(e.ChallengeIDs))
	for i, id := range e.ChallengeIDs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("journal challenge id: %w", err)
		}
		ids[i] = parsed.String()
	}
	oldValue, err := jsonObject(e.OldValue)
	if err != nil {
		return fmt.Errorf("journal old value: %w", err)
	}
	newValue, err := jsonObject(e.NewValue)
	if err != nil {
		return fmt.Errorf("journal new value: %w", err)
	}
	return j.store.InsertAction(ctx, ActionRecord{
		AdminID:      adminID.String(),
		CreatedAt:    j.clock.Now(),
		Action:       e.Action,
		TargetType:   e.TargetType,
		TargetID:     targetID.String(),
		ChallengeIDs: ids,
		OldValue:     oldValue,
		NewValue:     newValue,
		Reason:       reason,
	})
}

// validReason returns the reason of an admin change trimmed: 3 to 500
// characters once surrounding spaces are removed, 3 of them visible
// (textx.VisibleLen), no hidden character (textx) but line breaks and
// tabs. Otherwise 400 validation_failed on the field reason.
func validReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if textx.VisibleLen(trimmed) < minReason || utf8.RuneCountInString(trimmed) > maxReason ||
		textx.HasHidden(trimmed, "\n\r\t\u2028\u2029") {
		return "", apperr.InvalidFields(nil, "reason")
	}
	return trimmed, nil
}

// jsonObject encodes v, nil for none.
func jsonObject(v map[string]any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// EraseUser erases the profile values (profileFields) of the journal lines
// that target account userID, as an account or as a participant (README
// §2.12): a user.Eraser, run in the transaction of the deletion of the
// account (ctx carries it), whoever deletes it. The action, the instant,
// the target and the reason stay.
func (j *Journal) EraseUser(ctx context.Context, userID string) error {
	return j.store.EraseUserValues(ctx, userID)
}
