package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPAT_FormatAndHash(t *testing.T) {
	token, hash, prefix, err := MintPAT()
	require.NoError(t, err)
	assert.True(t, IsPATFormat(token), "minted PATs should pass format validation")
	assert.Len(t, token, 4+40)
	assert.Equal(t, token[:8], prefix)
	assert.Equal(t, HashPAT(token), hash, "hash should be reproducible")
	assert.NotContains(t, hash, "ork_", "hash must not contain plaintext fragments")
	assert.False(t, IsPATFormat("ork_short"), "short token should be rejected")
	assert.False(t, IsPATFormat("xxx_"+token[4:]), "non-ork_ prefix should be rejected")
}

func TestCode_IssueAndHash(t *testing.T) {
	code, err := IssueCode()
	require.NoError(t, err)
	assert.True(t, CodeOK(code))
	assert.Equal(t, HashCode("123456"), HashCode("123456"), "same code same hash")
	assert.NotEqual(t, HashCode("123456"), HashCode("123457"))
	assert.False(t, CodeOK("12345"), "5 rejected")
	assert.False(t, CodeOK("12a456"), "non-numeric rejected")
	exp := CodeExpiry(time.Now())
	assert.True(t, exp.After(time.Now().Add(9*time.Minute)))
}

func TestLastOwner_Protection(t *testing.T) {
	assert.True(t, LastOwnerProtected(RoleOwner, true, ActionDemote), "demoting the sole owner must be rejected")
	assert.True(t, LastOwnerProtected(RoleOwner, true, ActionRemove), "removing the sole owner must be rejected")
	assert.False(t, LastOwnerProtected(RoleOwner, false, ActionDemote), "non-sole owners can be demoted")
	assert.False(t, LastOwnerProtected(RoleAdmin, true, ActionRemove), "admin removal not protected by owner")
	assert.False(t, CanLeave(RoleOwner, true), "the sole owner cannot leave")
	assert.True(t, CanLeave(RoleOwner, false))
}

func TestSignupAllowlist(t *testing.T) {
	assert.True(t, SignupAllowed("a@b.com", nil, nil), "empty allowlist = open signup")
	assert.True(t, SignupAllowed("a@corp.com", nil, []string{"corp.com"}))
	assert.True(t, SignupAllowed("x@mail.corp.com", nil, []string{".corp.com"}), "subdomain match")
	assert.True(t, SignupAllowed("a@b.com", []string{"a@b.com"}, nil))
	assert.False(t, SignupAllowed("c@other.com", nil, []string{"corp.com", "d.com"}))
	assert.False(t, SignupAllowed("a@corp.com.evil.com", []string{"a@corp.com"}, []string{"corp.com"}), "suffix spoofing rejected")
}

func TestEmailWhitelisted(t *testing.T) {
	assert.False(t, EmailWhitelisted("a@b.com", nil, nil), "empty allowlist ≠ hit — closing signup closes all")
	assert.True(t, EmailWhitelisted("a@corp.com", nil, []string{"corp.com"}))
	assert.True(t, EmailWhitelisted("a@b.com", []string{"A@B.com"}, nil), "email items case-insensitive")
	assert.True(t, EmailWhitelisted("x@mail.corp.com", nil, []string{".corp.com"}), "subdomain match")
	assert.False(t, EmailWhitelisted("c@other.com", []string{"a@b.com"}, []string{"corp.com"}))
}

func TestSlugAndShareCode(t *testing.T) {
	assert.True(t, SlugOK("my-team"))
	assert.False(t, SlugOK("My-Team"), "uppercase rejected")
	assert.False(t, SlugOK("ab"), "too short")
	assert.True(t, SlugReserved("API"), "kept field case-insensitive")
	code, err := MintShareCode()
	require.NoError(t, err)
	assert.True(t, ShareCodeOK(code))
	assert.False(t, ShareCodeOK("short"))
}
