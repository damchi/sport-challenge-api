package challenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository is the only access to the challenge tables.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Create inserts c, its creator c.CreatedBy, the creator's participation
// (active, joined at joinedAt) and c.Categories in one transaction, and
// returns the challenge as stored.
func (r *Repository) Create(ctx context.Context, c Challenge, joinedAt time.Time) (Challenge, error) {
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		// A creator whose account is being deleted would otherwise keep an
		// active participation the deletion never saw.
		if err := lockUser(tx, c.CreatedBy); err != nil {
			return err
		}
		// RETURNING fills the columns the database sets (id, timestamps).
		if err := tx.Clauses(clause.Returning{}).Create(&c).Error; err != nil {
			return fmt.Errorf("insert challenge: %w", err)
		}
		if err := tx.Create(&creator{ChallengeID: c.ID, UserID: c.CreatedBy}).Error; err != nil {
			return fmt.Errorf("insert creator: %w", err)
		}
		p := participant{ChallengeID: c.ID, UserID: c.CreatedBy, Status: ParticipantActive, JoinedAt: joinedAt}
		if err := tx.Create(&p).Error; err != nil {
			return fmt.Errorf("insert participant: %w", err)
		}
		for i := range c.Categories {
			c.Categories[i].ChallengeID = c.ID
		}
		if err := tx.Clauses(clause.Returning{}).Create(&c.Categories).Error; err != nil {
			return fmt.Errorf("insert categories: %w", err)
		}
		return nil
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("create challenge: %w", err)
	}
	return c, nil
}

// participantCountColumn counts the participations of challenge c,
// abandoned included, but those of deleted accounts; a summary and the
// detail both read it.
const participantCountColumn = `(SELECT count(*) FROM participants p JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
        WHERE p.challenge_id = c.id)`

// listColumns are the columns of a Summary. The membership of @actor is read
// as in membershipQuery, so a summary gives the same role as the challenge
// itself.
const listColumns = `
SELECT c.id, c.title, c.visibility, c.timezone, c.reference_sport, c.starts_at, c.ends_at,
       ` + participantCountColumn + ` AS participant_count,
       me.status AS my_status,
       EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = @actor) AS creator,
       ` + invitedColumn + ` AS invited
FROM challenges c
LEFT JOIN participants me ON me.challenge_id = c.id AND me.user_id = @actor
WHERE c.deleted_at IS NULL`

// List returns at most q.Limit challenges of q, filtered in SQL: a
// deleted challenge never appears, and the public scope holds public
// challenges only. Finished challenges come most recent first, the others
// soonest first; the key (starts_at, id) makes the order total.
func (r *Repository) List(ctx context.Context, q ListQuery) ([]Summary, error) {
	var sql strings.Builder
	sql.WriteString(listColumns)
	args := map[string]any{"actor": q.ActorID, "now": q.Now, "limit": q.Limit}

	sql.WriteString(scopeFilter(q.Scope))
	switch q.Status {
	case StatusUpcoming:
		sql.WriteString(" AND c.starts_at > @now")
	case StatusOngoing:
		sql.WriteString(" AND c.starts_at <= @now AND c.ends_at > @now")
	case StatusFinished:
		sql.WriteString(" AND c.ends_at <= @now")
	}

	descending := q.Status == StatusFinished
	if q.After != nil {
		args["after_starts"], args["after_id"] = q.After.StartsAt, q.After.ID
		if descending {
			sql.WriteString(" AND (c.starts_at, c.id) < (@after_starts, @after_id)")
		} else {
			sql.WriteString(" AND (c.starts_at, c.id) > (@after_starts, @after_id)")
		}
	}
	if descending {
		sql.WriteString(" ORDER BY c.starts_at DESC, c.id DESC")
	} else {
		sql.WriteString(" ORDER BY c.starts_at, c.id")
	}
	sql.WriteString(" LIMIT @limit")

	var items []Summary
	if err := r.db.WithContext(ctx).Raw(sql.String(), args).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list challenges: %w", err)
	}
	return items, nil
}

// scopeFilter is the condition of a listing's scope on challenges c, with
// me the participation of @actor: the visibility rules of the scope.
func scopeFilter(scope string) string {
	if scope == ScopeMine {
		// The participants (user_id) index: every creator is a participant.
		return " AND me.user_id IS NOT NULL"
	}
	return " AND c.visibility = 'public'"
}

// Counts returns how many challenges of q's scope have each status at q.Now,
// with the conditions of List on the scope, whatever q.Status and q.After.
// The statuses are those of StatusAt.
func (r *Repository) Counts(ctx context.Context, q ListQuery) (Counts, error) {
	var out Counts
	err := r.db.WithContext(ctx).Raw(`SELECT
		count(*) FILTER (WHERE c.starts_at > @now) AS upcoming,
		count(*) FILTER (WHERE c.starts_at <= @now AND c.ends_at > @now) AS ongoing,
		count(*) FILTER (WHERE c.ends_at <= @now) AS finished
		FROM challenges c
		LEFT JOIN participants me ON me.challenge_id = c.id AND me.user_id = @actor
		WHERE c.deleted_at IS NULL`+scopeFilter(q.Scope),
		map[string]any{"actor": q.ActorID, "now": q.Now},
	).Scan(&out).Error
	if err != nil {
		return Counts{}, fmt.Errorf("count challenges: %w", err)
	}
	return out, nil
}

// errChallengeNotFound means no challenge, or a deleted one.
var errChallengeNotFound = errors.New("challenge not found")

// membershipQuery reads challenge @id unless deleted, with the membership of
// @actor, invited when an invitation of the challenge linked to @actor is
// valid at @now: not expired, not revoked, its author's account not deleted
// (README §2.2), and its author may still invite (CanInvite: a creator, or an
// active participant when members may invite), so the links of an excluded or
// abandoned participant, or of a deleted account, give nothing. The
// token never reaches this query: only the link, recorded when the user
// opened the invitation, counts. account_deleted tells whether @actor's
// account was deleted (anonymized). participant_count is that of a summary
// (participantCountColumn). It never takes a lock: under READ COMMITTED, a
// statement that waits for a lock keeps the snapshot taken before the wait, so
// its membership subqueries would miss a participation committed meanwhile.
// Join locks the challenge in a statement of its own, then runs this query.
const membershipQuery = `SELECT c.*,
	EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = @actor) AS creator,
	(SELECT status FROM participants WHERE challenge_id = c.id AND user_id = @actor) AS participant,
	(SELECT abandoned_at FROM participants WHERE challenge_id = c.id AND user_id = @actor) AS abandoned_at,
	` + invitedColumn + ` AS invited,
	EXISTS (SELECT 1 FROM users WHERE id = @actor AND anonymized_at IS NOT NULL) AS account_deleted,
	` + participantCountColumn + ` AS participant_count
	FROM challenges c WHERE c.id = @id AND c.deleted_at IS NULL`

// invitedColumn tells whether @actor is invited to challenge c at @now (see
// membershipQuery); List reads it too.
const invitedColumn = `EXISTS (SELECT 1 FROM invitation_recipients r JOIN invitations i ON i.id = r.invitation_id
		WHERE r.user_id = @actor AND i.challenge_id = c.id AND i.revoked_at IS NULL AND i.expires_at > @now
		AND EXISTS (SELECT 1 FROM users WHERE id = i.invited_by AND anonymized_at IS NULL)
		AND (EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = i.invited_by)
			OR (c.members_can_invite AND EXISTS (SELECT 1 FROM participants
				WHERE challenge_id = c.id AND user_id = i.invited_by AND status = 'active'))))`

// readWithMembership runs membershipQuery on gdb, the invitations judged at
// now. A missing or deleted challenge gives errChallengeNotFound.
func readWithMembership(gdb *gorm.DB, id, actorID string, now time.Time) (Challenge, Membership, error) {
	var row struct {
		Challenge
		Creator          bool
		Participant      *string
		AbandonedAt      *time.Time
		Invited          bool
		AccountDeleted   bool
		ParticipantCount int
	}
	err := gdb.Raw(membershipQuery, map[string]any{"id": id, "actor": actorID, "now": now}).Scan(&row).Error
	if err != nil {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", err)
	}
	if row.ID == "" {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", errChallengeNotFound)
	}
	m := Membership{Creator: row.Creator, Invited: row.Invited, AccountDeleted: row.AccountDeleted, AbandonedAt: row.AbandonedAt}
	if row.Participant != nil {
		m.Participant = ParticipantStatus(*row.Participant)
	}
	c := row.Challenge
	c.ParticipantCount = row.ParticipantCount
	return c, m, nil
}

// Get returns the challenge id unless deleted, without its categories, and
// the membership of actorID in it, its invitations judged at now, in one
// query: a missing, deleted or private challenge costs the same before the
// service decides what the actor may see. A missing or deleted challenge
// gives errChallengeNotFound. It reads in the transaction of ctx, if any
// (the preview of an admin change ranks what the change wrote).
func (r *Repository) Get(ctx context.Context, id, actorID string, now time.Time) (Challenge, Membership, error) {
	return readWithMembership(db.Conn(ctx, r.db), id, actorID, now)
}

// ActiveParticipations returns the challenges, not deleted and not finished
// at now, where userID is an active participant, by id. It reads in the
// transaction of ctx, if any (account deletion).
func (r *Repository) ActiveParticipations(ctx context.Context, userID string, now time.Time) ([]string, error) {
	var ids []string
	err := db.Conn(ctx, r.db).Raw(`SELECT c.id FROM participants p JOIN challenges c ON c.id = p.challenge_id
		WHERE p.user_id = ? AND p.status = 'active' AND c.deleted_at IS NULL AND c.ends_at > ?
		ORDER BY c.id`, userID, now).Scan(&ids).Error
	if err != nil {
		return nil, fmt.Errorf("list active participations: %w", err)
	}
	return ids, nil
}

// LastCreatorChallenges returns the challenges, not deleted and not finished
// at now, where userID is a creator and no other creator has an account (not
// anonymized), by id: those the deletion of userID's account would leave
// without anybody to manage them. It runs in the transaction of ctx (account
// deletion) and first locks, in one statement ordered by id, every challenge
// not finished the deletion touches: those userID creates, so that two
// creators deleting their accounts at once cannot both leave, and those where
// userID is an active participant, which the deletion then abandons. Locking
// both sets in one global order means two deletions never wait for each other
// in a cycle. The creators are read after the lock, in a new statement (see
// membershipQuery).
func (r *Repository) LastCreatorChallenges(ctx context.Context, userID string, now time.Time) ([]string, error) {
	tx := db.Conn(ctx, r.db)
	var locked []string
	err := tx.Raw(`SELECT c.id FROM challenges c
		WHERE c.deleted_at IS NULL AND c.ends_at > @now
			AND (c.id IN (SELECT challenge_id FROM challenge_creators WHERE user_id = @user)
				OR c.id IN (SELECT challenge_id FROM participants WHERE user_id = @user AND status = 'active'))
		ORDER BY c.id FOR UPDATE`, map[string]any{"user": userID, "now": now}).Scan(&locked).Error
	if err != nil {
		return nil, fmt.Errorf("lock challenges of the account: %w", err)
	}
	// Required: IN with an empty list is not valid SQL.
	if len(locked) == 0 {
		return nil, nil
	}
	var ids []string
	err = tx.Raw(`SELECT c.id FROM challenges c WHERE c.id IN @locked
		AND EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = @user)
		AND NOT EXISTS (SELECT 1 FROM challenge_creators o JOIN users u ON u.id = o.user_id
			WHERE o.challenge_id = c.id AND o.user_id <> @user AND u.anonymized_at IS NULL)
		ORDER BY c.id`, map[string]any{"locked": locked, "user": userID}).Scan(&ids).Error
	if err != nil {
		return nil, fmt.Errorf("list last creator challenges: %w", err)
	}
	return ids, nil
}

// Memberships returns the challenges, not deleted, finished ones included,
// that userID joined, whatever the status of the participation, with their
// membership and their distance factors, by start then id. It reads in the
// transaction of ctx, if any (recording an activity). A challenge without a
// cumulative distance category still appears, with no factors (outer join):
// the week lock must see every challenge joined.
func (r *Repository) Memberships(ctx context.Context, userID string) ([]UserChallenge, error) {
	var rows []struct {
		ID             string
		Title          string
		Timezone       string
		StartsAt       time.Time
		EndsAt         time.Time
		Creator        bool
		Participant    string
		AbandonedAt    *time.Time
		DistanceParams Params
	}
	err := db.Conn(ctx, r.db).Raw(`SELECT c.id, c.title, c.timezone, c.starts_at, c.ends_at, p.status AS participant, p.abandoned_at,
		EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = p.user_id) AS creator,
		COALESCE(cat.params, '{}') AS distance_params
		FROM participants p JOIN challenges c ON c.id = p.challenge_id
		LEFT JOIN categories cat ON cat.challenge_id = c.id AND cat.type = ?
		WHERE p.user_id = ? AND c.deleted_at IS NULL
		ORDER BY c.starts_at, c.id`, CategoryCumulativeDistance, userID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	out := make([]UserChallenge, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserChallenge{
			ID:         row.ID,
			Title:      row.Title,
			Timezone:   row.Timezone,
			StartsAt:   row.StartsAt,
			EndsAt:     row.EndsAt,
			Factors:    row.DistanceParams.Factors,
			Membership: Membership{Creator: row.Creator, Participant: ParticipantStatus(row.Participant), AbandonedAt: row.AbandonedAt},
		})
	}
	return out, nil
}

// Participants returns up to limit participations of challenge id, abandoned
// ones included and those of deleted accounts left out, in order of
// registration (joined_at, user_id), after the position after when set,
// each telling whether it is a creator. Pseudos are not read: they belong to
// module user.
func (r *Repository) Participants(ctx context.Context, id string, limit int, after *ParticipantPosition) ([]Participant, error) {
	q := r.db.WithContext(ctx).Table("participants p").
		Select("p.user_id, p.status, p.joined_at, "+
			"EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = p.challenge_id AND user_id = p.user_id) AS creator").
		Joins("JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL").
		Where("p.challenge_id = ?", id)
	if after != nil {
		q = q.Where("(p.joined_at, p.user_id) > (?, ?)", after.JoinedAt, after.UserID)
	}
	var rows []struct {
		UserID   string
		Status   string
		JoinedAt time.Time
		Creator  bool
	}
	if err := q.Order("p.joined_at, p.user_id").Limit(limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	items := make([]Participant, 0, len(rows))
	for _, row := range rows {
		items = append(items, Participant{UserID: row.UserID, Status: ParticipantStatus(row.Status), IsCreator: row.Creator, JoinedAt: row.JoinedAt})
	}
	return items, nil
}

// ParticipantMemberships returns the membership of each participant of
// challenge id, abandoned ones included and those of deleted accounts left
// out, by user id: the participation alone (status and abandon), what tells
// where their activities count.
func (r *Repository) ParticipantMemberships(ctx context.Context, id string) (map[string]Membership, error) {
	var rows []struct {
		UserID      string
		Status      string
		AbandonedAt *time.Time
	}
	err := r.db.WithContext(ctx).Raw(`SELECT p.user_id, p.status, p.abandoned_at FROM participants p
		JOIN users u ON u.id = p.user_id AND u.anonymized_at IS NULL
		WHERE p.challenge_id = ?`, id).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read participant memberships: %w", err)
	}
	out := make(map[string]Membership, len(rows))
	for _, row := range rows {
		out[row.UserID] = Membership{Participant: ParticipantStatus(row.Status), AbandonedAt: row.AbandonedAt}
	}
	return out, nil
}

// errAlreadyParticipant means the participation to insert already exists.
var errAlreadyParticipant = errors.New("already a participant")

// errAccountDeleted means the user's account was deleted (anonymized).
var errAccountDeleted = errors.New("account deleted")

// lockUser takes the row of user id FOR SHARE, before any lock on a
// challenge: the order of an account deletion (the user row, then each of
// its challenges), so that the two never wait for each other in a cycle.
// Every write that references a user (a foreign key locks the row too) takes
// it first. An anonymized or unknown user gives errAccountDeleted.
func lockUser(tx *gorm.DB, id string) error {
	var anonymized []bool
	if err := tx.Raw("SELECT anonymized_at IS NOT NULL FROM users WHERE id = ? FOR SHARE", id).Scan(&anonymized).Error; err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if len(anonymized) == 0 || anonymized[0] {
		return errAccountDeleted
	}
	return nil
}

// withLockedMembership runs fn in a transaction on challenge id, locked
// first (FOR UPDATE) so that writes on its participations, and its deletion,
// never interleave. The membership is read after the lock, in a new
// statement, so it sees a change committed by a transaction that held the
// lock before (see membershipQuery); its invitations are judged at now. A
// missing or deleted challenge gives errChallengeNotFound; an error of fn
// rolls everything back.
func (r *Repository) withLockedMembership(ctx context.Context, id, actorID string, now time.Time, fn func(tx *gorm.DB, c Challenge, m Membership) error) error {
	return db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		var locked []string
		if err := tx.Raw("SELECT id FROM challenges WHERE id = ? AND deleted_at IS NULL FOR UPDATE", id).Scan(&locked).Error; err != nil {
			return fmt.Errorf("lock challenge: %w", err)
		}
		if len(locked) == 0 {
			return fmt.Errorf("lock challenge: %w", errChallengeNotFound)
		}
		c, m, err := readWithMembership(tx, id, actorID, now)
		if err != nil {
			return err
		}
		return fn(tx, c, m)
	})
}

// Join adds actorID to challenge id as an active participant, under the
// lock of withLockedMembership. decide gets the challenge and the membership
// and refuses with an error, nothing being written then; otherwise it
// returns the instant of the join. An existing participation gives
// errAlreadyParticipant (the lock makes it impossible, the insert stays
// safe); an anonymized account (deleted meanwhile) gives errAccountDeleted.
func (r *Repository) Join(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error {
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		// A deletion in progress makes the join wait, then refuse; a join in
		// progress makes the deletion wait, then abandon it.
		if err := lockUser(db.Conn(ctx, r.db), actorID); err != nil {
			return err
		}
		return r.joinLocked(ctx, id, actorID, now, decide)
	})
	if err != nil {
		return fmt.Errorf("join challenge: %w", err)
	}
	return nil
}

// joinLocked inserts the participation under the lock of withLockedMembership.
func (r *Repository) joinLocked(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error {
	return r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, m Membership) error {
		joinedAt, err := decide(c, m)
		if err != nil {
			return err
		}
		p := participant{ChallengeID: id, UserID: actorID, Status: ParticipantActive, JoinedAt: joinedAt}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p)
		if res.Error != nil {
			return fmt.Errorf("insert participant: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return errAlreadyParticipant
		}
		return nil
	})
}

// Abandon marks the active participation of actorID in challenge id as
// abandoned, under the lock of withLockedMembership. decide gets the
// challenge and the membership and refuses with an error, or answers that
// nothing is to write (already abandoned), or gives the instant of the
// abandon.
func (r *Repository) Abandon(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (noop bool, at time.Time, err error)) error {
	err := r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, m Membership) error {
		noop, at, err := decide(c, m)
		if err != nil || noop {
			return err
		}
		return abandonParticipation(tx, id, actorID, at)
	})
	if err != nil {
		return fmt.Errorf("abandon challenge: %w", err)
	}
	return nil
}

// Exclude marks the active participation of targetID in challenge id as
// abandoned, on behalf of actorID, under the lock of withLockedMembership.
// decide gets the challenge, the membership of the actor and the one of the
// target (read after the lock too), and refuses with an error, or answers
// that nothing is to write, or gives the instant of the exclusion.
func (r *Repository) Exclude(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (noop bool, at time.Time, err error)) error {
	err := r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, actor Membership) error {
		_, target, err := readWithMembership(tx, id, targetID, now)
		if err != nil {
			return err
		}
		noop, at, err := decide(c, actor, target)
		if err != nil || noop {
			return err
		}
		return abandonParticipation(tx, id, targetID, at)
	})
	if err != nil {
		return fmt.Errorf("exclude participant: %w", err)
	}
	return nil
}

// AddCreator makes targetID a creator of challenge id, on behalf of actorID,
// under the lock of withLockedMembership: an exclusion of the same target
// waits for it, then sees the new creator. decide gets the challenge, the
// membership of the actor and the one of the target (read after the lock),
// and refuses with an error, or answers that nothing is to write.
func (r *Repository) AddCreator(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (noop bool, err error)) error {
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		// The target's row before the challenge (the creator row references
		// it). A target deleted meanwhile is no reason to fail here: its
		// membership, read after the lock, tells the account is deleted, and
		// the rule refuses it as for anyone not active.
		if err := lockUser(db.Conn(ctx, r.db), targetID); err != nil && !errors.Is(err, errAccountDeleted) {
			return err
		}
		return r.addCreatorLocked(ctx, id, actorID, targetID, now, decide)
	})
	if err != nil {
		return fmt.Errorf("add creator: %w", err)
	}
	return nil
}

// addCreatorLocked inserts the creator row under the lock of withLockedMembership.
func (r *Repository) addCreatorLocked(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (noop bool, err error)) error {
	return r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, actor Membership) error {
		_, target, err := readWithMembership(tx, id, targetID, now)
		if err != nil {
			return err
		}
		noop, err := decide(c, actor, target)
		if err != nil || noop {
			return err
		}
		// The lock makes a duplicate impossible; DO NOTHING keeps it harmless.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&creator{ChallengeID: id, UserID: targetID}).Error; err != nil {
			return fmt.Errorf("insert creator: %w", err)
		}
		return nil
	})
}

// LockAccount takes the row of user id FOR UPDATE, in the transaction of
// ctx (an admin change), until it ends; outside of one it is refused. An
// admin change of the members of a challenge takes it right after the week
// lock, before any challenge: in the order of an account deletion (the user
// row, then its challenges and participations), so that the change and the
// deletion of the same account never wait for each other in a cycle, and
// the recompute that follows (FOR SHARE on the users of the participants)
// finds it held already. An anonymized or unknown user gives
// errAccountDeleted.
func (r *Repository) LockAccount(ctx context.Context, id string) error {
	tx, ok := db.Tx(ctx)
	if !ok {
		return errors.New("lock account: no transaction")
	}
	var anonymized []bool
	if err := tx.Raw("SELECT anonymized_at IS NOT NULL FROM users WHERE id = ? FOR UPDATE", id).Scan(&anonymized).Error; err != nil {
		return fmt.Errorf("lock account: %w", err)
	}
	if len(anonymized) == 0 || anonymized[0] {
		return errAccountDeleted
	}
	return nil
}

// Reinstate makes the participation of userID in challenge id active again
// (an admin change), under the lock of withLockedMembership, in the
// transaction of ctx. decide gets the challenge and the membership of
// userID, read after the lock, and refuses with an error, or answers that
// nothing is to write.
func (r *Repository) Reinstate(ctx context.Context, id, userID string, now time.Time, decide func(Challenge, Membership) (noop bool, err error)) error {
	err := r.withLockedMembership(ctx, id, userID, now, func(tx *gorm.DB, c Challenge, m Membership) error {
		noop, err := decide(c, m)
		if err != nil || noop {
			return err
		}
		res := tx.Model(&participant{}).
			Where("challenge_id = ? AND user_id = ? AND status = ?", id, userID, ParticipantAbandoned).
			Updates(map[string]any{"status": ParticipantActive, "abandoned_at": nil})
		if res.Error != nil {
			return fmt.Errorf("update participant: %w", res.Error)
		}
		// The lock makes it impossible: a silent success would hide a bug.
		if res.RowsAffected == 0 {
			return errors.New("update participant: no abandoned participation")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reinstate participant: %w", err)
	}
	return nil
}

// RemoveCreator removes the creator role of userID in challenge id (an
// admin change), under the lock of withLockedMembership, in the transaction
// of ctx: an addition or a removal of another creator waits for it, then
// counts again. decide gets the challenge, the membership of userID and
// the number of the other creators with an account, read after the lock,
// and refuses with an error, or answers that nothing is to write.
func (r *Repository) RemoveCreator(ctx context.Context, id, userID string, now time.Time, decide func(c Challenge, m Membership, otherCreators int) (noop bool, err error)) error {
	err := r.withLockedMembership(ctx, id, userID, now, func(tx *gorm.DB, c Challenge, m Membership) error {
		var others int
		if err := tx.Raw(`SELECT count(*) FROM challenge_creators o JOIN users u ON u.id = o.user_id
			WHERE o.challenge_id = ? AND o.user_id <> ? AND u.anonymized_at IS NULL`, id, userID).Scan(&others).Error; err != nil {
			return fmt.Errorf("count creators: %w", err)
		}
		noop, err := decide(c, m, others)
		if err != nil || noop {
			return err
		}
		res := tx.Exec("DELETE FROM challenge_creators WHERE challenge_id = ? AND user_id = ?", id, userID)
		if res.Error != nil {
			return fmt.Errorf("delete creator: %w", res.Error)
		}
		// The lock makes it impossible: a silent success would hide a bug.
		if res.RowsAffected == 0 {
			return errors.New("delete creator: not a creator under the lock")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove creator: %w", err)
	}
	return nil
}

// Delete soft-deletes challenge id on behalf of actorID: deleted_at is set on
// the challenge row itself, under the lock of withLockedMembership, so a
// join, an abandon or any other write on the challenge either happens before
// (and stays) or waits and then finds the challenge gone. decide gets the
// challenge and the actor's membership and refuses with an error, or gives
// the instant of the deletion. Nothing else is removed: participations,
// categories and, later, activities stay for the final purge.
func (r *Repository) Delete(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error {
	err := r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, m Membership) error {
		at, err := decide(c, m)
		if err != nil {
			return err
		}
		res := tx.Model(&Challenge{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", at)
		if res.Error != nil {
			return fmt.Errorf("update challenge: %w", res.Error)
		}
		// The lock makes it impossible: a silent success would hide a bug.
		if res.RowsAffected == 0 {
			return errors.New("update challenge: not found under the lock")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete challenge: %w", err)
	}
	return nil
}

// Restore clears the deletion of challenge id (an admin change), under the
// lock of its row, in the transaction of ctx, and returns the instant it
// was deleted at, in UTC; a missing challenge, or one that is not deleted, gives
// errChallengeNotFound.
func (r *Repository) Restore(ctx context.Context, id string) (time.Time, error) {
	var deletedAt []time.Time
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT deleted_at FROM challenges WHERE id = ? AND deleted_at IS NOT NULL FOR UPDATE", id).Scan(&deletedAt).Error; err != nil {
			return fmt.Errorf("lock challenge: %w", err)
		}
		if len(deletedAt) == 0 {
			return errChallengeNotFound
		}
		if err := tx.Exec("UPDATE challenges SET deleted_at = NULL WHERE id = ?", id).Error; err != nil {
			return fmt.Errorf("update challenge: %w", err)
		}
		return nil
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("restore challenge: %w", err)
	}
	return deletedAt[0].UTC(), nil
}

// abandonParticipation turns the active participation of userID in
// challenge id into an abandon at the given instant: by the user's choice,
// or by an exclusion, both shown alike.
func abandonParticipation(tx *gorm.DB, id, userID string, at time.Time) error {
	res := tx.Model(&participant{}).
		Where("challenge_id = ? AND user_id = ? AND status = ?", id, userID, ParticipantActive).
		Updates(map[string]any{"status": ParticipantAbandoned, "abandoned_at": at})
	if res.Error != nil {
		return fmt.Errorf("update participant: %w", res.Error)
	}
	// The lock makes it impossible: a silent success would hide a bug.
	if res.RowsAffected == 0 {
		return errors.New("update participant: no active participation")
	}
	return nil
}

// CategoriesOf returns the categories of each of ids, in position order, in
// one query.
func (r *Repository) CategoriesOf(ctx context.Context, ids []string) (map[string][]Category, error) {
	out := make(map[string][]Category, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var cats []Category
	if err := r.db.WithContext(ctx).Where("challenge_id IN ?", ids).Order("challenge_id, position").Find(&cats).Error; err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	for _, c := range cats {
		out[c.ChallengeID] = append(out[c.ChallengeID], c)
	}
	return out, nil
}

// Categories returns the categories of challenge id, in position order, in
// the transaction of ctx, if any (as Get).
func (r *Repository) Categories(ctx context.Context, id string) ([]Category, error) {
	var cats []Category
	if err := db.Conn(ctx, r.db).Where("challenge_id = ?", id).Order("position").Find(&cats).Error; err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	return cats, nil
}

// LastAdminCorrection returns the instant of the last admin change that
// touches challenge id (admin_actions.challenge_ids, the journal of the admin
// module), nil when none does, in the transaction of ctx, if any (as Get).
// Only the instant is read: never the reason nor the admin.
func (r *Repository) LastAdminCorrection(ctx context.Context, id string) (*time.Time, error) {
	var row struct{ At *time.Time }
	err := db.Conn(ctx, r.db).Raw(`SELECT max(created_at) AS at FROM admin_actions
		WHERE challenge_ids @> ARRAY[?]::uuid[]`, id).Scan(&row).Error
	if err != nil {
		return nil, fmt.Errorf("read last admin correction: %w", err)
	}
	return row.At, nil
}

// UpdateText writes the changed title and description of challenge id
// (an empty description is stored as none). A deleted challenge is never
// written and gives errChallengeNotFound.
func (r *Repository) UpdateText(ctx context.Context, id string, changes TextChanges) error {
	columns := map[string]any{}
	if changes.Title != nil {
		columns["title"] = *changes.Title
	}
	if d := changes.Description; d != nil {
		if *d == "" {
			columns["description"] = nil
		} else {
			columns["description"] = *d
		}
	}
	if len(columns) == 0 {
		return nil
	}
	res := r.db.WithContext(ctx).Model(&Challenge{}).Where("id = ? AND deleted_at IS NULL", id).Updates(columns)
	if res.Error != nil {
		return fmt.Errorf("update challenge: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update challenge: %w", errChallengeNotFound)
	}
	return nil
}

// UpdateSettings writes the settings of challenge id that decide returns,
// under the lock of withLockedMembership, in the transaction of ctx (an
// admin change): decide gets the challenge, its membership left aside, and
// its categories, read after the lock, and refuses with an error, nothing
// being written then. The columns are written, then the categories: those
// kept get their new settings, those removed are deleted with their
// eliminations and frozen results (whose keys reference them; the recompute
// that follows the change erases and records them again), the new ones are
// inserted. A deleted challenge gives errChallengeNotFound.
func (r *Repository) UpdateSettings(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, []Category) (Challenge, error)) error {
	// withLockedMembership for its lock on the challenge row, as every
	// write on a challenge; the membership of the admin is of no use here.
	err := r.withLockedMembership(ctx, id, actorID, now, func(tx *gorm.DB, c Challenge, _ Membership) error {
		var cats []Category
		if err := tx.Where("challenge_id = ?", id).Order("position").Find(&cats).Error; err != nil {
			return fmt.Errorf("read categories: %w", err)
		}
		next, err := decide(c, cats)
		if err != nil {
			return err
		}
		res := tx.Model(&Challenge{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]any{
			"title":              next.Title,
			"description":        next.Description,
			"visibility":         next.Visibility,
			"members_can_invite": next.MembersCanInvite,
			"timezone":           next.Timezone,
			"reference_sport":    next.ReferenceSport,
			"starts_at":          next.StartsAt,
			"ends_at":            next.EndsAt,
		})
		if res.Error != nil {
			return fmt.Errorf("update challenge: %w", res.Error)
		}
		// The lock makes it impossible: a silent success would hide a bug.
		if res.RowsAffected == 0 {
			return errors.New("update challenge: not found under the lock")
		}
		return replaceCategories(tx, id, cats, next.Categories)
	})
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	return nil
}

// replaceCategories turns the categories old of challenge id into next, by
// type (one category of each type at most).
func replaceCategories(tx *gorm.DB, id string, old, next []Category) error {
	kept := make(map[string]string, len(old))
	for _, cat := range old {
		kept[cat.Type] = cat.ID
	}
	for _, cat := range next {
		catID, ok := kept[cat.Type]
		delete(kept, cat.Type)
		if ok {
			if err := tx.Model(&Category{}).Where("id = ?", catID).Update("params", cat.Params).Error; err != nil {
				return fmt.Errorf("update category %s: %w", cat.Type, err)
			}
			continue
		}
		cat.ChallengeID = id
		if err := tx.Create(&cat).Error; err != nil {
			return fmt.Errorf("insert category %s: %w", cat.Type, err)
		}
	}
	// What remains is removed, after what references it.
	for _, catID := range kept {
		for _, query := range []string{
			"DELETE FROM elimination WHERE category_id = ?",
			"DELETE FROM result_snapshot WHERE category_id = ?",
			"DELETE FROM categories WHERE id = ?",
		} {
			if err := tx.Exec(query, catID).Error; err != nil {
				return fmt.Errorf("remove category: %w", err)
			}
		}
	}
	return nil
}

// eraseSnapshotEntriesQuery removes the entries of @user from the frozen
// results of the challenges they joined, keeping the order of the others.
// The ranks are not rewritten: they are computed again when served.
const eraseSnapshotEntriesQuery = `UPDATE result_snapshot rs
SET payload = jsonb_set(rs.payload, '{entries}', COALESCE(
	(SELECT jsonb_agg(e ORDER BY n) FROM jsonb_array_elements(rs.payload->'entries') WITH ORDINALITY AS t(e, n)
		WHERE e->>'user_id' <> CAST(@user AS text)),
	'[]'::jsonb))
WHERE rs.challenge_id IN (SELECT challenge_id FROM participants WHERE user_id = CAST(@user AS uuid))
	AND rs.payload->'entries' @> jsonb_build_array(jsonb_build_object('user_id', CAST(@user AS text)))`

// EraseUser deletes every row of userID in the challenge tables: their
// entries in the frozen results, eliminations, creator roles and
// participations, in that order (an elimination references its
// participation). The challenges userID created stay. It runs in the
// transaction of ctx (account deletion).
func (r *Repository) EraseUser(ctx context.Context, userID string) error {
	tx := db.Conn(ctx, r.db)
	steps := []struct{ name, query string }{
		{"erase result snapshot entries", eraseSnapshotEntriesQuery},
		{"delete eliminations", "DELETE FROM elimination WHERE user_id = CAST(@user AS uuid)"},
		{"delete creator roles", "DELETE FROM challenge_creators WHERE user_id = CAST(@user AS uuid)"},
		{"delete participations", "DELETE FROM participants WHERE user_id = CAST(@user AS uuid)"},
	}
	for _, s := range steps {
		if err := tx.Exec(s.query, map[string]any{"user": userID}).Error; err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}
