package failuretax

const (
	RuntimeRecovery        = "runtime_recovery"
	Timeout                = "timeout"
	InterruptTimeout       = "interrupt_timeout"
	QueuedExpired          = "queued_expired"
	Cancelled              = "cancelled"
	ModelNotConfigured     = "model_not_configured"
	SkillBundleUnavailable = "skill_bundle_unavailable"
)

const (
	AgentCtxOverflow    = "agent_error.context_overflow"
	AgentStateOversized = "agent_error.state_oversized"
	AgentIterationLimit = "agent_error.iteration_limit"
	AgentToolFailed     = "agent_error.tool_failed"
	ProviderNetwork     = "agent_provider.network"
	ProviderOverload    = "agent_provider.overload"
	ProviderAuth        = "agent_provider.auth"
	ProviderBadRequest  = "agent_provider.bad_request"
)

var RetryWhitelist = map[string]bool{
	RuntimeRecovery: true, Timeout: true, ProviderNetwork: true,
	ProviderOverload: true, SkillBundleUnavailable: true,
}

func Retryable(reason string) bool { return RetryWhitelist[reason] }

var ResumeUnsafe = map[string]bool{AgentCtxOverflow: true, AgentStateOversized: true}
