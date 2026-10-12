package domain

import "strings"

// SafeText is valid PostgreSQL text even when an upstream/model error contains NUL or partial UTF-8.
func SafeText(s string, maxRunes int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\x00", "�")
	r := []rune(s)
	if maxRunes > 0 && len(r) > maxRunes {
		r = r[:maxRunes]
	}
	return string(r)
}
