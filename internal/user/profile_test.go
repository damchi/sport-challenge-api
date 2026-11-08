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
	if (User{}).ProfileComplete() {
		t.Error("profile without pseudo is complete")
	}
	if !(User{Pseudo: ptr("marie")}).ProfileComplete() {
		t.Error("profile with a pseudo is incomplete")
	}
}
