package slug

import (
	"strings"
	"unicode"
)

// Slugify lowercases s, turns every run of whitespace into one '_' and drops every other
// character that is not a letter, a digit or '_'.
func Slugify(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsSpace(r):
			if !inSpace {
				b.WriteByte('_')
			}
			inSpace = true
			continue
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
			b.WriteRune(r)
		}
		inSpace = false
	}
	return b.String()
}
