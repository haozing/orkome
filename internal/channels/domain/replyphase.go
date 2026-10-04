package domain

const (
	ReplyPhaseStreaming = "streaming"
	ReplyPhaseTerminal  = "terminal"
	ReplyPhaseSettled   = "settled"
)

const (
	CardStatusPending   = "pending"
	CardStatusStreaming = "streaming"
	CardStatusFinal     = "final"
	CardStatusError     = "error"
)

const StreamingPlaceholderText = "…"

func ReplyPhaseMayOpenPlaceholder(phase string) bool {
	return phase == "" || phase == ReplyPhaseStreaming
}

func ReplyPhaseMayDeliverTerminal(phase string) bool {
	return phase != ReplyPhaseSettled
}

func CardStatusMayFinish(status string) bool {
	return status == CardStatusPending || status == CardStatusStreaming || status == CardStatusFinal
}
