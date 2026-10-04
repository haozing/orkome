package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

func CredentialFingerprint(kind string, config []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(config)
	return hex.EncodeToString(h.Sum(nil))
}
