package admin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// fakeJournalStore records what the journal wrote.
type fakeJournalStore struct {
	records []ActionRecord
	// erased records the accounts whose profile values were erased.
	erased []string
	err    error
}

func (f *fakeJournalStore) InsertAction(_ context.Context, a ActionRecord) error {
	f.records = append(f.records, a)
	return f.err
}

func (f *fakeJournalStore) EraseUserValues(_ context.Context, userID string) error {
	f.erased = append(f.erased, userID)
	return f.err
}

func TestValidReason(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   string
		ok     bool
	}{
		{name: "2 characters", reason: "ab"},
		{name: "3 characters", reason: "abc", want: "abc", ok: true},
		{name: "500 characters", reason: strings.Repeat("é", 500), want: strings.Repeat("é", 500), ok: true},
		{name: "501 characters", reason: strings.Repeat("é", 501)},
		{name: "empty", reason: ""},
		{name: "spaces only", reason: "     "},
		{name: "surrounding spaces do not count", reason: "  ab  "},
		{name: "trimmed", reason: "  Oubli d'une séance  ", want: "Oubli d'une séance", ok: true},
		{name: "500 characters once trimmed", reason: " " + strings.Repeat("a", 500) + " ", want: strings.Repeat("a", 500), ok: true},
		{name: "line breaks and tabs", reason: "Oubli\n\tde Bob", want: "Oubli\n\tde Bob", ok: true},
		{name: "invisible characters do not count", reason: "a\u200d\u200db"},
		{name: "hidden character", reason: "Oubli\u202e"},
		{name: "control character", reason: "Oubli\x00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validReason(tt.reason)
			if !tt.ok {
				if !errors.Is(err, apperr.InvalidFields(nil, "reason")) || fieldsOf(err) != "reason" {
					t.Fatalf("validReason(%q) = %q, %v, want validation_failed on reason", tt.reason, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("validReason(%q) = %q, %v, want %q", tt.reason, got, err, tt.want)
			}
		})
	}
}

// fieldsOf is the parameter fields of an apperr error, "" for none.
func fieldsOf(err error) string {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Params[apperr.FieldsParam]
}

func TestJournalRecord(t *testing.T) {
	store := &fakeJournalStore{}
	j := NewJournal(store, clock.NewFake(statsAt))
	err := j.Record(t.Context(), Entry{
		AdminID: annID, Action: "challenge.update", TargetType: TargetChallenge, TargetID: challengeID,
		ChallengeIDs: []string{strings.ToUpper(challengeID)},
		OldValue:     map[string]any{"weeks": 2}, NewValue: map[string]any{"weeks": 3},
		Reason: " Oubli ",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ActionRecord{{
		AdminID: annID, CreatedAt: statsAt, Action: "challenge.update", TargetType: TargetChallenge, TargetID: challengeID,
		ChallengeIDs: []string{challengeID}, OldValue: []byte(`{"weeks":2}`), NewValue: []byte(`{"weeks":3}`), Reason: "Oubli",
	}}
	if !reflect.DeepEqual(store.records, want) {
		t.Fatalf("recorded %+v, want %+v", store.records, want)
	}
}

func TestJournalRecordWithoutValues(t *testing.T) {
	store := &fakeJournalStore{}
	err := NewJournal(store, clock.NewFake(statsAt)).Record(t.Context(), Entry{
		AdminID: annID, Action: "user.update", TargetType: TargetUser, TargetID: bobID, Reason: "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := store.records[0]
	if r.OldValue != nil || r.NewValue != nil || r.ChallengeIDs == nil || len(r.ChallengeIDs) != 0 {
		t.Fatalf("recorded %+v, want no values and no challenge", r)
	}
}

func TestJournalRecordRefuses(t *testing.T) {
	tests := []struct {
		name       string
		entry      Entry
		validation bool
	}{
		{name: "reason too short", entry: Entry{Reason: "ab"}, validation: true},
		{name: "reason too long", entry: Entry{Reason: strings.Repeat("a", 501)}, validation: true},
		{name: "challenge id that is not one", entry: Entry{AdminID: annID, TargetID: challengeID, Reason: "abc", ChallengeIDs: []string{"nope"}}},
		{name: "admin id that is not one", entry: Entry{AdminID: "nope", TargetID: challengeID, Reason: "abc"}},
		{name: "target id that is not one", entry: Entry{AdminID: annID, TargetID: "nope", Reason: "abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeJournalStore{}
			err := NewJournal(store, clock.NewFake(statsAt)).Record(t.Context(), tt.entry)
			if err == nil {
				t.Fatal("recorded, want an error")
			}
			if got := errors.Is(err, apperr.New(apperr.CodeValidationFailed, nil)); got != tt.validation {
				t.Fatalf("error %v: validation_failed = %v, want %v", err, got, tt.validation)
			}
			if len(store.records) != 0 {
				t.Fatalf("wrote %+v", store.records)
			}
		})
	}
}

func TestJournalRecordStoreError(t *testing.T) {
	boom := errors.New("boom")
	err := NewJournal(&fakeJournalStore{err: boom}, clock.NewFake(statsAt)).Record(t.Context(), Entry{AdminID: annID, TargetID: challengeID, Reason: "abc"})
	if !errors.Is(err, boom) {
		t.Fatalf("error %v, want %v", err, boom)
	}
}

// TestTargetsMatchTheContract: the targets of the journal are those the
// contract knows.
func TestTargetsMatchTheContract(t *testing.T) {
	targets := []string{TargetChallenge, TargetParticipant, TargetActivity, TargetUser}
	contract := []api.AdminActionTargetType{api.AdminActionTargetTypeChallenge, api.AdminActionTargetTypeParticipant, api.AdminActionTargetTypeActivity, api.AdminActionTargetTypeUser}
	for i, target := range targets {
		if string(contract[i]) != target {
			t.Errorf("target %q, contract has %q", target, contract[i])
		}
	}
}

// TestInsertActionRefusesAChallengeIDThatIsNotOne: the array literal of the
// challenges never gets an element a caller did not mean, whoever calls.
func TestInsertActionRefusesAChallengeIDThatIsNotOne(t *testing.T) {
	for _, id := range []string{"nope", challengeID + "," + annID, "{" + challengeID + "}"} {
		t.Run(id, func(t *testing.T) {
			// No database: the id is refused before any statement.
			err := (&Repository{}).InsertAction(t.Context(), ActionRecord{ChallengeIDs: []string{id}})
			if err == nil {
				t.Fatal("inserted, want an error")
			}
		})
	}
}

// TestJournalEraseUser: the deletion of an account erases the profile
// values of its journal lines through the store, in its transaction.
func TestJournalEraseUser(t *testing.T) {
	store := &fakeJournalStore{}
	if err := NewJournal(store, clock.NewFake(statsAt)).EraseUser(t.Context(), bobID); err != nil {
		t.Fatal(err)
	}
	if len(store.erased) != 1 || store.erased[0] != bobID || len(store.records) != 0 {
		t.Fatalf("erased %v, recorded %v, want bob's values erased and nothing recorded", store.erased, store.records)
	}
}
