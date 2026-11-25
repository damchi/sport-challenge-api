// Package user manages the local users: one per identity at the auth
// provider, created on first login.
package user

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// User is a row of the users table. id, created_at and updated_at are
// read-only for GORM: the database fills them (default values and trigger),
// so the application never writes the time itself.
type User struct {
	ID          string `gorm:"->"`
	AuthSubject string
	Pseudo      *string
	FirstName   *string
	LastName    *string
	// Email comes from the access token; only its owner ever sees it.
	Email        *string
	AvatarKey    *string
	Language     *string
	CreatedAt    time.Time `gorm:"->"`
	UpdatedAt    time.Time `gorm:"->"`
	AnonymizedAt *time.Time
	// BlockedAt is when an admin blocked the account, nil while it is not:
	// every request of a blocked account but DELETE /me answers
	// account_blocked (blockedRefusal).
	BlockedAt *time.Time
}

// ProfileChanges are the profile fields to change; nil leaves a field as is.
// RemovePseudo clears the pseudo, which is optional, and is never set with
// Pseudo; the names and the language can only be changed.
type ProfileChanges struct {
	Pseudo       *string
	RemovePseudo bool
	FirstName    *string
	LastName     *string
	Language     *string
}

// PendingDeletion is a deletion at Auth0 still to do. UserID is the local
// account, empty for a deletion recorded before the link (SC-API-158).
type PendingDeletion struct {
	Subject string
	UserID  string
}

// PublicName is what the other users see of a user: never the e-mail. A
// name the user has not set yet is nil.
type PublicName struct {
	Pseudo    *string
	FirstName *string
	LastName  *string
}

// ProfileComplete reports whether the user set a first name and a last name,
// required to create or join a challenge and to record an activity; the
// pseudo is optional.
func (u User) ProfileComplete() bool {
	return u.FirstName != nil && u.LastName != nil
}

// subjectFingerprint is what is kept of the auth subject of a deleted
// account (table deleted_subjects): its SHA-256, never the subject itself,
// in hexadecimal (the queries decode it).
func subjectFingerprint(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}
