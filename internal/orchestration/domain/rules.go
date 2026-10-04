package domain

import (
	"errors"
	"sort"
	"time"

	"orkome/internal/contract/failuretax"
)

var ErrFenceHit = errors.New("agent_task_queue: pending fence hit")

const (
	Queued     = "queued"
	Dispatched = "dispatched"
	Running    = "running"
	Completed  = "completed"
	Failed     = "failed"
	Cancelled  = "cancelled"
	Deferred   = "deferred"
)

func AllStatuses() []string {
	return []string{Queued, Dispatched, Running, Completed, Failed, Cancelled, Deferred}
}

func Terminal(s string) bool { return s == Completed || s == Failed || s == Cancelled }

func CanClaim(s string) bool { return s == Queued }

func CanStart(s string) bool { return s == Dispatched }

func CanCancel(s string) bool { return s == Queued || s == Dispatched }

func CancellableByTriggerComment(s string) bool {
	return s == Queued || s == Dispatched || s == Deferred
}

const (
	DefaultMaxAttempts  = 2
	ProviderMaxAttempts = 3

	RetryFinalDelay = 5 * time.Second
)

func providerRelaxed(reason string) bool {
	return reason == failuretax.ProviderNetwork || reason == failuretax.ProviderOverload
}

func EffectiveMaxAttempts(reason string, maxAttempts int) int {
	if maxAttempts <= 1 {
		return maxAttempts
	}
	if providerRelaxed(reason) && maxAttempts < ProviderMaxAttempts {
		return ProviderMaxAttempts
	}
	return maxAttempts
}

func RetryEligible(reason string, attempt, maxAttempts int) bool {
	if !failuretax.Retryable(reason) {
		return false
	}
	return attempt < EffectiveMaxAttempts(reason, maxAttempts)
}

func RetryDelay(reason string, attempt, maxAttempts int) time.Duration {
	if !providerRelaxed(reason) {
		return 0
	}
	if !RetryEligible(reason, attempt, maxAttempts) {
		return 0
	}
	if attempt+1 == EffectiveMaxAttempts(reason, maxAttempts) {
		return RetryFinalDelay
	}
	return 0
}

const (
	SrcDirectHuman   = "direct_human"
	SrcDelegation    = "delegation"
	SrcCommentSrc    = "comment_source"
	SrcTriggerOwner  = "trigger_owner"
	SrcRuleOwner     = "rule_owner"
	SrcOwnerFallback = "owner_fallback"

	SrcBackfill = "backfill"

	SrcUnattributed = "unattributed"
)

func ResolveAttribution(originatorID, accountableID, ownerID, source string) (string, string) {
	if originatorID != "" {
		return originatorID, originatorID
	}
	switch source {
	case SrcRuleOwner:
		return "", accountableID
	case SrcOwnerFallback:
		return "", ownerID
	case SrcUnattributed:

		return "", accountableID
	case SrcBackfill:

		return "", ""
	}
	return "", accountableID
}

type MCPServer struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
}

func MergeMCP(workspace []MCPServer, agent []MCPServer) []MCPServer {
	out := make([]MCPServer, 0, len(workspace)+len(agent))
	taken := map[string]bool{}
	for _, s := range agent {
		if s.Enabled {
			out = append(out, s)
		}
		taken[s.Name] = true
	}
	for _, s := range workspace {
		if !taken[s.Name] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func ConcurrencyGate(running, maxConcurrent int) bool { return running < maxConcurrent }
