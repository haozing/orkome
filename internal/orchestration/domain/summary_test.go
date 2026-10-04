package domain

import (
	"strings"
	"testing"
)

func TestTaskSummary(t *testing.T) {
	tests := []struct {
		name     string
		lastText string
		fallback string
		want     string
	}{
		{"last text priority", "conclusion: RSS done", "full-text result…", "conclusion: RSS done"},
		{"text whitespace degrades to fallback", "\n\t", "full result", "full result"},
		{"both empty", "", "", ""},
		{"overlong truncated to 500 runes", strings.Repeat("l", 600), "", strings.Repeat("l", SummaryMaxRunes)},
		{"fallback truncates when too long as well", "", strings.Repeat("x", 900), strings.Repeat("x", SummaryMaxRunes)},
		{"multi-byte truncated by rune", strings.Repeat("é", 501), "", strings.Repeat("é", SummaryMaxRunes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TaskSummary(tt.lastText, tt.fallback); got != tt.want {
				t.Fatalf("TaskSummary = %q (len %d), want %q (len %d)",
					got, len([]rune(got)), tt.want, len([]rune(tt.want)))
			}
		})
	}
}
