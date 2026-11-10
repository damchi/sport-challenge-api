package scoring

import (
	"cmp"
	"slices"
)

// Rank ranks the participants by value, highest first; a participant
// missing from values has 0. Equal values share the rank (1, 1, 3), with no
// arbitrary tie-break: the user ID only fixes the display order. Abandoned
// participants come last, unranked (rank 0).
func Rank(participants []Participant, values map[string]int) []Entry {
	entries := make([]Entry, len(participants))
	for i, p := range participants {
		entries[i] = Entry{UserID: p.UserID, Value: values[p.UserID], Abandoned: p.Abandoned}
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Abandoned != b.Abandoned {
			if a.Abandoned {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(b.Value, a.Value), cmp.Compare(a.UserID, b.UserID))
	})
	for i := range entries {
		switch {
		case entries[i].Abandoned:
		case i > 0 && entries[i].Value == entries[i-1].Value:
			entries[i].Rank = entries[i-1].Rank
		default:
			entries[i].Rank = i + 1
		}
	}
	return entries
}
