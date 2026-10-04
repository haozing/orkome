package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	RunPending   = "pending"
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunSkipped   = "skipped"
)

const (
	TriggerSchedule = "schedule"
	TriggerWebhook  = "webhook"
	TriggerManual   = "manual"
)

const (
	WakeupTime      = "time"
	WakeupEvent     = "event"
	WakeupCondition = "condition"
	WakeupSystem    = "system"
)

const ReasonQuotaExceeded = "quota_exceeded"

const (
	DispatchBudget      = 2 * time.Second
	DispatchLockTimeout = 50 * time.Millisecond
)

const WebhookReplayWindow = 5 * time.Minute

const (
	WebhookMaxAttempts = 5

	WebhookBackoffCap = 64 * time.Second
)

func WebhookBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	d := time.Duration(1<<uint(attempts)) * time.Second
	if d > WebhookBackoffCap {
		d = WebhookBackoffCap
	}
	return d
}

const (
	AutopilotFailureWindow = 7 * 24 * time.Hour

	AutopilotFailureMinRuns = 50

	AutopilotFailureRate = 0.9

	AutopilotPausedFailureRate = "failure_rate"
)

func FailureFuseTripped(runs, failed int) bool {
	if runs < AutopilotFailureMinRuns || failed > runs {
		return false
	}
	return float64(failed)/float64(runs) >= AutopilotFailureRate
}

var (
	ErrQuotaExceeded = errors.New("quota exceeded")

	ErrLockTimeout = errors.New("dispatch lock timeout")

	ErrWebhookTokenCollision = errors.New("webhook token collision")
)

func PlanLate(planned, now time.Time) bool {
	return now.Sub(planned) > 5*time.Minute
}

func IntervalNext(after time.Time, seconds int) time.Time {
	if seconds <= 0 {
		seconds = 3600
	}
	return after.Add(time.Duration(seconds) * time.Second)
}

type FieldPredicate struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type EvalFacts struct {
	Status    string
	Priority  int
	BatchDone bool
}

func (p FieldPredicate) Eval(f EvalFacts) bool {
	var actual any
	switch p.Field {
	case "status":
		actual = f.Status
	case "priority":
		actual = f.Priority
	case "batch":
		actual = map[bool]string{true: "done", false: "pending"}[f.BatchDone]
	default:
		return false
	}
	switch p.Op {
	case "eq":
		return fmt.Sprint(actual) == fmt.Sprint(p.Value)
	case "ne":
		return fmt.Sprint(actual) != fmt.Sprint(p.Value)
	}
	return false
}

func Interpolate(tpl string, now time.Time) string {
	return strings.ReplaceAll(tpl, "{{date}}", now.UTC().Format("2006-01-02"))
}

func MintWebhookToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func MintSigningSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ValidateSigningSecret(s string) error {
	if s == "" {
		return errors.New("signing secret required")
	}
	if n := len(s); n < 32 || n > 128 {
		return fmt.Errorf("signing secret must be 32..128 bytes, got %d", n)
	}
	return nil
}

func SecretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:8]
}

func QuotaWindow(now time.Time) (start, end time.Time) {
	start = now.UTC().Truncate(24 * time.Hour)
	return start, start.Add(24 * time.Hour)
}
