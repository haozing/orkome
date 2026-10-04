package domain

import "testing"

func TestRenderActivityLine(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload map[string]any
		want    string
	}{
		{"create with title", "work_item.created", map[string]any{"title": "add RSS"}, "created work item (add RSS)"},
		{"create without title", "work_item.created", nil, "created work item"},
		{"update carries status and assignment", "work_item.updated",
			map[string]any{"title": "add RSS", "status": "doing", "assignee_type": "agent"},
			"updated work item (status doing / assigned agent) (add RSS)"},
		{"update without fields", "work_item.updated", map[string]any{"title": "x"}, "updated work item (x)"},
		{"delete", "work_item.deleted", nil, "deleted work item"},
		{"leader evaluation with member", "squad_leader_evaluated",
			map[string]any{"member": "writer", "verdict": "action"}, "leader evaluated writer: action"},
		{"leader evaluation without member", "squad_leader_evaluated",
			map[string]any{"verdict": "no_action"}, "leader evaluated: no_action"},
		{"task completed with agent name", "task_settled",
			map[string]any{"outcome": "completed", "agent_name": "verify-writer", "summary": "W3C validation passed"},
			"task completed (verify-writer): W3C validation passed"},
		{"task failure carries a reason", "task_settled",
			map[string]any{"outcome": "failed", "agent_name": "verify-writer", "failure_reason": "provider.timeout"},
			"task failed (verify-writer): provider.timeout"},
		{"task completed without agent name", "task_settled",
			map[string]any{"outcome": "completed", "summary": "done"}, "task completed: done"},
		{"unknown events degrade to the event name", "future.event", map[string]any{"x": 1}, "future.event"},
		{"empty event", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderActivityLine(tt.event, tt.payload); got != tt.want {
				t.Fatalf("RenderActivityLine(%q) = %q, want %q", tt.event, got, tt.want)
			}
		})
	}
}
