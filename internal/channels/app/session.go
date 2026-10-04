package app

import (
	"context"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

type ChannelSession struct {
	repo  Repo
	convo ConversationRouter
}

func NewChannelSession(repo Repo, convo ConversationRouter) *ChannelSession {
	return &ChannelSession{repo: repo, convo: convo}
}

func (s *ChannelSession) Ensure(ctx context.Context, inst Installation, msg wire.InboundMessage, creatorUserID string) (Route, error) {
	route, ok, err := s.repo.GetRoute(ctx, inst.ID, msg.Source.ChatID)
	if err != nil {
		return Route{}, err
	}
	if ok {
		return route, nil
	}
	title := domain.DeriveTitle(msg.CommandText)
	if title == "" {
		title = "Channel session"
	}
	sessionID, err := s.convo.EnsureChannelSession(ctx, EnsureSessionParams{
		WorkspaceID:    inst.WorkspaceID,
		AgentID:        inst.AgentID,
		CreatorUserID:  creatorUserID,
		Title:          title,
		ChatType:       msg.Source.ChatType,
		ExternalChatID: msg.Source.ChatID,
	})
	if err != nil {
		return Route{}, err
	}
	route, err = s.repo.InsertRoute(ctx, Route{
		InstallationID:  inst.ID,
		WorkspaceID:     inst.WorkspaceID,
		ExternalChatID:  msg.Source.ChatID,
		ChatType:        msg.Source.ChatType,
		ChatSessionID:   sessionID,
		Generation:      1,
		ContextRevision: 1,
	})
	if err != nil {
		if err == ErrRouteConflict { //nolint:errorlint // sentinel direct check (pgrepo semantics)

			route, ok, err = s.repo.GetRoute(ctx, inst.ID, msg.Source.ChatID)
			if err != nil {
				return Route{}, err
			}
			if ok {
				return route, nil
			}
		}
		return Route{}, err
	}
	return route, nil
}

func (s *ChannelSession) Rotate(ctx context.Context, inst Installation, msg wire.InboundMessage, creatorUserID, firstPrompt string) (Route, error) {
	title := domain.DeriveTitle(firstPrompt)
	if title == "" {
		title = "Channel session"
	}
	sessionID, err := s.convo.EnsureChannelSession(ctx, EnsureSessionParams{
		WorkspaceID:    inst.WorkspaceID,
		AgentID:        inst.AgentID,
		CreatorUserID:  creatorUserID,
		Title:          title,
		ChatType:       msg.Source.ChatType,
		ExternalChatID: msg.Source.ChatID,
	})
	if err != nil {
		return Route{}, err
	}
	return s.repo.RotateRoute(ctx, inst.ID, msg.Source.ChatID, sessionID)
}

func (s *ChannelSession) NextContextGeneration(ctx context.Context, chatSessionID, initiatorUserID string) (int64, error) {
	return s.repo.NextGeneration(ctx, chatSessionID, initiatorUserID)
}

func (s *ChannelSession) Append(ctx context.Context, route Route, msg wire.InboundMessage, userID, installationID, claimToken string, mediaPending bool) (AppendResult, error) {
	kind := "message"
	if domain.ParseControlCommand(msg.CommandText).Kind != domain.ControlCommandNone {
		kind = "channel_command"
	} else if _, ok := domain.ParseWorkItemCommand(msg.CommandText); ok {
		kind = "channel_command"
	}
	return s.convo.AppendChannelMessage(ctx, AppendParams{
		SessionID:         route.ChatSessionID,
		UserID:            userID,
		Content:           msg.Text,
		Kind:              kind,
		ContextRevision:   route.ContextRevision,
		MediaPending:      mediaPending,
		ExternalMessageID: msg.MessageID,
	}, func(ctx context.Context) error {
		if msg.MessageID == "" {
			return nil
		}
		return s.repo.MarkInboundProcessed(ctx, installationID, msg.MessageID, claimToken)
	})
}
