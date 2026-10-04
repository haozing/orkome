package dispatchreason

const (
	Queued    = "queued"
	Coalesced = "coalesced"
	Deferred  = "deferred"
	Steered   = "steered"
	Blocked   = "blocked"
)

const (
	InvocationNotAllowed  = "invocation_not_allowed"
	TargetUnavailable     = "target_unavailable"
	AgentArchived         = "agent_archived"
	WorkItemInTriage      = "work_item_in_triage"
	QuotaExceeded         = "quota_exceeded"
	SelfTriggerSuppressed = "self_trigger_suppressed"
	AlreadyActive         = "already_active"
	ModelNotConfigured    = "model_not_configured"
	TriggerEngineNotWired = "trigger_engine_not_wired"
)
