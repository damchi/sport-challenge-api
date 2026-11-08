package user

import "regexp"

// pseudoPattern is the pseudo rule of README §2.8: 3 to 20 ASCII letters,
// digits, "_", "." or "-". Uniqueness, regardless of case, is checked by the
// database.
var pseudoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,20}$`)

// languages are the languages of the apps (README §2.8).
var languages = map[string]bool{"fr": true, "en": true}

// invalidProfileFields returns the fields of changes that break the profile
// rules, in alphabetical order.
func invalidProfileFields(changes ProfileChanges) []string {
	var fields []string
	if changes.Language != nil && !languages[*changes.Language] {
		fields = append(fields, "language")
	}
	if changes.Pseudo != nil && !pseudoPattern.MatchString(*changes.Pseudo) {
		fields = append(fields, "pseudo")
	}
	return fields
}
