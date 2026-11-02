// Package user manages the local users: one per identity at the auth
// provider, created on first login.
package user

import "time"

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
