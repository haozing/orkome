package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMintWebhookToken_NotEmptyAndUnique(t *testing.T) {
	a, err := MintWebhookToken()
	require.NoError(t, err)
	b, err := MintWebhookToken()
	require.NoError(t, err)
	require.Len(t, a, 40)
	require.NotEqual(t, a, b, "tokens must be minted fresh each time (UNIQUE column)")
}

func TestMintSigningSecret_LengthAndEntropy(t *testing.T) {
	a, err := MintSigningSecret()
	require.NoError(t, err)
	b, err := MintSigningSecret()
	require.NoError(t, err)
	require.Len(t, a, 64)
	require.NotEqual(t, a, b, "secrets must be minted fresh each time")
}

func TestValidateSigningSecret(t *testing.T) {
	require.Error(t, ValidateSigningSecret(""), "empty string must be rejected")
	require.Error(t, ValidateSigningSecret(strings.Repeat("x", 31)), "  31 bytes must be rejected")
	require.NoError(t, ValidateSigningSecret(strings.Repeat("x", 32)))
	require.NoError(t, ValidateSigningSecret(strings.Repeat("x", 128)))
	require.Error(t, ValidateSigningSecret(strings.Repeat("x", 129)), "  129 bytes must be rejected")
}

func TestSecretFingerprint(t *testing.T) {
	fp := SecretFingerprint("some-secret-value-with-enough-length")
	require.Len(t, fp, 8, "fingerprint = first 8 hex of sha256")
	require.Equal(t, fp, SecretFingerprint("some-secret-value-with-enough-length"))
	require.NotEqual(t, fp, SecretFingerprint("some-secret-value-with-enough-lengtX"))
}

func TestQuotaWindow_UTCDayBoundary(t *testing.T) {

	now := time.Date(2026, 10, 2, 23, 4, 5, 0, time.FixedZone("CST", 8*3600))
	start, end := QuotaWindow(now)
	require.Equal(t, "2026-10-02T00:00:00Z", start.UTC().Format(time.RFC3339))
	require.Equal(t, "2026-10-03T00:00:00Z", end.UTC().Format(time.RFC3339))
	require.Equal(t, 24*time.Hour, end.Sub(start))
}

func TestDispatchConstants_MatchSpec(t *testing.T) {

	require.Equal(t, 2*time.Second, DispatchBudget)
	require.Equal(t, 50*time.Millisecond, DispatchLockTimeout)
}

func TestWebhookBackoff_RhythmTable(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, 64 * time.Second},
		{7, 64 * time.Second},
		{20, 64 * time.Second},
		{-1, 1 * time.Second},
	}
	for _, c := range cases {
		require.Equal(t, c.want, WebhookBackoff(c.attempts), "attempts=%d", c.attempts)
	}
	require.Equal(t, 5, WebhookMaxAttempts, "exhaustion threshold = 5 attempts")
}

func TestFailureFuseTripped_Boundaries(t *testing.T) {
	cases := []struct {
		runs, failed int
		want         bool
	}{
		{49, 49, false},
		{50, 44, false},
		{50, 45, true},
		{100, 89, false},
		{100, 90, true},
		{50, 50, true},
		{10, 10, false},
		{50, 51, false},
		{0, 0, false},
		{50, 0, false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, FailureFuseTripped(c.runs, c.failed), "runs=%d failed=%d", c.runs, c.failed)
	}
}
