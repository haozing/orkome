package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveAttribution_Waterfall(t *testing.T) {
	cases := []struct {
		name                           string
		originator, accountable, owner string
		source                         string
		wantOriginator                 string
		wantAccountable                string
	}{

		{"direct_human dual tracks merged", "u1", "u1", "u9", SrcDirectHuman, "u1", "u1"},

		{"delegation responsible-person copy", "u2", "u2", "u9", SrcDelegation, "u2", "u2"},

		{"comment_source chain head is human", "u3", "u3", "u9", SrcCommentSrc, "u3", "u3"},

		{"trigger_owner armed means authorized", "u4", "u4", "u9", SrcTriggerOwner, "u4", "u4"},

		{"rule_owner audit only", "", "u5", "u9", SrcRuleOwner, "", "u5"},

		{"owner_fallback degradation", "", "u5", "u6", SrcOwnerFallback, "", "u6"},

		{"backfill with both tracks empty", "", "u5", "u6", SrcBackfill, "", ""},

		{"unattributed audit still lands", "", "u7", "u6", SrcUnattributed, "", "u7"},

		{"unknown level fallback", "", "u8", "u6", "autopilot_x", "", "u8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, a := ResolveAttribution(tc.originator, tc.accountable, tc.owner, tc.source)
			assert.Equal(t, tc.wantOriginator, o, "authorization track")
			assert.Equal(t, tc.wantAccountable, a, "audit trail")
		})
	}
}

func TestResolveAttribution_DualTrackInvariant(t *testing.T) {
	for _, src := range []string{SrcDirectHuman, SrcDelegation, SrcCommentSrc, SrcTriggerOwner} {
		o, a := ResolveAttribution("u1", "someone-else", "u9", src)
		assert.Equal(t, "u1", o, src)
		assert.Equal(t, "u1", a, "originator valid ⇒ accountable == originator (internal archive 05 §1): "+src)
	}

	for _, src := range []string{SrcRuleOwner, SrcOwnerFallback, SrcUnattributed, SrcBackfill} {
		o, _ := ResolveAttribution("", "acc", "own", src)
		assert.Empty(t, o, "forking allowed only when originator is NULL: "+src)
	}
}

func TestResolveAttribution_BackfillVsUnattributed(t *testing.T) {
	bo, ba := ResolveAttribution("", "acc", "own", SrcBackfill)
	assert.Empty(t, bo, "backfill authorization empty")
	assert.Empty(t, ba, "backfill audit empty (historical data does not over-claim)")

	uo, ua := ResolveAttribution("", "acc", "own", SrcUnattributed)
	assert.Empty(t, uo, "unattributed authorization empty (fail-closed)")
	assert.Equal(t, "acc", ua, "unattributed audit still lands accountable (traceable)")
}

func TestRetryEligible_PerReasonBudget(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		attempt     int
		maxAttempts int
		want        bool
	}{

		{"timeout default 2, retryable after first run", "timeout", 1, 2, true},
		{"timeout default 2, unusable after second failure", "timeout", 2, 2, false},
		{"runtime_recovery default 2", "runtime_recovery", 1, 2, true},
		{"skill_bundle_unavailable default 2", "skill_bundle_unavailable", 1, 2, true},

		{"provider_network relaxed 3 attempt 1", "agent_provider.network", 1, 2, true},
		{"provider_network relaxed 3 attempt 2 still ok", "agent_provider.network", 2, 2, true},
		{"provider_network attempt 3 exhausted", "agent_provider.network", 3, 2, false},
		{"provider_overload relaxed 3 attempt 2", "agent_provider.overload", 2, 2, true},
		{"provider_overload attempt 3 exhausted", "agent_provider.overload", 3, 2, false},

		{"max<=1 always disables timeout", "timeout", 1, 1, false},
		{"max<=1 always disables provider_network", "agent_provider.network", 1, 1, false},
		{"max<=1 always disables provider_overload", "agent_provider.overload", 1, 1, false},
		{"max=0 always disabled", "timeout", 1, 0, false},

		{"interrupt_timeout not in the allowlist", "interrupt_timeout", 1, 2, false},
		{"agent_error.context_overflow not in the allowlist", "agent_error.context_overflow", 1, 2, false},
		{"agent_provider.auth not in the allowlist", "agent_provider.auth", 1, 2, false},

		{"timeout explicit 3, attempt 2 retryable", "timeout", 2, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RetryEligible(tc.reason, tc.attempt, tc.maxAttempts))
		})
	}
}

func TestEffectiveMaxAttempts(t *testing.T) {
	assert.Equal(t, 2, EffectiveMaxAttempts("timeout", 2))
	assert.Equal(t, 3, EffectiveMaxAttempts("agent_provider.network", 2), "provider family relaxed to 3")
	assert.Equal(t, 3, EffectiveMaxAttempts("agent_provider.overload", 2))
	assert.Equal(t, 4, EffectiveMaxAttempts("agent_provider.network", 4), "explicitly higher respects the explicit value")
	assert.Equal(t, 1, EffectiveMaxAttempts("agent_provider.network", 1), "explicit disable never relaxed")
	assert.Equal(t, 0, EffectiveMaxAttempts("timeout", 0))
}

func TestRetryDelay_ThreeStage(t *testing.T) {
	assert.Equal(t, time.Duration(0), RetryDelay("timeout", 1, 2), "non-provider family zero delay")
	assert.Equal(t, time.Duration(0), RetryDelay("agent_provider.network", 1, 2),
		"second segment retries immediately (after first run)")
	assert.Equal(t, RetryFinalDelay, RetryDelay("agent_provider.network", 2, 2),
		"third segment (the delayed last one) ≈5s")
	assert.Equal(t, time.Duration(0), RetryDelay("agent_provider.overload", 3, 2), "no more delay after exhaustion")
	assert.Equal(t, time.Duration(0), RetryDelay("agent_provider.network", 1, 1),
		"explicit disable without delay")
	assert.Equal(t, time.Duration(0), RetryDelay("interrupt_timeout", 1, 2),
		"outside the allowlist without delay")
	assert.Equal(t, 5*time.Second, RetryFinalDelay, "internal archive spec: about 5s")
}

func TestCancellableByTriggerComment(t *testing.T) {
	for _, s := range []string{Queued, Dispatched, Deferred} {
		assert.True(t, CancellableByTriggerComment(s), "%s not started/registered medium, can be cancelled by comment", s)
	}
	for _, s := range []string{Running, Completed, Failed, Cancelled} {
		assert.False(t, CancellableByTriggerComment(s), "%s cannot be cancelled by comment", s)
	}
}
