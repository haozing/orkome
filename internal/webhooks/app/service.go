package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
)

type Subscription struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	URL         string    `json:"url"`
	Events      []string  `json:"events"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Secret      string    `json:"-"`
}

type CreateResult struct {
	Subscription Subscription `json:"subscription"`
	Secret       string       `json:"secret"`
}

type Delivery struct {
	ID                 string     `json:"id"`
	SubscriptionID     string     `json:"subscription_id"`
	EventID            string     `json:"event_id"`
	EventType          string     `json:"event_type"`
	Payload            string     `json:"payload,omitempty"`
	Status             string     `json:"status"`
	Attempts           int        `json:"attempts"`
	NextAttemptAt      time.Time  `json:"next_attempt_at"`
	LastResponseStatus *int       `json:"last_response_status,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	DeliveredAt        *time.Time `json:"delivered_at,omitempty"`
}

type CatalogEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	WorkspaceID string          `json:"workspace_id"`
	Data        json.RawMessage `json:"data,omitempty"`
}

type Repo interface {
	CreateSubscription(ctx context.Context, ws, url string, evs []string, secret, description, createdBy string) (Subscription, error)
	ListSubscriptions(ctx context.Context, ws string) ([]Subscription, error)
	GetSubscription(ctx context.Context, id string) (Subscription, bool, error)
	RevokeSubscription(ctx context.Context, id string) error
	ListActiveSubscriptions(ctx context.Context, ws string) ([]Subscription, error)
	InsertDelivery(ctx context.Context, subID string, ev CatalogEvent) error
	ListDeliveries(ctx context.Context, subID, status string, limit int) ([]Delivery, error)
	ClaimPendingDeliveries(ctx context.Context, batch int) ([]Delivery, error)
	MarkDelivered(ctx context.Context, id string, httpStatus int) error
	MarkFailed(ctx context.Context, id string, httpStatus int, errMsg string, nextAttempt time.Time) error
	MarkDead(ctx context.Context, id string, httpStatus int, errMsg string) error
	ReplayDelivery(ctx context.Context, id string) error

	ListEvents(ctx context.Context, ws string, afterCreatedAt time.Time, afterID string, limit int) ([]CatalogEvent, error)
}

type EgressGuard interface {
	ValidateURL(ctx context.Context, raw string) error
}

type Service struct {
	repo   Repo
	egress EgressGuard
	now    func() time.Time
}

func New(repo Repo, egress EgressGuard) *Service {
	return &Service{repo: repo, egress: egress, now: time.Now}
}

var catalogTypes = map[string]bool{
	events.TaskCompleted:   true,
	events.TaskFailed:      true,
	events.WorkItemCreated: true,
	events.WorkItemUpdated: true,
	events.CommentCreated:  true,
}

func (s *Service) Create(ctx context.Context, ws, url string, evs []string, description, createdBy string) (*CreateResult, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, apierr.NewValidation("url required")
	}
	if s.egress != nil {
		if err := s.egress.ValidateURL(ctx, url); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	for _, e := range evs {
		if !catalogTypes[e] {
			return nil, apierr.NewValidation("unknown event type: " + e)
		}
	}
	secret, err := mintSecret()
	if err != nil {
		return nil, err
	}
	sub, err := s.repo.CreateSubscription(ctx, ws, url, evs, secret, description, createdBy)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Subscription: sub, Secret: secret}, nil
}

func mintSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "webk_" + hex.EncodeToString(b), nil
}

func (s *Service) List(ctx context.Context, ws string) ([]Subscription, error) {
	return s.repo.ListSubscriptions(ctx, ws)
}

func (s *Service) Revoke(ctx context.Context, ws, id string) error {
	sub, ok, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if !ok || sub.WorkspaceID != ws {
		return apierr.New(apierr.NotFound, "subscription not found")
	}
	return s.repo.RevokeSubscription(ctx, id)
}

func (s *Service) HandleEvent(ctx context.Context, e events.Envelope) {
	if e.WorkspaceID == "" || e.EventID == "" || !catalogTypes[e.Event] {
		return
	}
	subs, err := s.repo.ListActiveSubscriptions(ctx, e.WorkspaceID)
	if err != nil || len(subs) == 0 {
		return
	}
	data, _ := json.Marshal(e.Payload)
	ev := CatalogEvent{ID: e.EventID, Type: e.Event, OccurredAt: e.OccurredAt, WorkspaceID: e.WorkspaceID, Data: data}
	for _, sub := range subs {
		if len(sub.Events) > 0 && !containsType(sub.Events, e.Event) {
			continue
		}
		_ = s.repo.InsertDelivery(ctx, sub.ID, ev)
	}
}

func containsType(evs []string, t string) bool {
	for _, e := range evs {
		if e == t {
			return true
		}
	}
	return false
}

func (s *Service) Deliveries(ctx context.Context, ws, subID, status string, limit int) ([]Delivery, error) {
	sub, ok, err := s.repo.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if !ok || sub.WorkspaceID != ws {
		return nil, apierr.New(apierr.NotFound, "subscription not found")
	}
	return s.repo.ListDeliveries(ctx, subID, status, limit)
}

func (s *Service) Replay(ctx context.Context, ws, subID, deliveryID string) error {
	if _, err := s.Deliveries(ctx, ws, subID, "", 1); err != nil {
		return err
	}
	return s.repo.ReplayDelivery(ctx, deliveryID)
}

func (s *Service) Test(ctx context.Context, ws, subID string) error {
	sub, ok, err := s.repo.GetSubscription(ctx, subID)
	if err != nil || !ok || sub.WorkspaceID != ws {
		return apierr.New(apierr.NotFound, "subscription not found")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	ev := CatalogEvent{
		ID: "ping_" + hex.EncodeToString(b), Type: "ping",
		OccurredAt: s.now().UTC(), WorkspaceID: ws,
		Data: json.RawMessage(`{"ping":true}`),
	}
	return s.repo.InsertDelivery(ctx, subID, ev)
}

func (s *Service) Events(ctx context.Context, ws string, afterAt time.Time, afterID string, limit int) ([]CatalogEvent, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out, err := s.repo.ListEvents(ctx, ws, afterAt, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = fmt.Sprintf("%d|%s", last.OccurredAt.UnixNano(), last.ID)
	}
	return out, next, nil
}

func (s *Service) Deliver(ctx context.Context, d Delivery, sub Subscription) error {
	ts := s.now().UTC().Format(time.RFC3339Nano)
	body, _ := json.Marshal(d.payloadOrPing())
	mac := hmac.New(sha256.New, []byte(sub.Secret))
	mac.Write([]byte(ts + "." + string(body)))
	sig := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, strings.NewReader(string(body)))
	if err != nil {
		return s.repo.MarkDead(ctx, d.ID, 0, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", "sha256="+sig)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Delivery-Id", d.ID)
	req.Header.Set("X-Event-Id", d.EventID)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return s.fail(ctx, d, 0, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return s.repo.MarkDelivered(ctx, d.ID, resp.StatusCode)
	}
	return s.fail(ctx, d, resp.StatusCode, fmt.Sprintf("http %d", resp.StatusCode))
}

func (d Delivery) payloadOrPing() any {
	return json.RawMessage(d.Payload)
}

func (s *Service) ClaimBatch(ctx context.Context, batch int) ([]Delivery, error) {
	return s.repo.ClaimPendingDeliveries(ctx, batch)
}

func (s *Service) GetSubscriptionForDelivery(ctx context.Context, id string) (Subscription, bool, error) {
	return s.repo.GetSubscription(ctx, id)
}

func (s *Service) DeadUndeliverable(ctx context.Context, deliveryID string) error {
	return s.repo.MarkDead(ctx, deliveryID, 0, "subscription gone")
}

var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

func MaxAttempts() int { return len(backoff) }

func (s *Service) fail(ctx context.Context, d Delivery, httpStatus int, errMsg string) error {
	if d.Attempts >= len(backoff) {
		return s.repo.MarkDead(ctx, d.ID, httpStatus, errMsg)
	}
	return s.repo.MarkFailed(ctx, d.ID, httpStatus, errMsg, s.now().UTC().Add(backoff[d.Attempts]))
}
