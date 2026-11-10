package scoring

import (
	"cmp"
	"slices"
)

// Rank ranks the participants by value, highest first; a participant
// missing from values has 0. Equal values share the rank (1, 1, 3), with no
// arbitrary tie-break: the user ID only fixes the display order. Abandoned
// participants come last, unranked (rank 0) and at 0: their activities no
// longer count.
func Rank(participants []Participant, values map[string]int) []Entry {
	return rankByTier(participants, values, nil)
}

// rankByTier is Rank with a tier that comes before the value: the higher
// tier ranks first, and only the same tier and value share a rank. A
// participant missing from tiers is in tier 0.
func rankByTier(participants []Participant, values, tiers map[string]int) []Entry {
	entries := make([]Entry, len(participants))
	for i, p := range participants {
		entries[i] = Entry{UserID: p.UserID, Abandoned: p.Abandoned}
		if !p.Abandoned {
			entries[i].Value = values[p.UserID]
		}
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Abandoned != b.Abandoned {
			if a.Abandoned {
				return 1
			}
			return -1
		}
		return cmp.Or(
			cmp.Compare(tiers[b.UserID], tiers[a.UserID]),
			cmp.Compare(b.Value, a.Value),
			cmp.Compare(a.UserID, b.UserID),
		)
	})
	for i := range entries {
		switch {
		case entries[i].Abandoned:
		case i > 0 && entries[i].Value == entries[i-1].Value && tiers[entries[i].UserID] == tiers[entries[i-1].UserID]:
			entries[i].Rank = entries[i-1].Rank
		default:
			entries[i].Rank = i + 1
		}
	}
	return entries
}
