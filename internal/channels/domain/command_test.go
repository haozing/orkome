package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseControlCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		kind ControlCommandKind
		body string
	}{
		{"clear", "/clear", ControlCommandFreshSession, ""},
		{"clear with hint", "/clear start over", ControlCommandFreshSession, "start over"},
		{"behavior described after clear", "/clear\nnext: redo it for me", ControlCommandFreshSession, "next: redo it for me"},
		{"new", "/new", ControlCommandNewChat, ""},
		{"new with title", "/new new topic", ControlCommandNewChat, "new topic"},
		{"leading blank lines do not block", "\n\n  /clear", ControlCommandFreshSession, ""},
		{"case-sensitive", "/Clear", ControlCommandNone, ""},
		{"/NEW", "/NEW", ControlCommandNone, ""},
		{"whole token", "/cleared", ControlCommandNone, ""},
		{"/newx", "/newx", ControlCommandNone, ""},
		{"first non-empty line only", "one sentence first\n/clear", ControlCommandNone, ""},
		{"tab-delimited", "/clear\thint", ControlCommandFreshSession, "hint"},
		{"plain text", "check the progress for me", ControlCommandNone, ""},
		{"empty string", "", ControlCommandNone, ""},
		{"whitespace only", " \n\t ", ControlCommandNone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseControlCommand(tc.in)
			assert.Equal(t, tc.kind, got.Kind)
			assert.Equal(t, tc.body, got.Body)
		})
	}
}

func TestParseWorkItemCommand(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		ok    bool
		title string
		desc  string
	}{
		{"title + description", "/task fix login\ndescription: occasional 401\nsteps in the attachment", true, "fix login", "description: occasional 401\nsteps in the attachment"},
		{"title only", "/task fix login", true, "fix login", ""},
		{"no title = usage hint", "/task", true, "", ""},
		{"no title + following lines do not count as a title", "/task\nthis is the description but the title is empty", true, "", "this is the description but the title is empty"},
		{"case-sensitive (uppercase does not trigger)", "/Task fix login", false, "", ""},
		{"whole token", "/tasklist refresh", false, "", ""},
		{"first non-empty line only", "take a look\n/task fix login", false, "", ""},
		{"leading blank lines do not block", "\n /task fix login", true, "fix login", ""},
		{"descriptions containing /task patterns are not re-parsed", "/task A\n/task B", true, "A", "/task B"},
		{"tab-delimited", "/task\tfix login", true, "fix login", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseWorkItemCommand(tc.in)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.title, got.Title)
			assert.Equal(t, tc.desc, got.Description)
		})
	}
}

func TestCommandsMutuallyExclusiveOnFirstLine(t *testing.T) {
	for _, in := range []string{"/clear /new", "/new   /task x", "/task x /clear"} {
		cc := ParseControlCommand(in)
		_, wiOk := ParseWorkItemCommand(in)
		if cc.Kind != ControlCommandNone {
			assert.False(t, wiOk, "control command lines must not also be judged /task: %q", in)
		}
	}

	assert.Equal(t, ControlCommandNewChat, ParseControlCommand("/new /clear").Kind)
}
