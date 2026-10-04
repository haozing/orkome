package domain

import "strings"

const SummaryMaxRunes = 500

func TaskSummary(lastText, fallback string) string {
	s := strings.TrimSpace(lastText)
	if s == "" {
		s = strings.TrimSpace(fallback)
	}
	r := []rune(s)
	if len(r) <= SummaryMaxRunes {
		return s
	}
	return string(r[:SummaryMaxRunes])
}
