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
	// SkippedByRequest 评论显式要求跳过触发面（助理接入 add_comment 的
	// skipTrigger：AI 代用户评论不得意外触发 agent 派发）。
	SkippedByRequest = "skipped_by_request"
)
