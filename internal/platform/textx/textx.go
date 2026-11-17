// Package textx checks the free texts that users write and others read: a
// challenge title or description, the label of a sport.
package textx

import (
	"strings"
	"unicode"
)

// HasHidden reports whether s holds a character with no place in a free
// text, other than those of allowed:
//   - a control character: PostgreSQL refuses NUL in a text column, and the
//     others have no place in a title or a label (a description allows line
//     breaks and tabs);
//   - a line or paragraph separator (U+2028, U+2029), a line break as "\n";
//   - a format character (Unicode Cf), which hides text or turns it around
//     (U+200B, U+202E): a title could pass for another. Those that writing
//     needs stay (see keptFormat).
func HasHidden(s, allowed string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		if strings.ContainsRune(allowed, r) {
			return false
		}
		switch {
		case unicode.IsControl(r), r == lineSeparator, r == paragraphSeparator:
			return true
		case unicode.Is(unicode.Cf, r):
			return !keptFormat(r)
		}
		return false
	})
}

// VisibleLen counts the characters of s that are seen: the format
// characters kept by HasHidden show nothing alone, so a minimum length counts
// without them (a title of joiners only would show empty).
func VisibleLen(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.Is(unicode.Cf, r) || !keptFormat(r) {
			n++
		}
	}
	return n
}

// Code points, in hexadecimal: written as escapes or as characters, they
// are invisible in the source.
const (
	lineSeparator      = 0x2028
	paragraphSeparator = 0x2029
	zeroWidthNonJoiner = 0x200c
	zeroWidthJoiner    = 0x200d
	leftToRightMark    = 0x200e
	rightToLeftMark    = 0x200f
	firstFlagTag       = 0xe0020
	lastFlagTag        = 0xe007f
)

// keptFormat reports whether the format character r is kept: the joiners
// of emojis made of several (U+200D) and of Persian or Indic words (U+200C),
// the marks that set the direction of text mixing Arabic or Hebrew with
// Latin (U+200E, U+200F), and the tags of the flags of England, Scotland and
// Wales (U+E0020 to U+E007F).
func keptFormat(r rune) bool {
	switch {
	case r == zeroWidthNonJoiner, r == zeroWidthJoiner, r == leftToRightMark, r == rightToLeftMark:
		return true
	case r >= firstFlagTag && r <= lastFlagTag:
		return true
	}
	return false
}
