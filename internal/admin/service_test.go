package admin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

var statsAt = time.Date(2026, 11, 23, 15, 30, 0, 0, time.UTC)

// fakeRepo answers figures, runs and challenges, and records what it was
// asked.
type fakeRepo struct {
	figures    Figures
	runs       []TaskRun
	figuresErr error
	runsErr    error

	rows         []ChallengeRow
	creators     map[string][]UserRef
	data         ChallengeData
	listErr      error
	creatorsErr  error
	challengeErr error

	users             []UserRow
	user              UserRow
	userChallenges    []UserChallengeRow
	activities        []ActivityRow
	usersErr          error
	userErr           error
	userChallengesErr error
	activitiesErr     error

	calls         int
	now, since    time.Time
	query         ChallengeQuery
	creatorIDs    []string
	id            string
	userQuery     UserQuery
	pageQuery     PageQuery
	activityQuery ActivityQuery
}

func (f *fakeRepo) ListUsers(_ context.Context, q UserQuery) ([]UserRow, error) {
	f.calls++
	f.userQuery = q
	return f.users, f.usersErr
}

func (f *fakeRepo) User(_ context.Context, id string) (UserRow, error) {
	f.calls++
	f.id = id
	return f.user, f.userErr
}

func (f *fakeRepo) ListUserChallenges(_ context.Context, id string, q PageQuery) ([]UserChallengeRow, error) {
	f.calls++
	f.id, f.pageQuery = id, q
	return f.userChallenges, f.userChallengesErr
}

func (f *fakeRepo) ListUserActivities(_ context.Context, id string, q ActivityQuery) ([]ActivityRow, error) {
	f.calls++
	f.id, f.activityQuery = id, q
	return f.activities, f.activitiesErr
}

func (f *fakeRepo) ListChallenges(_ context.Context, q ChallengeQuery) ([]ChallengeRow, error) {
	f.calls++
	f.query = q
	return f.rows, f.listErr
}

func (f *fakeRepo) CreatorsOf(_ context.Context, ids []string) (map[string][]UserRef, error) {
	f.calls++
	f.creatorIDs = ids
	return f.creators, f.creatorsErr
}

func (f *fakeRepo) Challenge(_ context.Context, id string) (ChallengeData, error) {
	f.calls++
	f.id = id
	return f.data, f.challengeErr
}

func (f *fakeRepo) Figures(_ context.Context, now, since time.Time) (Figures, error) {
	f.calls++
	f.now, f.since = now, since
	return f.figures, f.figuresErr
}

func (f *fakeRepo) LastWorkerRuns(context.Context) ([]TaskRun, error) {
	f.calls++
	return f.runs, f.runsErr
}

func ptr[T any](v T) *T { return &v }

func TestStatsIsHiddenFromANonAdmin(t *testing.T) {
	repo := &fakeRepo{}
	_, err := NewService(repo, clock.NewFake(statsAt), buildinfo.Info{}).Stats(t.Context(), auth.Actor{ID: "u1"})
	if !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
		t.Fatalf("Stats() = %v, want not_found", err)
	}
	if repo.calls != 0 {
		t.Fatalf("repository called %d times before the policy refused", repo.calls)
	}
}

func TestStats(t *testing.T) {
	ended := statsAt.Add(-time.Hour)
	succeeded, failed := "succeeded", "failed"
	run := WorkerRun{StartedAt: statsAt.Add(-2 * time.Hour), EndedAt: &ended, Outcome: &succeeded}
	failure := WorkerRun{StartedAt: statsAt.Add(-48 * time.Hour), EndedAt: &ended, Outcome: &failed, ErrorCode: ptr("internal_error")}
	going := WorkerRun{StartedAt: statsAt.Add(-time.Minute)}
	repo := &fakeRepo{
		figures: Figures{AccountsTotal: 9, AccountsNew: 2, AccountsActive: 4, Upcoming: 1, Ongoing: 2, Finished: 3, Deleted: 1, Activities: 12, PendingAuth0Deletions: 1},
		runs: []TaskRun{
			{Task: "retry-auth0-deletions", Run: going},
			{Task: "close-weeks", Run: run},
			{Task: "close-weeks", Failure: true, Run: failure},
			// A task the dashboard does not know is left out.
			{Task: "unknown-task", Run: run},
		},
	}
	build := buildinfo.Info{Version: "v1.2.3", Commit: "abc"}

	got, err := NewService(repo, clock.NewFake(statsAt), build).Stats(t.Context(), auth.Actor{ID: "u1", Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if !repo.now.Equal(statsAt) || !repo.since.Equal(statsAt.Add(-7*24*time.Hour)) {
		t.Errorf("figures read at %v since %v, want %v since 7 days before", repo.now, repo.since, statsAt)
	}
	want := Stats{
		ComputedAt: statsAt,
		Figures:    repo.figures,
		WorkerTasks: []WorkerTask{
			{Task: "close-weeks", LastRun: &run, LastFailure: &failure},
			{Task: "finalize-challenges"},
			{Task: "retry-auth0-deletions", LastRun: &going},
		},
		Build: build,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestStatsStorageFailure(t *testing.T) {
	boom := errors.New("boom")
	for name, repo := range map[string]*fakeRepo{
		"figures": {figuresErr: boom},
		"runs":    {runsErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewService(repo, clock.NewFake(statsAt), buildinfo.Info{}).Stats(t.Context(), auth.Actor{ID: "u1", Admin: true})
			if !errors.Is(err, boom) {
				t.Fatalf("Stats() = %v, want it to wrap %v", err, boom)
			}
		})
	}
}
