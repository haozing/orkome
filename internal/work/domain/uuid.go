package domain

import (
	"regexp"
	"strings"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ParseUUID(s string) (string, bool) {
	if !uuidRe.MatchString(s) {
		return "", false
	}
	return strings.ToLower(s), true
}
