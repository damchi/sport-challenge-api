package user

import (
	"slices"
	"strings"
	"testing"
)

func TestInvalidProfileFields(t *testing.T) {
	tests := []struct {
		name    string
		changes ProfileChanges
		want    []string
	}{
		{name: "nothing", changes: ProfileChanges{}},
		{name: "3 characters", changes: ProfileChanges{Pseudo: ptr("abc")}},
		{name: "20 characters", changes: ProfileChanges{Pseudo: ptr(strings.Repeat("a", 20))}},
		{name: "every allowed character", changes: ProfileChanges{Pseudo: ptr("Az09_.-")}},
		{name: "2 characters", changes: ProfileChanges{Pseudo: ptr("ab")}, want: []string{"pseudo"}},
		{name: "21 characters", changes: ProfileChanges{Pseudo: ptr(strings.Repeat("a", 21))}, want: []string{"pseudo"}},
		{name: "empty", changes: ProfileChanges{Pseudo: ptr("")}, want: []string{"pseudo"}},
		{name: "space", changes: ProfileChanges{Pseudo: ptr("marie 92")}, want: []string{"pseudo"}},
		{name: "accent (ASCII only)", changes: ProfileChanges{Pseudo: ptr("élodie")}, want: []string{"pseudo"}},
		{name: "other symbol", changes: ProfileChanges{Pseudo: ptr("marie@92")}, want: []string{"pseudo"}},
		{name: "trailing newline", changes: ProfileChanges{Pseudo: ptr("marie\n")}, want: []string{"pseudo"}},
		{name: "french", changes: ProfileChanges{Language: ptr("fr")}},
		{name: "english", changes: ProfileChanges{Language: ptr("en")}},
		{name: "other language", changes: ProfileChanges{Language: ptr("de")}, want: []string{"language"}},
		{name: "uppercase language", changes: ProfileChanges{Language: ptr("EN")}, want: []string{"language"}},
		{name: "both invalid, sorted", changes: ProfileChanges{Pseudo: ptr("x"), Language: ptr("")}, want: []string{"language", "pseudo"}},
		{name: "simple names", changes: ProfileChanges{FirstName: ptr("Marie"), LastName: ptr("Roy")}},
		{name: "one letter", changes: ProfileChanges{FirstName: ptr("J")}},
		{name: "accents and hyphen", changes: ProfileChanges{FirstName: ptr("Marie-Ève"), LastName: ptr("Bélanger-Côté")}},
		{name: "apostrophes, straight and curly", changes: ProfileChanges{LastName: ptr("D'Amours"), FirstName: ptr("N’Golo")}},
		{name: "space between words", changes: ProfileChanges{LastName: ptr("Van der Berg")}},
		{name: "other alphabet", changes: ProfileChanges{FirstName: ptr("Алёна"), LastName: ptr("王")}},
		{name: "combining accent", changes: ProfileChanges{FirstName: ptr("Zoe\u0301")}},
		{name: "50 characters", changes: ProfileChanges{LastName: ptr(strings.Repeat("é", 50))}},
		{name: "51 characters", changes: ProfileChanges{LastName: ptr(strings.Repeat("é", 51))}, want: []string{"last_name"}},
		{name: "empty name", changes: ProfileChanges{FirstName: ptr("")}, want: []string{"first_name"}},
		{name: "leading space", changes: ProfileChanges{FirstName: ptr(" Marie")}, want: []string{"first_name"}},
		{name: "trailing hyphen", changes: ProfileChanges{FirstName: ptr("Marie-")}, want: []string{"first_name"}},
		{name: "two spaces", changes: ProfileChanges{LastName: ptr("Van  Berg")}, want: []string{"last_name"}},
		{name: "digit", changes: ProfileChanges{LastName: ptr("Roy2")}, want: []string{"last_name"}},
		{name: "symbol", changes: ProfileChanges{LastName: ptr("Roy!")}, want: []string{"last_name"}},
		{name: "newline", changes: ProfileChanges{FirstName: ptr("Marie\n")}, want: []string{"first_name"}},
		{name: "starts with a combining accent", changes: ProfileChanges{FirstName: ptr("\u0301Zoe")}, want: []string{"first_name"}},
		{name: "word after a hyphen starts with a combining accent", changes: ProfileChanges{LastName: ptr("Roy-\u0301a")}, want: []string{"last_name"}},
		{name: "all invalid, sorted", changes: ProfileChanges{Pseudo: ptr("x"), FirstName: ptr(""), LastName: ptr("1"), Language: ptr("de")}, want: []string{"first_name", "language", "last_name", "pseudo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := invalidProfileFields(tt.changes); !slices.Equal(got, tt.want) {
				t.Fatalf("invalidProfileFields() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProfileComplete(t *testing.T) {
	tests := []struct {
		name string
		user User
		want bool
	}{
		{name: "empty", user: User{}},
		{name: "pseudo only", user: User{Pseudo: ptr("marie")}},
		{name: "names only", user: User{FirstName: ptr("Marie"), LastName: ptr("Roy")}},
		{name: "no last name", user: User{Pseudo: ptr("marie"), FirstName: ptr("Marie")}},
		{name: "pseudo and names", user: User{Pseudo: ptr("marie"), FirstName: ptr("Marie"), LastName: ptr("Roy")}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.ProfileComplete(); got != tt.want {
				t.Fatalf("ProfileComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenEmail(t *testing.T) {
	tests := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{raw: "marie@example.com", want: "marie@example.com", wantOK: true},
		{raw: "  marie@example.com\n", want: "marie@example.com", wantOK: true},
		{raw: ""},
		{raw: "marie"},
		{raw: "@"},
		{raw: strings.Repeat("a", 250) + "@x.ca"},
		{raw: "mar\x00ie@example.com"},
	}
	for _, tt := range tests {
		got, ok := tokenEmail(tt.raw)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("tokenEmail(%q) = %q, %v, want %q, %v", tt.raw, got, ok, tt.want, tt.wantOK)
		}
	}
}
