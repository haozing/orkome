package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	uBob = "00000000-0000-4000-8000-00000000000b"
	uAmy = "00000000-0000-4000-8000-00000000000a"
)

func upper(s string) string { return strings.ToUpper(s) }

func TestDelegationChainSubscriber(t *testing.T) {

	root, ok := DelegationChainSubscriber(DelegationChain{
		OriginType: "agent_create", ChainRootUserID: upper(uBob), ChainRootDirectHuman: true})
	assert.True(t, ok)
	assert.Equal(t, uBob, root)

	for _, c := range []DelegationChain{
		{OriginType: "quick_create", ChainRootUserID: uBob, ChainRootDirectHuman: true},
		{OriginType: "autopilot", ChainRootUserID: uBob, ChainRootDirectHuman: true},
		{OriginType: "agent_create", ChainRootUserID: uBob, ChainRootDirectHuman: false},
		{OriginType: "agent_create", ChainRootUserID: "", ChainRootDirectHuman: true},
		{OriginType: "agent_create", ChainRootUserID: "not-a-uuid", ChainRootDirectHuman: true},
	} {
		_, ok := DelegationChainSubscriber(c)
		assert.False(t, ok, "origin=%s root=%q human=%v", c.OriginType, c.ChainRootUserID, c.ChainRootDirectHuman)
	}
}

func TestSubscriberLevel(t *testing.T) {
	assert.Equal(t, NotifyLevelDelegated, SubscriberLevel(SubSourceDelegation))
	for _, src := range []string{
		SubSourceCreator, SubSourceAssignee, SubSourceCommenter,
		SubSourceMentionee, SubSourceAutopilotTemplate,
	} {
		assert.Equal(t, NotifyLevelAll, SubscriberLevel(src), "source=%s", src)
	}
}

func TestValidateSubscriberSpec(t *testing.T) {
	uid, src, err := ValidateSubscriberSpec(SubSourceAutopilotTemplate, upper(uAmy))
	assert.NoError(t, err)
	assert.Equal(t, uAmy, uid)
	assert.Equal(t, SubSourceAutopilotTemplate, src)

	for _, bad := range []string{SubSourceCreator, SubSourceAssignee, SubSourceCommenter, SubSourceMentionee, SubSourceDelegation, "whatever"} {
		_, _, err := ValidateSubscriberSpec(bad, uAmy)
		assert.Error(t, err, "source=%s", bad)
	}

	for _, bad := range []string{"", "abc", "00000000-0000-4000-8000-00000000000Z", "urn:uuid:" + uAmy, "{12345678-1234-1234-1234-123456789012}"} {
		_, _, err := ValidateSubscriberSpec(SubSourceAutopilotTemplate, bad)
		assert.Error(t, err, "user=%q", bad)
	}
}

func TestParseUUID(t *testing.T) {
	ok, valid := ParseUUID("ABCDEFAB-1234-5678-9ABC-DEF012345678")
	assert.True(t, valid)
	assert.Equal(t, "abcdefab-1234-5678-9abc-def012345678", ok)
	_, valid = ParseUUID("abcdefab123456789abcdef012345678")
	assert.False(t, valid)
}
