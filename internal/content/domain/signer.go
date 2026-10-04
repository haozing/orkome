package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Signer struct{ Secret []byte }

func (s Signer) Sig(id string, exp int64) string {
	mac := hmac.New(sha256.New, s.Secret)
	fmt.Fprintf(mac, "%s|%d", id, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s Signer) Verify(id, expStr, sig string, now time.Time) bool {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	want := s.Sig(id, exp)
	return hmac.Equal([]byte(want), []byte(sig))
}

const (
	IntentDownload = "download"
	IntentContent  = "content"
)

func validIntent(s string) bool { return s == IntentDownload || s == IntentContent }

type DownloadSigner struct {
	Secret []byte

	BindMember bool
}

func NewDownloadSigner(secret []byte) DownloadSigner {
	return DownloadSigner{Secret: secret, BindMember: true}
}

func (d DownloadSigner) message(attachmentID, intent, memberID string, exp int64) string {
	if !d.BindMember {
		memberID = "-"
	}
	return strings.Join([]string{"sdl", attachmentID, intent, strconv.FormatInt(exp, 10), memberID}, "\x00")
}

func (d DownloadSigner) mac(attachmentID, intent, memberID string, exp int64) string {
	m := hmac.New(sha256.New, d.Secret)
	fmt.Fprintf(m, "%s", d.message(attachmentID, intent, memberID, exp))
	return hex.EncodeToString(m.Sum(nil))
}

func (d DownloadSigner) Sign(attachmentID, intent, memberID string, ttl time.Duration, now time.Time) string {
	exp := now.Add(ttl).Unix()
	u := fmt.Sprintf("/attachments/%s/signed-download?exp=%d&intent=%s&sig=%s",
		attachmentID, exp, intent, d.mac(attachmentID, intent, memberID, exp))
	if d.BindMember {
		u += "&m=" + memberID
	}
	return u
}

func (d DownloadSigner) Verify(attachmentID, intent, memberID, expStr, sig string, now time.Time) bool {
	if !validIntent(intent) {
		return false
	}
	if d.BindMember && memberID == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(d.mac(attachmentID, intent, memberID, exp)), []byte(sig))
}
