package domain

import "regexp"

// Pin rules (sidebar pins, internal archive 04 §2.8).
const (
	PinTypeWorkItem    = "work_item"
	PinTypeChatSession = "chat_session"
)

func PinItemTypeOK(t string) bool {
	return t == PinTypeWorkItem || t == PinTypeChatSession
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func UUIDOK(s string) bool { return uuidRe.MatchString(s) }
