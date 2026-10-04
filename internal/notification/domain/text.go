package domain

import "unicode/utf8"

const (
	TitleMaxRunes = 200

	SummaryMaxRunes = 2000

	BodyMaxRunes = 4000
)

func TruncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	cut, seen := 0, 0
	for off := range s {
		if seen == n-1 {
			cut = off
			break
		}
		seen++
	}
	return s[:cut] + "…"
}
