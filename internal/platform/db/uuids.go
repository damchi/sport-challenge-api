package db

import (
	"database/sql/driver"
	"strings"
)

// UUIDs passes a list of UUIDs as one parameter, a PostgreSQL array
// literal, to compare with `= ANY(CAST(? AS uuid[]))`. GORM would expand
// a plain slice into one placeholder per item, and PostgreSQL takes at most
// 65,535 parameters per query: the list of every participant of a challenge
// has no such bound (README §5.5).
type UUIDs []string

// arrayQuoter escapes an item of an array literal, between double quotes.
var arrayQuoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// Value returns the array literal of u: `{"a","b"}`, each item quoted, so
// that a value that is not a UUID fails as such instead of splitting.
func (u UUIDs) Value() (driver.Value, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, id := range u {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(arrayQuoter.Replace(id))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}
