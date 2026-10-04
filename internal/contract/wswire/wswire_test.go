package wswire

import "testing"

func TestWSName(t *testing.T) {
	cases := map[string]string{
		"task.queued":             "task:queued",
		"task.awaiting_input":     "task:awaiting_input",
		"work_item.created":       "work_item:created",
		"notification.batch_read": "notification:batch_read",
		"chat.done":               "chat:done",
		"chat.cancel_finalized":   "chat:cancel_finalized",
	}
	for dot, colon := range cases {
		if got := WSName(dot); got != colon {
			t.Errorf("WSName(%q)=%q want %q", dot, got, colon)
		}
	}
}
