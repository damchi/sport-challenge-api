package user

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pseudoPattern is the pseudo rule of README §2.8: 3 to 20 ASCII letters,
// digits, "_", "." or "-". Uniqueness, regardless of case, is checked by the
// database.
var pseudoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,20}$`)

// namePattern is the rule of a first or last name (README §2.8): letters of
// any alphabet, accents included, in words separated by one space, "-", "'"
// or "’", with no space at either end; each word starts with a letter, never
// with a combining accent. maxNameLength bounds it in
// characters.
var namePattern = regexp.MustCompile(`^\p{L}[\p{L}\p{M}]*(?:[ '’-]\p{L}[\p{L}\p{M}]*)*$`)

const maxNameLength = 50

// maxEmailLength is the longest e-mail address kept (RFC 5321 path limit).
const maxEmailLength = 254

// languages are the languages of the apps (README §2.8).
var languages = map[string]bool{"fr": true, "en": true}

// invalidProfileFields returns the fields of changes that break the profile
// rules, in alphabetical order.
func invalidProfileFields(changes ProfileChanges) []string {
	var fields []string
	if changes.FirstName != nil && !validName(*changes.FirstName) {
		fields = append(fields, "first_name")
	}
	if changes.Language != nil && !languages[*changes.Language] {
		fields = append(fields, "language")
	}
	if changes.LastName != nil && !validName(*changes.LastName) {
		fields = append(fields, "last_name")
	}
	if changes.Pseudo != nil && !pseudoPattern.MatchString(*changes.Pseudo) {
		fields = append(fields, "pseudo")
	}
	return fields
}

func validName(name string) bool {
	return utf8.RuneCountInString(name) <= maxNameLength && namePattern.MatchString(name)
}

// tokenEmail returns the e-mail of the access token to keep, and false when
// there is none worth keeping: missing (the claim is not set) or not
// shaped like an address. The auth provider verified it; it is only trimmed.
func tokenEmail(raw string) (string, bool) {
	email := strings.TrimSpace(raw)
	if len(email) < 3 || len(email) > maxEmailLength || !strings.Contains(email, "@") || strings.ContainsFunc(email, unicode.IsControl) {
		return "", false
	}
	return email, true
}
