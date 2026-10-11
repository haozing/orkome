package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPinItemTypeOK(t *testing.T) {
	assert.True(t, PinItemTypeOK("work_item"))
	assert.True(t, PinItemTypeOK("chat_session"))
	assert.False(t, PinItemTypeOK("agent"))
	assert.False(t, PinItemTypeOK(""))
}

func TestUUIDOK(t *testing.T) {
	assert.True(t, UUIDOK("11111111-1111-1111-1111-111111111111"))
	assert.False(t, UUIDOK("not-a-uuid"))
	assert.False(t, UUIDOK("111111111111111111111111111111111"))
}
