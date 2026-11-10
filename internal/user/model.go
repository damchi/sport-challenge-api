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
	ID           string `gorm:"->"`
	AuthSubject  string
	Pseudo       *string
	AvatarKey    *string
	Language     *string
	CreatedAt    time.Time `gorm:"->"`
	UpdatedAt    time.Time `gorm:"->"`
	AnonymizedAt *time.Time
}

// ProfileChanges are the profile fields to change; nil leaves a field as is.
type ProfileChanges struct {
	Pseudo   *string
	Language *string
}

// ProfileComplete reports whether the user chose a pseudo, required to
// create or join a challenge.
func (u User) ProfileComplete() bool {
	return u.Pseudo != nil
}

// subjectFingerprint is what is kept of the auth subject of a deleted
// account (table deleted_subjects): its SHA-256, never the subject itself,
// in hexadecimal (the queries decode it).
func subjectFingerprint(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}
