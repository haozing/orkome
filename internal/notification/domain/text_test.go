package domain

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncateRunes_ChineseNoSplit(t *testing.T) {
	s := strings.Repeat("あ", TitleMaxRunes+10)
	got := TruncateRunes(s, TitleMaxRunes)
	assert.True(t, utf8.ValidString(got), "CJK truncation must not split UTF-8")
	assert.Equal(t, TitleMaxRunes, len([]rune(got)), "ellipsis counts toward the cap")
	assert.Equal(t, strings.Repeat("あ", TitleMaxRunes-1)+"…", got)
}

func TestTruncateRunes_EmojiNotSplit(t *testing.T) {
	s := strings.Repeat("🎉", 50)
	got := TruncateRunes(s, 30)
	assert.True(t, utf8.ValidString(got))
	assert.Equal(t, 30, len([]rune(got)))
	assert.Equal(t, strings.Repeat("🎉", 29)+"…", got)
	assert.NotContains(t, got, "\uFFFD", "no replacement characters allowed")
}

func TestTruncateRunes_BoundaryExact(t *testing.T) {
	s := strings.Repeat("a", SummaryMaxRunes)
	assert.Equal(t, s, TruncateRunes(s, SummaryMaxRunes), "exactly n runes, no truncation")
	got := TruncateRunes(s+"x", SummaryMaxRunes)
	assert.Equal(t, strings.Repeat("a", SummaryMaxRunes-1)+"…", got)
}

func TestTruncateRunes_MixedAndEdge(t *testing.T) {
	assert.Equal(t, "", TruncateRunes("abc", 0), "n=0 → empty")
	assert.Equal(t, "", TruncateRunes("", 10), "empty string as-is")
	assert.Equal(t, "ab", TruncateRunes("ab", 10), "short string as-is")

	got := TruncateRunes("aあ🎉bあ🎉c", 5)
	assert.Equal(t, "aあ🎉b…", got)
	assert.True(t, utf8.ValidString(got))
}
