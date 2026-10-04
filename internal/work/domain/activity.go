package domain

import (
	"fmt"
	"strings"
)

func RenderActivityLine(event string, payload map[string]any) string {
	if event == "" {
		return ""
	}
	switch event {
	case "work_item.created":
		return withTitle("created work item", payload)
	case "work_item.updated":
		return withTitle(updateDetail(payload), payload)
	case "work_item.deleted":
		return withTitle("deleted work item", payload)
	case "squad_leader_evaluated":
		verdict, _ := payload["verdict"].(string)
		member, _ := payload["member"].(string)
		if member != "" {
			return fmt.Sprintf("leader evaluated %s: %s", member, verdict)
		}
		return "leader evaluated: " + verdict
	case "task_settled":
		outcome, _ := payload["outcome"].(string)
		agent, _ := agentLabel(payload)
		summary, _ := payload["summary"].(string)
		switch outcome {
		case "completed":
			return fmt.Sprintf("task completed%s: %s", agent, summary)
		case "failed":
			reason, _ := payload["failure_reason"].(string)
			return fmt.Sprintf("task failed%s: %s", agent, reason)
		}
		return fmt.Sprintf("task settled%s: %s", agent, summary)
	}
	return event
}

func withTitle(base string, payload map[string]any) string {
	if title, _ := payload["title"].(string); title != "" {
		return base + " (" + title + ")"
	}
	return base
}

func updateDetail(payload map[string]any) string {
	var parts []string
	if v, _ := payload["status"].(string); v != "" {
		parts = append(parts, "status "+v)
	}
	if v, _ := payload["assignee_type"].(string); v != "" && v != "none" {
		parts = append(parts, "assigned "+v)
	}
	if len(parts) == 0 {
		return "updated work item"
	}
	return "updated work item (" + strings.Join(parts, " / ") + ")"
}

func agentLabel(payload map[string]any) (string, bool) {
	name, _ := payload["agent_name"].(string)
	if name == "" {
		return "", false
	}
	return " (" + name + ")", true
}
