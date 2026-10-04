package domain

const (
	KindMessage        = "message"
	KindNoResponse     = "no_response"
	KindSystemStopped  = "system_stopped"
	KindChannelCommand = "channel_command"

	KindAgentIntro = "agent_intro"
)

const (
	OutcomeStopped  = "stopped"
	OutcomeRestored = "restored"

	OutcomeNone = ""
)

const (
	TaskTerminalCancelled = "cancelled"
	TaskTerminalCompleted = "completed"
	TaskTerminalFailed    = "failed"
)

func ShouldVerdictTerminal(terminalStatus string) bool {
	return terminalStatus == TaskTerminalCancelled
}

func ShouldDeferCancel(transcriptCount int) bool { return transcriptCount == 0 }

func FinalizeOutcome(assistantCount int) string {
	if assistantCount == 0 {
		return OutcomeRestored
	}
	return OutcomeStopped
}

const PrivacyDraftNotBroadcast = true
