package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

const (
	CodeLength     = 6
	CodeTTL        = 10 * time.Minute
	CodeMaxTries   = 5
	ResendCooldown = time.Minute
)

func CodeExpiry(now time.Time) time.Time { return now.Add(CodeTTL) }

func IssueCode() (string, error) {
	b := make([]byte, CodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, CodeLength)
	for i, v := range b {
		out[i] = '0' + v%10
	}
	return string(out), nil
}

func HashCode(code string) string {
	h := sha256.Sum256([]byte("orkome-vcode:" + code))
	return hex.EncodeToString(h[:])
}

func CodeOK(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

const patPrefix = "ork_"

func MintPAT() (token, hash, prefix string, err error) {
	b := make([]byte, 20)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	token = patPrefix + hex.EncodeToString(b)
	return token, HashPAT(token), token[:8], nil
}

func HashPAT(token string) string {
	h := sha256.Sum256([]byte("orkome-pat:" + token))
	return hex.EncodeToString(h[:])
}

func IsPATFormat(token string) bool {
	if !strings.HasPrefix(token, patPrefix) {
		return false
	}
	rest := token[len(patPrefix):]
	if len(rest) != 40 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

func AssignableRole(role string) bool { return role == RoleAdmin || role == RoleMember }

func CanManageMembers(actorRole string) bool { return actorRole == RoleOwner || actorRole == RoleAdmin }

func LastOwnerProtected(targetRole string, targetIsLastOwner bool, action RoleAction) bool {
	if targetRole == RoleOwner && targetIsLastOwner {
		switch action {
		case ActionDemote, ActionRemove:
			return true
		}
	}
	return false
}

type RoleAction int

const (
	ActionDemote RoleAction = iota + 1
	ActionRemove
	ActionPromote
)

func CanLeave(role string, isLastOwner bool) bool {
	return !(role == RoleOwner && isLastOwner)
}

const (
	InviteTTL    = 7 * 24 * time.Hour
	ShareCodeLen = 24
)

func InviteExpiry(now time.Time) time.Time { return now.Add(InviteTTL) }

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func EmailOK(email string) bool {
	return len(email) <= 254 && emailRe.MatchString(strings.ToLower(strings.TrimSpace(email)))
}

func ShareCodeOK(code string) bool {
	if len(code) != ShareCodeLen {
		return false
	}
	_, err := hex.DecodeString(code)
	return err == nil
}

func MintShareCode() (string, error) {
	b := make([]byte, ShareCodeLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func SlugOK(slug string) bool {
	ok, _ := regexp.MatchString(`^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$`, slug)
	return ok
}

var reservedSlugs = map[string]bool{
	"api": true, "www": true, "app": true, "admin": true, "mail": true,
	"static": true, "assets": true, "auth": true, "orkome": true, "support": true,
}

func SlugReserved(slug string) bool { return reservedSlugs[strings.ToLower(slug)] }

func SignupAllowed(email string, allowedEmails, allowedDomains []string) bool {
	if len(allowedEmails) == 0 && len(allowedDomains) == 0 {
		return true
	}
	return EmailWhitelisted(email, allowedEmails, allowedDomains)
}

func EmailWhitelisted(email string, allowedEmails, allowedDomains []string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range allowedEmails {
		if strings.EqualFold(strings.TrimSpace(e), email) {
			return true
		}
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := email[at+1:]
	for _, d := range allowedDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == domain || (strings.HasPrefix(d, ".") && strings.HasSuffix(domain, d)) {
			return true
		}
	}
	return false
}

const (
	PinTypeWorkItem    = "work_item"
	PinTypeChatSession = "chat_session"
)

func PinItemTypeOK(t string) bool {
	return t == PinTypeWorkItem || t == PinTypeChatSession
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func UUIDOK(s string) bool { return uuidRe.MatchString(s) }
