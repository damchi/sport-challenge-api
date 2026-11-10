package invitation

import "time"

// Invitation is a row of the invitations table: a link to a challenge,
// reusable by several people until it expires at the end of week 1 or is
// revoked (column revoked_at, which no use case writes yet).
type Invitation struct {
	ID          string `gorm:"->"`
	ChallengeID string
	InvitedBy   string
	// TokenHash is the SHA-256 of the token: the token itself is never
	// stored.
	TokenHash []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}
