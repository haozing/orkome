package domain

const (
	BatchPending   = "pending"
	BatchRunning   = "running"
	BatchPaused    = "paused"
	BatchCompleted = "completed"
	BatchFailed    = "failed"
	BatchCancelled = "cancelled"

	ItemPending    = "pending"
	ItemClaimed    = "claimed"
	ItemProcessing = "processing"
	ItemSucceeded  = "succeeded"
	ItemFailed     = "failed"
	ItemSkipped    = "skipped"
)

func BatchTerminal(s string) bool {
	return s == BatchCompleted || s == BatchFailed || s == BatchCancelled
}

func ItemTerminal(s string) bool {
	return s == ItemSucceeded || s == ItemFailed || s == ItemSkipped
}

const MaxItemsPerBatch = 100000
