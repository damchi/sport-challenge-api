package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// fakeAccounts is the module user, logging its changes in log.
type fakeAccounts struct {
	log *correctionLog
	// change is what a change of the profile or of the block gives.
	change user.AccountChange
	at     time.Time
	err    error
	// completed records the deletions completed at Auth0.
	completed []user.Deletion
}

func (f *fakeAccounts) answer(what string, actor auth.Actor, id string) (user.AccountChange, error) {
	if !actor.Admin {
		return user.AccountChange{}, apperr.New(apperr.CodeNotFound, nil)
	}
	f.log.add(what + " " + id)
	return f.change, f.err
}

func (f *fakeAccounts) AdminUpdateProfile(_ context.Context, actor auth.Actor, id string, changes user.ProfileChanges) (user.AccountChange, error) {
	return f.answer("update "+*changes.FirstName, actor, id)
}

func (f *fakeAccounts) AdminBlock(_ context.Context, actor auth.Actor, id string) (user.AccountChange, error) {
	return f.answer("block", actor, id)
}

func (f *fakeAccounts) AdminUnblock(_ context.Context, actor auth.Actor, id string) (user.AccountChange, error) {
	return f.answer("unblock", actor, id)
}

func (f *fakeAccounts) AdminDelete(_ context.Context, actor auth.Actor, id string) (user.Deletion, error) {
	if _, err := f.answer("delete", actor, id); err != nil {
		return user.Deletion{}, err
	}
	return user.Deletion{At: f.at}, nil
}

func (f *fakeAccounts) CompleteDeletion(_ context.Context, d user.Deletion) {
	f.log.add("auth0")
	f.completed = append(f.completed, d)
}

// accountRun is one of the changes of AccountCorrections, on bob.
type accountRun func(s *AccountCorrections, ctx context.Context, actor auth.Actor, reason string) error

var (
	updateBob accountRun = func(s *AccountCorrections, ctx context.Context, actor auth.Actor, reason string) error {
		return s.UpdateProfile(ctx, actor, bobID, user.ProfileChanges{FirstName: ptr("Robert")}, reason)
	}
	blockBob accountRun = func(s *AccountCorrections, ctx context.Context, actor auth.Actor, reason string) error {
		return s.Block(ctx, actor, bobID, reason)
	}
	unblockBob accountRun = func(s *AccountCorrections, ctx context.Context, actor auth.Actor, reason string) error {
		return s.Unblock(ctx, actor, bobID, reason)
	}
	deleteBob accountRun = func(s *AccountCorrections, ctx context.Context, actor auth.Actor, reason string) error {
		return s.Delete(ctx, actor, bobID, reason)
	}
)

// TestAccountCorrections: each change of an account runs under the week
// lock with its journal line (the account as target, no challenge), and
// ranks, recomputes and previews nothing; the identity of a deleted account
// goes at Auth0 after the commit only.
func TestAccountCorrections(t *testing.T) {
	at := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	change := user.AccountChange{Old: map[string]any{"first_name": "Bob"}, New: map[string]any{"first_name": "Robert"}}
	tests := []struct {
		name       string
		run        accountRun
		wantSteps  []string
		wantAction string
		wantOld    map[string]any
		wantNew    map[string]any
	}{
		{name: "update", run: updateBob, wantSteps: []string{"tx", "update Robert " + bobID, "journal"}, wantAction: ActionUserUpdate, wantOld: change.Old, wantNew: change.New},
		{name: "block", run: blockBob, wantSteps: []string{"tx", "block " + bobID, "journal"}, wantAction: ActionUserBlock, wantOld: change.Old, wantNew: change.New},
		{name: "unblock", run: unblockBob, wantSteps: []string{"tx", "unblock " + bobID, "journal"}, wantAction: ActionUserUnblock, wantOld: change.Old, wantNew: change.New},
		{
			name: "delete", run: deleteBob, wantSteps: []string{"tx", "delete " + bobID, "journal", "auth0"}, wantAction: ActionUserDelete,
			wantOld: map[string]any{"deleted_at": nil}, wantNew: map[string]any{"deleted_at": at},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			accounts := &fakeAccounts{log: log, change: change, at: at}
			if err := tt.run(NewAccountCorrections(corrections, accounts), t.Context(), admin, "Demande du titulaire"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(log.steps, tt.wantSteps) || log.rolledBack {
				t.Fatalf("steps %v (rolled back %v), want %v", log.steps, log.rolledBack, tt.wantSteps)
			}
			a := journal.records[0]
			oldValue, _ := json.Marshal(tt.wantOld)
			newValue, _ := json.Marshal(tt.wantNew)
			if a.Action != tt.wantAction || a.TargetType != TargetUser || a.TargetID != bobID || len(a.ChallengeIDs) != 0 ||
				string(a.OldValue) != string(oldValue) || string(a.NewValue) != string(newValue) ||
				a.Reason != "Demande du titulaire" || a.AdminID != annID {
				t.Fatalf("journal line %+v", a)
			}
		})
	}
}

// TestAccountCorrectionsRefusals: a non-admin reaches nothing, a missing
// reason is refused before anything is locked, and a refused change rolls
// everything back: no journal line, nothing deleted at Auth0.
func TestAccountCorrectionsRefusals(t *testing.T) {
	lastCreator := apperr.New(apperr.CodeLastCreator, nil)
	tests := []struct {
		name      string
		run       accountRun
		actor     auth.Actor
		reason    string
		err       error
		wantCode  apperr.Code
		wantSteps []string
	}{
		{name: "update, not an admin", run: updateBob, actor: auth.Actor{ID: annID}, reason: "Demande", wantCode: apperr.CodeNotFound},
		{name: "block, not an admin", run: blockBob, actor: auth.Actor{ID: annID}, reason: "Demande", wantCode: apperr.CodeNotFound},
		{name: "delete, not an admin", run: deleteBob, actor: auth.Actor{ID: annID}, reason: "Demande", wantCode: apperr.CodeNotFound},
		{name: "unblock, no reason", run: unblockBob, actor: admin, reason: " ", wantCode: apperr.CodeValidationFailed},
		{
			name: "block, refused", run: blockBob, actor: admin, reason: "Demande", err: apperr.New(apperr.CodeForbidden, nil),
			wantCode: apperr.CodeForbidden, wantSteps: []string{"tx", "block " + bobID},
		},
		{
			name: "delete, last creator", run: deleteBob, actor: admin, reason: "Demande", err: lastCreator,
			wantCode: apperr.CodeLastCreator, wantSteps: []string{"tx", "delete " + bobID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			accounts := &fakeAccounts{log: log, err: tt.err}
			err := tt.run(NewAccountCorrections(corrections, accounts), t.Context(), tt.actor, tt.reason)
			if !errors.Is(err, apperr.New(tt.wantCode, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.wantCode)
			}
			if !reflect.DeepEqual(log.steps, tt.wantSteps) || len(journal.records) != 0 || len(accounts.completed) != 0 {
				t.Fatalf("steps %v, journal %v, completed %v: want %v, nothing journaled nor deleted at Auth0", log.steps, journal.records, accounts.completed, tt.wantSteps)
			}
		})
	}
}

// fakeAccountUpdater records what the handler asked.
type fakeAccountUpdater struct {
	asked string
}

func (f *fakeAccountUpdater) ask(what string, actor auth.Actor, id, reason string) error {
	if !actor.Admin {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	f.asked = what + " " + id + " " + reason
	return nil
}

func (f *fakeAccountUpdater) UpdateProfile(_ context.Context, actor auth.Actor, id string, c user.ProfileChanges, reason string) error {
	var fields []string
	if c.RemovePseudo {
		fields = append(fields, "pseudo=null")
	}
	if c.Pseudo != nil {
		fields = append(fields, "pseudo="+*c.Pseudo)
	}
	if c.FirstName != nil {
		fields = append(fields, "first_name="+*c.FirstName)
	}
	if c.LastName != nil {
		fields = append(fields, "last_name="+*c.LastName)
	}
	return f.ask("update "+strings.Join(fields, ","), actor, id, reason)
}

func (f *fakeAccountUpdater) Block(_ context.Context, actor auth.Actor, id, reason string) error {
	return f.ask("block", actor, id, reason)
}

func (f *fakeAccountUpdater) Unblock(_ context.Context, actor auth.Actor, id, reason string) error {
	return f.ask("unblock", actor, id, reason)
}

func (f *fakeAccountUpdater) Delete(_ context.Context, actor auth.Actor, id, reason string) error {
	return f.ask("delete", actor, id, reason)
}

func TestAdminUserChanges(t *testing.T) {
	path := "/api/v1/admin/users/" + bobID
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantAsked  string
	}{
		{
			name: "update", method: http.MethodPatch, path: path, body: `{"reason": "Faute de frappe", "first_name": "Robert", "pseudo": null}`,
			wantStatus: http.StatusOK, wantAsked: "update pseudo=null,first_name=Robert " + bobID + " Faute de frappe",
		},
		{
			name: "update, the language is the user's", method: http.MethodPatch, path: path, body: `{"reason": "Faute de frappe", "language": "en"}`,
			wantStatus: http.StatusBadRequest,
		},
		{name: "block", method: http.MethodPost, path: path + "/block", body: `{"reason": "Spam"}`, wantStatus: http.StatusOK, wantAsked: "block " + bobID + " Spam"},
		{name: "unblock", method: http.MethodPost, path: path + "/unblock", body: `{"reason": "Erreur"}`, wantStatus: http.StatusOK, wantAsked: "unblock " + bobID + " Erreur"},
		// No preview of an account change: nothing is judged again.
		{name: "block, no dry run", method: http.MethodPost, path: path + "/block", body: `{"reason": "Spam", "dry_run": true}`, wantStatus: http.StatusBadRequest},
		{name: "delete", method: http.MethodDelete, path: path, body: `{"reason": "Demande du titulaire"}`, wantStatus: http.StatusNoContent, wantAsked: "delete " + bobID + " Demande du titulaire"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := &fakeAccountUpdater{}
			repo := &fakeRepo{user: UserRow{ID: bobID, State: UserBlocked, CreatedAt: statsAt}}
			h := NewHandler(NewService(repo, nil, clock.NewFake(statsAt), buildinfo.Info{}), nil, nil, nil, accounts)
			rec, got := serveWith(t, h, adminToken, tt.method, tt.path, tt.body, io.Discard)
			if rec.Code != tt.wantStatus || accounts.asked != tt.wantAsked {
				t.Fatalf("got %d %v, asked %q, want %d and %q", rec.Code, got, accounts.asked, tt.wantStatus, tt.wantAsked)
			}
			// The account is answered as read after the change.
			if tt.wantStatus == http.StatusOK && (got["id"] != bobID || got["state"] != "blocked" || repo.id != bobID) {
				t.Fatalf("body %v, read %q, want the account read after the change", got, repo.id)
			}
		})
		t.Run(tt.name+", without the permission", func(t *testing.T) {
			accounts := &fakeAccountUpdater{}
			h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), nil, nil, nil, accounts)
			rec, got := serveWith(t, h, "some.access.token", tt.method, tt.path, tt.body, io.Discard)
			if e, _ := got["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" || accounts.asked != "" {
				t.Fatalf("got %d %v, asked %q, want 404 not_found and nothing asked", rec.Code, got, accounts.asked)
			}
		})
	}
}
