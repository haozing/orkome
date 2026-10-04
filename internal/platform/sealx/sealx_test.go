package sealx

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealRoundtrip(t *testing.T) {
	s, err := New(DeriveKey("jwt-secret-at-least-32-bytes-xxx", "credential"))
	require.NoError(t, err)

	ct, err := s.Encrypt("sk-live-abcdef0123456789")
	require.NoError(t, err)

	assert.False(t, bytes.Contains(ct, []byte("sk-live")))

	pt, err := s.Decrypt(ct)
	require.NoError(t, err)
	assert.Equal(t, "sk-live-abcdef0123456789", pt)
}

func TestSealTamperRejected(t *testing.T) {
	s, _ := New(DeriveKey("jwt-secret-at-least-32-bytes-xxx", "credential"))
	ct, _ := s.Encrypt("secret")
	ct[len(ct)-1] ^= 0x01
	_, err := s.Decrypt(ct)
	assert.Error(t, err)
}

func TestSealWrongKeyRejected(t *testing.T) {
	a, _ := New(DeriveKey("jwt-secret-at-least-32-bytes-xxx", "credential"))
	b, _ := New(DeriveKey("another-secret-at-least-32-bytes", "credential"))
	ct, _ := a.Encrypt("secret")
	_, err := b.Decrypt(ct)
	assert.Error(t, err)
}

func TestNewKeyLength(t *testing.T) {
	_, err := New([]byte("short"))
	assert.Error(t, err)
}
