package app

import (
	"context"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
)

type PinService struct{ repo Repo }

func NewPinService(repo Repo) *PinService { return &PinService{repo: repo} }

func (s *PinService) List(ctx context.Context, wsID, userID string) ([]Pin, error) {
	if err := s.requireMember(ctx, wsID, userID); err != nil {
		return nil, err
	}
	return s.repo.ListPins(ctx, wsID, userID)
}

func (s *PinService) Add(ctx context.Context, wsID, userID, itemType, itemID string) (*Pin, bool, error) {
	if err := s.requireMember(ctx, wsID, userID); err != nil {
		return nil, false, err
	}
	if !domain.PinItemTypeOK(itemType) {
		return nil, false, apierr.NewValidation("invalid item_type",
			fieldErr("item_type", "must be work_item|chat_session"))
	}
	if !domain.UUIDOK(itemID) {
		return nil, false, apierr.NewValidation("invalid item_id", fieldErr("item_id", "must be a uuid"))
	}
	pin, created, err := s.repo.AddPin(ctx, wsID, userID, itemType, itemID)
	if err != nil {
		return nil, false, err
	}
	return &pin, created, nil
}

func (s *PinService) Reorder(ctx context.Context, wsID, userID string, order []string) error {
	if err := s.requireMember(ctx, wsID, userID); err != nil {
		return err
	}
	pins, err := s.repo.ListPins(ctx, wsID, userID)
	if err != nil {
		return err
	}
	current := make(map[string]struct{}, len(pins))
	for _, p := range pins {
		current[p.ID] = struct{}{}
	}
	if len(order) != len(current) {
		return apierr.NewValidation("order must list every pinned item exactly once")
	}
	seen := make(map[string]struct{}, len(order))
	for _, id := range order {
		if _, ok := current[id]; !ok {
			return apierr.NewValidation("order must list every pinned item exactly once")
		}
		if _, dup := seen[id]; dup {
			return apierr.NewValidation("order must list every pinned item exactly once")
		}
		seen[id] = struct{}{}
	}
	return s.repo.ReorderPins(ctx, wsID, userID, order)
}

func (s *PinService) Remove(ctx context.Context, wsID, userID, itemType, itemID string) error {
	if err := s.requireMember(ctx, wsID, userID); err != nil {
		return err
	}
	if !domain.PinItemTypeOK(itemType) {
		return apierr.NewValidation("invalid item_type",
			fieldErr("item_type", "must be work_item|chat_session"))
	}
	if !domain.UUIDOK(itemID) {
		return apierr.NewValidation("invalid item_id", fieldErr("item_id", "must be a uuid"))
	}
	return s.repo.RemovePin(ctx, wsID, userID, itemType, itemID)
}

func (s *PinService) requireMember(ctx context.Context, wsID, userID string) error {
	_, ok, err := s.repo.GetMember(ctx, wsID, userID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "workspace not found")
	}
	return nil
}
