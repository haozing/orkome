package app

import (
	"context"
	"time"
)

// Clock is the injectable time source shared by the product services.
type Clock func() time.Time

// PinRepo is the persistence port for the product-owned pin extension:
// sidebar pins plus the membership read that gates them. It reads the
// framework-owned member table; it never writes identity state.
type PinRepo interface {
	GetMember(ctx context.Context, workspaceID, userID string) (Member, bool, error)
	ListPins(ctx context.Context, workspaceID, userID string) ([]Pin, error)
	AddPin(ctx context.Context, workspaceID, userID, itemType, itemID string) (pin Pin, created bool, err error)
	ReorderPins(ctx context.Context, workspaceID, userID string, order []string) error
	RemovePin(ctx context.Context, workspaceID, userID, itemType, itemID string) error
}
