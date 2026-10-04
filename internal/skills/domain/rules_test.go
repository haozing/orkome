package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mf(path, content string) ManifestFile {
	sum := sha256.Sum256([]byte(content))
	return ManifestFile{Path: path, Content: content, SHA256: hex.EncodeToString(sum[:]), Size: len(content)}
}

func TestManifestDigestDeterministic(t *testing.T) {
	a := mf("SKILL.md", "# main")
	b := mf("docs/guide.md", "guide")
	c := mf("refs/api.md", "api")

	d1 := ManifestDigest([]ManifestFile{a, b, c})
	d2 := ManifestDigest([]ManifestFile{c, a, b})
	assert.Equal(t, d1, d2, "path order normalized: shuffled input same digest")
	assert.NotEqual(t, d1, ManifestDigest([]ManifestFile{a, b}), "file set change → digest change")
	assert.Len(t, d1, 64, "SHA-256 hex")

	a2 := mf("SKILL.md", "# maIn")
	assert.NotEqual(t, ManifestDigest([]ManifestFile{a}), ManifestDigest([]ManifestFile{a2}))
}

func TestManifestDigestLengthPrefixNoAmbiguity(t *testing.T) {

	x := ManifestDigest([]ManifestFile{mf("p", "abc")})
	y := ManifestDigest([]ManifestFile{mf("p1", "abc")})
	assert.NotEqual(t, x, y)

	m1 := ManifestDigest([]ManifestFile{mf("a", "12"), mf("b", "34")})
	m2 := ManifestDigest([]ManifestFile{mf("a", "123"), mf("b", "4")})
	assert.NotEqual(t, m1, m2, "length prefix eliminates cross-file concatenation ambiguity")
}

func TestManifestDigestEmptySet(t *testing.T) {
	assert.Len(t, ManifestDigest(nil), 64)
}

func TestVerifyManifest(t *testing.T) {
	files := []ManifestFile{mf("SKILL.md", "# body"), mf("docs/a.md", "A")}
	require.NoError(t, VerifyManifest(files))

	tampered := append([]ManifestFile(nil), files...)
	tampered[0].Content = "# bOdy"
	require.ErrorIs(t, VerifyManifest(tampered), ErrManifestMismatch)

	sized := append([]ManifestFile(nil), files...)
	sized[1].Size = 99
	require.ErrorIs(t, VerifyManifest(sized), ErrManifestMismatch)

	badSum := append([]ManifestFile(nil), files...)
	badSum[0].SHA256 = "deadbeef"
	require.ErrorIs(t, VerifyManifest(badSum), ErrManifestMismatch)
}
