package workers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/content/domain"
	skdom "orkome/internal/skills/domain"
)

func TestM8_SkillsImportValidation(t *testing.T) {

	_, ok := skdom.CleanPath("../etc/passwd")
	assert.False(t, ok)
	_, ok = skdom.CleanPath("a/../../b")
	assert.False(t, ok)
	_, ok = skdom.CleanPath("skills/readme.md")
	assert.True(t, ok)

	assert.True(t, skdom.IsBinaryExt("tool.exe"))
	assert.True(t, skdom.IsBinaryExt("IMG.PNG"))
	assert.False(t, skdom.IsBinaryExt("SKILL.md"))

	fm := skdom.ParseFrontmatter("---\nname: my-skill\ndescription: test skill\n---\n# Content")
	assert.Equal(t, "my-skill", fm.Name)
	assert.Equal(t, "test skill", fm.Description)
	fm2 := skdom.ParseFrontmatter("# no heading")
	assert.Equal(t, "", fm2.Name, "missing header non-fatal (internal archive 05 §12)")

	assert.Equal(t, 200, skdom.MaxFilesPerSkill)
	assert.LessOrEqual(t, skdom.MaxFileBytes, skdom.MaxZipBytes)
}

func TestM8_SignedDownload(t *testing.T) {
	s := domain.NewDownloadSigner([]byte("attach-secret-32-bytes-aaaaaaaaaa"))
	attID := "11111111-1111-1111-1111-111111111111"
	member := "m-1"
	now := time.Now()

	url := s.Sign(attID, domain.IntentDownload, member, time.Hour, now)

	exp, intent, sig, m, ok := extractSigParts(url)
	require.True(t, ok)
	assert.Equal(t, domain.IntentDownload, intent)
	assert.Equal(t, member, m)
	assert.True(t, s.Verify(attID, intent, m, exp, sig, now.Add(time.Minute)), "should pass within the window")

	assert.False(t, s.Verify(attID, intent, m, exp, sig, now.Add(2*time.Hour)), "expired should be rejected")

	assert.False(t, s.Verify("22222222-2222-2222-2222-222222222222", intent, m, exp, sig, now),
		"changing ID should be rejected")

	assert.False(t, s.Verify(attID, domain.IntentContent, m, exp, sig, now), "changing intent should be rejected")

	bad := []byte(sig)
	if bad[0] != 'a' {
		bad[0] = 'a'
	} else {
		bad[0] = 'b'
	}
	assert.False(t, s.Verify(attID, intent, m, exp, string(bad), now), "tampered signatures should be rejected")

	s2 := domain.NewDownloadSigner([]byte("other-secret-32-bytes-bbbbbbbbbb"))
	assert.False(t, s2.Verify(attID, intent, m, exp, sig, now))
	_ = hmac.New
	_ = sha256.New
	_ = hex.EncodeToString
}

func extractSigParts(u string) (exp, intent, sig, m string, ok bool) {
	e := queryParam(u, "exp=")
	i := queryParam(u, "intent=")
	g := queryParam(u, "sig=")
	mm := queryParam(u, "m=")
	if e == "" || i == "" || g == "" || mm == "" {
		return "", "", "", "", false
	}
	return e, i, g, mm, true
}

func queryParam(u, key string) string {
	i := indexOf(u, key)
	if i < 0 {
		return ""
	}
	rest := u[i+len(key):]
	if j := indexOf(rest, "&"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
