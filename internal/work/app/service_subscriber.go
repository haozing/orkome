package app

import (
	"context"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

type SubscriberService struct {
	repo Repo
}

func NewSubscriberService(repo Repo) *SubscriberService { return &SubscriberService{repo: repo} }

func AutopilotTemplateSubscribers(userIDs []string) []SubscriberSpec {
	out := make([]SubscriberSpec, 0, len(userIDs))
	for _, id := range userIDs {
		out = append(out, SubscriberSpec{UserID: id, Source: domain.SubSourceAutopilotTemplate})
	}
	return out
}

func initialSubscribersOf(creatorUserID string, in NewWorkItem) ([]SubscriberWrite, error) {
	out := make([]SubscriberWrite, 0, 1+len(in.InitialSubscribers)+1)
	seen := map[string]bool{}
	add := func(uid, source string) {
		if uid == "" || seen[uid] {
			return
		}
		seen[uid] = true
		out = append(out, SubscriberWrite{UserID: uid, Source: source, Level: domain.SubscriberLevel(source)})
	}
	add(creatorUserID, domain.SubSourceCreator)
	for _, s := range in.InitialSubscribers {
		uid, source, err := domain.ValidateSubscriberSpec(s.Source, s.UserID)
		if err != nil {
			return nil, apierr.NewValidation(err.Error(),
				apierr.FieldError{Field: "initial_subscribers", Problem: err.Error()})
		}
		add(uid, source)
	}
	if root, ok := domain.DelegationChainSubscriber(in.Delegation); ok {
		add(root, domain.SubSourceDelegation)
	}
	return out, nil
}

func (s *SubscriberService) Subscribe(ctx context.Context, workItemID string, subs []SubscriberWrite) error {
	for _, sub := range subs {
		if _, _, err := domain.ValidateSubscriberSpec(sub.Source, sub.UserID); err != nil {
			return apierr.NewValidation(err.Error())
		}
	}
	return s.repo.UpsertSubscribers(ctx, workItemID, subs)
}
