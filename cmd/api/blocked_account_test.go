package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// errNotReached fails the storage calls no request of a blocked account
// must make.
var errNotReached = errors.New("storage reached by a blocked account")

// blockedAccounts is the storage of the users in the tests of a blocked
// account: every token is that of blockedSubject, blocked by an admin.
// Delete runs the erasure of the service and records the account deleted.
type blockedAccounts struct {
	deleted *string
}

const (
	blockedID      = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"
	blockedSubject = "email|123" // the subject of acceptingVerifier
)

func (blockedAccounts) GetOrCreateBySubject(context.Context, string) (user.User, error) {
	at := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	return user.User{ID: blockedID, AuthSubject: blockedSubject, BlockedAt: &at}, nil
}

func (b blockedAccounts) Delete(ctx context.Context, id string, _ time.Time, erase func(context.Context) error) (string, error) {
	if err := erase(ctx); err != nil {
		return "", err
	}
	*b.deleted = id
	return blockedSubject, nil
}

func (blockedAccounts) RemovePendingAuth0Deletion(context.Context, string) error { return nil }

func (blockedAccounts) GetByID(context.Context, string) (user.User, error) {
	return user.User{}, errNotReached
}

func (blockedAccounts) UpdateProfile(context.Context, string, user.ProfileChanges) (user.User, error) {
	return user.User{}, errNotReached
}

func (blockedAccounts) UpdateEmail(context.Context, string, string) error { return errNotReached }

func (blockedAccounts) PublicNames(context.Context, []string) (map[string]user.PublicName, error) {
	return nil, errNotReached
}

func (blockedAccounts) Lock(context.Context, string) (user.User, error) {
	return user.User{}, errNotReached
}

func (blockedAccounts) SetBlockedAt(context.Context, string, *time.Time) error { return errNotReached }

func (blockedAccounts) PendingAuth0Deletions(context.Context) ([]user.PendingDeletion, error) {
	return nil, errNotReached
}

func (blockedAccounts) RecordAuth0DeletionFailure(context.Context, string, string, bool) error {
	return errNotReached
}

type deletionLockFunc func(ctx context.Context, userID string) error

func (f deletionLockFunc) LockForDeletion(ctx context.Context, userID string) error {
	return f(ctx, userID)
}

// auth0Deletions records the subjects deleted at Auth0.
type auth0Deletions struct{ subjects []string }

func (a *auth0Deletions) DeleteUser(_ context.Context, subject string) error {
	a.subjects = append(a.subjects, subject)
	return nil
}

// blockedAccountTest is the router of the API, with the resolver of run and
// the use cases of the users on blockedAccounts; lock is the deletion lock
// of module challenge.
type blockedAccountTest struct {
	deleted    string
	identities auth0Deletions
}

func (b *blockedAccountTest) router(t *testing.T, lock deletionLockFunc) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	users := user.NewService(blockedAccounts{deleted: &b.deleted}, lock, nil, &b.identities, clock.Real{}, logger)
	handlers := testHandlers()
	handlers.userHandler = user.NewHandler(users)
	opts := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(1000), userLimiter: testLimiter(1000)}
	return newTestRouterWith(t, opts, handlers, acceptingVerifier{}, resolveUser(users))
}

// TestOnlyTheDeletionOfItsAccountIsOpenToABlockedAccount walks the
// registered routes with the token of a blocked account (README §2.12):
// DELETE /me deletes it, at Auth0 too; every other protected route, GET and
// PATCH /me and those of every other module and of /admin included, and a
// path matching no route, answer 403 account_blocked before reaching any
// storage.
func TestOnlyTheDeletionOfItsAccountIsOpenToABlockedAccount(t *testing.T) {
	public := map[string]bool{"GET /health": true, "GET /ready": true}
	var b blockedAccountTest
	r := b.router(t, func(context.Context, string) error { return nil })

	var open []string
	for _, route := range r.Routes() {
		name := route.Method + " " + route.Path
		if public[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(route.Method, pathParam.ReplaceAllString(route.Path, "${1}"+blockedID), nil)
			req.Header.Set("Authorization", "Bearer some.access.token")
			status, code := serve(t, r, req)
			if status == http.StatusForbidden && code == "account_blocked" {
				return
			}
			open = append(open, name)
			if name != "DELETE /api/v1/me" {
				t.Fatalf("got %d %q for a blocked account, want 403 account_blocked", status, code)
			}
			if status != http.StatusNoContent || b.deleted != blockedID || len(b.identities.subjects) != 1 || b.identities.subjects[0] != blockedSubject {
				t.Fatalf("got %d %q, deleted %q, at Auth0 %v, want 204 and the account deleted everywhere", status, code, b.deleted, b.identities.subjects)
			}
		})
	}
	if len(open) != 1 || open[0] != "DELETE /api/v1/me" {
		t.Fatalf("routes open to a blocked account: %v, want DELETE /api/v1/me alone", open)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/me/x", nil)
	req.Header.Set("Authorization", "Bearer some.access.token")
	if status, code := serve(t, r, req); status != http.StatusForbidden || code != "account_blocked" {
		t.Fatalf("path matching no route got %d %q, want 403 account_blocked", status, code)
	}
}

// TestABlockedLastCreatorIsRefusedItsDeletion: DELETE /me takes the same
// path for a blocked account, last_creator included, nothing deleted.
func TestABlockedLastCreatorIsRefusedItsDeletion(t *testing.T) {
	var b blockedAccountTest
	r := b.router(t, func(context.Context, string) error {
		return apperr.New(apperr.CodeLastCreator, nil)
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer some.access.token")
	if status, code := serve(t, r, req); status != http.StatusUnprocessableEntity || code != "last_creator" || b.deleted != "" || len(b.identities.subjects) != 0 {
		t.Fatalf("got %d %q, deleted %q, at Auth0 %v, want 422 last_creator and nothing deleted", status, code, b.deleted, b.identities.subjects)
	}
}
