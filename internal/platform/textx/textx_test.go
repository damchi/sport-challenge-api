package textx

import (
	"strings"
	"testing"
)

// text builds a string from code points, in hexadecimal: escapes of
// invisible characters do not survive every editor.
func text(points ...rune) string {
	var b strings.Builder
	for _, r := range points {
		b.WriteRune(r)
	}
	return b.String()
}

func TestHasHidden(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		allowed string
		want    bool
	}{
		{name: "plain text", s: "Spring run, 2026"},
		{name: "accents", s: text('E', 0xe9, 't', 0xe9)},
		{name: "empty", s: ""},
		{name: "NUL", s: text('a', 0x0, 'b'), want: true},
		{name: "escape", s: text('a', 0x1b, 'b'), want: true},
		{name: "newline", s: "a\nb", want: true},
		{name: "newline allowed", s: "a\r\n\tb", allowed: "\n\r\t"},
		{name: "line separator", s: text('a', 0x2028, 'b'), want: true},
		{name: "paragraph separator", s: text('a', 0x2029, 'b'), want: true},
		{name: "separators allowed", s: text('a', 0x2028, 'b', 0x2029, 'c'), allowed: text(0x2028, 0x2029)},
		{name: "zero width space", s: text('a', 0x200b, 'b'), want: true},
		{name: "format character allowed", s: text('a', 0x200b, 'b'), allowed: text(0x200b)},
		{name: "right-to-left override", s: text('a', 0x202e, 'b'), want: true},
		{name: "right-to-left isolate", s: text('a', 0x2067, 'b'), want: true},
		{name: "byte order mark", s: text('a', 0xfeff, 'b'), want: true},
		{name: "soft hyphen", s: text('a', 0xad, 'b'), want: true},
		{name: "word joiner", s: text('a', 0x2060, 'b'), want: true},
		{name: "language tag", s: text('a', 0xe0001, 'b'), want: true},
		{name: "emoji joined", s: text(0x1f468, 0x200d, 0x1f469, 0x200d, 0x1f467, ' ', 0x1f3c3, 0x200d, 0x2640, 0xfe0f)},
		{name: "Persian non joiner", s: text(0x645, 0x6cc, 0x200c, 0x62e, 0x648, 0x627, 0x647, 0x645)},
		{name: "direction marks", s: text('R', 'u', 'n', ' ', 0x200e, 0x5e8, 0x5d9, 0x5e6, 0x5d4, 0x200f)},
		{name: "flag of Scotland", s: text(0x1f3f4, 0xe0067, 0xe0062, 0xe0073, 0xe0063, 0xe0074, 0xe007f)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasHidden(tt.s, tt.allowed); got != tt.want {
				t.Fatalf("HasHidden(%q, %q) = %v, want %v", tt.s, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestVisibleLen(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want int
	}{
		{name: "empty", s: "", want: 0},
		{name: "letters", s: "Run", want: 3},
		{name: "joiners only", s: text(0x200d, 0x200d, 0x200d), want: 0},
		{name: "marks and non joiners only", s: text(0x200c, 0x200e, 0x200f), want: 0},
		{name: "tags only", s: text(0xe0067, 0xe0062), want: 0},
		{name: "emoji joined", s: text(0x1f468, 0x200d, 0x1f469), want: 2},
		{name: "refused format still counted", s: text('a', 0x200b, 'b'), want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VisibleLen(tt.s); got != tt.want {
				t.Fatalf("VisibleLen(%q) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}
