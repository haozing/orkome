package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orkome/internal/automation/domain"
	"orkome/internal/contract/apierr"
)

type WebhookService struct {
	repo Repo
	auto *AutopilotService
	now  func() time.Time
}

func NewWebhookService(repo Repo, auto *AutopilotService) *WebhookService {
	return &WebhookService{repo: repo, auto: auto, now: time.Now}
}

func (s *WebhookService) Ingest(ctx context.Context, token, signature, timestamp string, body []byte) (int, error) {
	t, ok, err := s.repo.GetTriggerByToken(ctx, token)
	if err != nil || !ok || !t.Enabled {
		return 404, errors.New("webhook not found")
	}

	if t.SigningSecret == "" {
		return 403, errors.New("signing secret not configured")
	}

	var ts int64
	fmt.Sscanf(timestamp, "%d", &ts)
	if ts == 0 || abs64(s.now().Unix()-ts) > int64(domain.WebhookReplayWindow.Seconds()) {
		return 403, errors.New("timestamp outside replay window")
	}
	mac := hmac.New(sha256.New, []byte(t.SigningSecret))
	mac.Write([]byte(timestamp + "." + string(body)))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return 403, errors.New("bad signature")
	}

	sum := sha256.Sum256(body)
	key := t.ID + ":" + hex.EncodeToString(sum[:8])
	ws := t.Workspace()
	_, dup, err := s.repo.InsertDelivery(ctx, ws, t.ID, key, body)
	if err != nil {
		return 500, err
	}
	if dup {
		return 202, nil
	}
	return 202, nil
}

type TriggerCredential struct {
	TriggerID         string `json:"trigger_id"`
	HasSigningSecret  bool   `json:"has_signing_secret"`
	SecretFingerprint string `json:"secret_fingerprint,omitempty"`
	WebhookToken      string `json:"webhook_token,omitempty"`
}

func (s *WebhookService) SetSigningSecret(ctx context.Context, workspaceID, autopilotID, triggerID, secret string) (*TriggerCredential, error) {
	secret = strings.TrimSpace(secret)
	if err := domain.ValidateSigningSecret(secret); err != nil {
		return nil, apierr.NewValidation(err.Error(), apierr.FieldError{Field: "secret", Problem: "length must be 32..128 bytes"})
	}
	ok, err := s.repo.SetTriggerSigningSecret(ctx, workspaceID, autopilotID, triggerID, secret)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "trigger not found")
	}
	return &TriggerCredential{TriggerID: triggerID, HasSigningSecret: true,
		SecretFingerprint: domain.SecretFingerprint(secret)}, nil
}

func (s *WebhookService) RotateWebhookToken(ctx context.Context, workspaceID, autopilotID, triggerID string) (*TriggerCredential, error) {
	for attempt := 0; attempt < 3; attempt++ {
		tok, err := domain.MintWebhookToken()
		if err != nil {
			return nil, err
		}
		ok, err := s.repo.SetTriggerWebhookToken(ctx, workspaceID, autopilotID, triggerID, tok)
		if err == nil {
			if !ok {
				return nil, apierr.New(apierr.NotFound, "trigger not found")
			}
			return &TriggerCredential{TriggerID: triggerID, WebhookToken: tok}, nil
		}
		if !errors.Is(err, domain.ErrWebhookTokenCollision) {
			return nil, err
		}
	}
	return nil, errors.New("rotate webhook token: exhausted attempts")
}

func (s *WebhookService) ConsumeOnce(ctx context.Context) (bool, error) {
	id, triggerID, _, payload, attempts, ok, err := s.repo.ClaimDelivery(ctx)
	if err != nil || !ok {
		return false, err
	}
	t, ok2, err := s.repo.GetTriggerByID(ctx, triggerID)
	if err != nil || !ok2 {

		_ = s.repo.FailDelivery(ctx, id, true, nil, "trigger gone")
		return true, nil
	}
	if _, err := s.auto.StartRun(ctx, t.AutopilotID, t.ID, "webhook"); err != nil {

		if errors.Is(err, domain.ErrQuotaExceeded) {
			_ = s.repo.FinishDelivery(ctx, id, "skipped", err.Error())
			return true, nil
		}

		if attempts >= domain.WebhookMaxAttempts {
			_ = s.repo.FailDelivery(ctx, id, true, nil, err.Error())
		} else {
			next := s.now().Add(domain.WebhookBackoff(attempts))
			_ = s.repo.FailDelivery(ctx, id, false, &next, err.Error())
		}
		return true, nil
	}
	_ = s.repo.FinishDelivery(ctx, id, "delivered", "")
	_ = payload
	return true, nil
}

func (s *WebhookService) ListDeliveries(ctx context.Context, ws string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out, err := s.repo.ListDeliveries(ctx, ws, limit)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Delivery{}
	}
	return out, nil
}

func (s *WebhookService) GetDelivery(ctx context.Context, ws, id string) (*Delivery, error) {
	d, ok, err := s.repo.GetDelivery(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "delivery not found")
	}
	return &d, nil
}

func (s *WebhookService) ReplayDelivery(ctx context.Context, ws, id string) (*Delivery, error) {
	ok, err := s.repo.ReplayDelivery(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "delivery not found")
	}
	return s.GetDelivery(ctx, ws, id)
}

func strOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func abs64(i int64) int64 {
	if i < 0 {
		return -i
	}
	return i
}
