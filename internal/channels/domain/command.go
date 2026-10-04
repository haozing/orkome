package domain

import "strings"

const (
	freshCommandPrefix    = "/clear"
	newChatCommandPrefix  = "/new"
	workItemCommandPrefix = "/task"
)

type ControlCommandKind uint8

const (
	ControlCommandNone ControlCommandKind = iota
	ControlCommandFreshSession
	ControlCommandNewChat
	ControlCommandCreateWorkItem
)

type ControlCommand struct {
	Kind ControlCommandKind
	Body string
}

type WorkItemCommand struct {
	Title       string
	Description string
}

func ParseControlCommand(body string) ControlCommand {
	if rest, ok := parseLeadingCommand(body, newChatCommandPrefix); ok {
		return ControlCommand{Kind: ControlCommandNewChat, Body: rest}
	}
	if rest, ok := parseLeadingCommand(body, freshCommandPrefix); ok {
		return ControlCommand{Kind: ControlCommandFreshSession, Body: rest}
	}
	return ControlCommand{Kind: ControlCommandNone}
}

func ParseWorkItemCommand(body string) (WorkItemCommand, bool) {
	lines := strings.Split(body, "\n")
	i := firstNonEmptyLine(lines)
	if i < 0 {
		return WorkItemCommand{}, false
	}
	trimmed := strings.TrimLeft(lines[i], " \t")
	if !strings.HasPrefix(trimmed, workItemCommandPrefix) {
		return WorkItemCommand{}, false
	}
	rest := trimmed[len(workItemCommandPrefix):]
	if rest != "" {
		if r0 := rest[0]; r0 != ' ' && r0 != '\t' {
			return WorkItemCommand{}, false
		}
	}

	title := strings.TrimSpace(rest)
	description := ""
	if i+1 < len(lines) {
		description = strings.TrimRight(strings.Join(lines[i+1:], "\n"), " \t\n")
	}
	return WorkItemCommand{Title: title, Description: description}, true
}

func parseLeadingCommand(body, prefix string) (string, bool) {
	lines := strings.Split(body, "\n")
	i := firstNonEmptyLine(lines)
	if i < 0 {
		return "", false
	}
	trimmed := strings.TrimLeft(lines[i], " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := trimmed[len(prefix):]
	if rest != "" {
		if r0 := rest[0]; r0 != ' ' && r0 != '\t' {
			return "", false
		}
	}
	parts := make([]string, 0, 2)
	if first := strings.TrimSpace(rest); first != "" {
		parts = append(parts, first)
	}
	if i+1 < len(lines) {
		parts = append(parts, strings.Join(lines[i+1:], "\n"))
	}
	return strings.TrimRight(strings.Join(parts, "\n"), " \t\n"), true
}

func firstNonEmptyLine(lines []string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			return i
		}
	}
	return -1
}
