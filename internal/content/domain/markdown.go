package domain

import "strings"

func BreakLinkAdjacency(s string) string {
	return strings.ReplaceAll(s, "](", "] (")
}
