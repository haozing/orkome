package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldDebounce(t *testing.T) {
	now := time.Now()
	assert.False(t, ShouldDebounce(time.Time{}, now), "no history = independent run")
	assert.True(t, ShouldDebounce(now.Add(-DebounceWindow+time.Millisecond), now), "within window = merge")
	assert.False(t, ShouldDebounce(now.Add(-DebounceWindow), now), "outside the window boundary = independent")
	assert.True(t, ShouldDebounce(now.Add(-1*time.Second), now))
}

func TestDebounceKeySeparatesGenerations(t *testing.T) {

	a := DebounceKey{SessionID: "s1", ContextRevision: 1}
	b := DebounceKey{SessionID: "s1", ContextRevision: 2}
	assert.NotEqual(t, a, b)
}

func TestDedupStates(t *testing.T) {
	assert.True(t, DedupCanClaim(DedupSeen))
	assert.False(t, DedupCanClaim(DedupClaimed))
	assert.False(t, DedupCanClaim(DedupProcessed))
	assert.True(t, DedupIsDuplicate(DedupClaimed))
	assert.True(t, DedupIsDuplicate(DedupProcessed))
	assert.False(t, DedupIsDuplicate(DedupSeen))
}

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"first non-empty line", "\n\n next week's release plan\nsecond line ignored", "next week's release plan"},
		{"strip Markdown marks", "**bold** and `code`", "bold and code"},
		{"link takes the text", "[document](https://x.io) here", "document here"},
		{"image link takes the alt text (first line only)", "![image](https://x.io/a.png) caption", "image caption"},
		{"multi-line code blocks do not apply off the first line", "```go\nfmt\n``` after", "go"},
		{"whitespace folding applies to the first line only", "a\t b\n c", "a b"},
		{"empty body", "\n\t ", ""},
		{"as-is within 30 code points", "one two three four five", "one two three four five"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DeriveTitle(tc.in))
		})
	}
}

func TestDeriveTitleTruncation(t *testing.T) {

	long := ""
	for i := 0; i < 40; i++ {
		long += "zh"
	}
	got := DeriveTitle(long)
	runes := []rune(got)
	assert.Len(t, runes, TitleLimit, "exactly 30 code points after truncation (including ellipsis)")
	assert.Equal(t, "…", string(runes[len(runes)-1]))
	assert.Equal(t, string([]rune("zh")[:0])+string(runes[:TitleLimit-1]), string(runes[:TitleLimit-1]))

	exact := ""
	for i := 0; i < TitleLimit; i++ {
		exact += "x"
	}
	assert.Equal(t, exact, DeriveTitle(exact))
}

func TestMediaLedgerRules(t *testing.T) {
	now := time.Now()
	assert.False(t, MediaAbandoned(now.Add(-MediaSettleDelay+time.Second), now), "still bindable inside the quiet window")
	assert.True(t, MediaAbandoned(now.Add(-MediaSettleDelay), now), "stale entries outside the window reclaimable")

	assert.Equal(t, mediaBackoffBase, MediaRetryDelay(1))
	assert.Equal(t, 2*mediaBackoffBase, MediaRetryDelay(2))
	assert.Equal(t, 4*mediaBackoffBase, MediaRetryDelay(3))

	var big time.Duration = mediaBackoffCap
	for i := 1; i < 40; i++ {
		big = MediaRetryDelay(i)
	}
	assert.Equal(t, mediaBackoffCap, big)

	assert.True(t, MediaIntentRefBindEligible(MediaPending))
	assert.False(t, MediaIntentRefBindEligible(MediaDeleting), "bindings must not revive objects after claim reclamation")
	assert.False(t, MediaIntentRefBindEligible(MediaTombstoned))
}

func TestTombstoneScheduleWidens(t *testing.T) {
	assert.Equal(t, 15*time.Minute, TombstoneNextDelay(0))
	assert.Equal(t, time.Hour, TombstoneNextDelay(1))
	assert.Equal(t, 6*time.Hour, TombstoneNextDelay(2))
	assert.Equal(t, 24*time.Hour, TombstoneNextDelay(3))
	assert.Equal(t, 24*time.Hour, TombstoneNextDelay(99), "beyond the table tail, repeats the tail value")
	assert.Equal(t, 15*time.Minute, TombstoneNextDelay(-1), "negative steps treated as 0")
}

func TestCredentialFingerprint(t *testing.T) {
	a := CredentialFingerprint("fake", []byte(`{"a":1}`))
	assert.Equal(t, a, CredentialFingerprint("fake", []byte(`{"a":1}`)), "same input same fingerprint (premise for drift detection)")
	assert.NotEqual(t, a, CredentialFingerprint("fake", []byte(`{"a":2}`)), "config drift → fingerprint changes")
	assert.NotEqual(t, a, CredentialFingerprint("feishu", []byte(`{"a":1}`)), "kind participates in the fingerprint")

	assert.NotEqual(t, CredentialFingerprint("ab", []byte("c")), CredentialFingerprint("a", []byte("bc")))
	assert.Len(t, a, 64, "sha256 hex")
}
