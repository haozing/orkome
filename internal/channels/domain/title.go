package domain

import (
	"regexp"
	"strings"
	"unicode"
)

const TitleLimit = 30

var (
	titleMarkdownFence = regexp.MustCompile("```.*?```")
	titleMarkdownMarks = regexp.MustCompile("[#*`>~_]")
	titleMarkdownLink  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	titleSpace         = regexp.MustCompile(`[[:space:]]+`)
)

func DeriveTitle(body string) string {
	line := ""
	for _, candidate := range strings.Split(body, "\n") {
		if strings.TrimSpace(candidate) != "" {
			line = candidate
			break
		}
	}
	line = titleMarkdownFence.ReplaceAllString(line, " ")
	line = titleMarkdownMarks.ReplaceAllString(line, "")
	line = titleMarkdownLink.ReplaceAllString(line, "$1")
	line = strings.TrimSpace(titleSpace.ReplaceAllString(line, " "))
	if line == "" {
		return ""
	}
	runes := []rune(line)
	if len(runes) <= TitleLimit {
		return line
	}
	return strings.TrimRightFunc(string(runes[:TitleLimit-1]), unicode.IsSpace) + "…"
}

const (
	TitleSourceDerived  = "derived"
	TitleSourceLLM      = "llm"
	TitleSourceExplicit = "explicit"
)

const TitleCallbackMaxRunes = 200

func TitleCallbackNormalize(title string) string {
	title = strings.ReplaceAll(title, "\x00", "")
	title = strings.TrimSpace(titleSpace.ReplaceAllString(title, " "))
	if title == "" {
		return ""
	}
	runes := []rune(title)
	if len(runes) <= TitleCallbackMaxRunes {
		return title
	}
	return strings.TrimRightFunc(string(runes[:TitleCallbackMaxRunes-1]), unicode.IsSpace) + "…"
}

func TitleCallbackMayReplace(currentSource string) bool {
	return currentSource != TitleSourceExplicit
}
