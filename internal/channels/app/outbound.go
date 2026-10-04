package app

import (
	"context"
	"errors"
	"log/slog"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
	"orkome/internal/platform/redactx"
)

type OutboundService struct {
	repo    Repo
	senders *SenderRegistry
	guard   OutboundTextGuard
	tasks   TaskOutcomeReader
	logger  *slog.Logger
}

func NewOutboundService(repo Repo, senders *SenderRegistry, guard OutboundTextGuard, tasks TaskOutcomeReader) *OutboundService {
	return &OutboundService{repo: repo, senders: senders, guard: guard, tasks: tasks, logger: slog.Default()}
}

type DeliverResult struct {
	Sent   bool
	Reason string
}

func (s *OutboundService) DeliverTaskReply(ctx context.Context, taskID string) (DeliverResult, error) {
	stamp, ok, err := s.repo.GetTaskDelivery(ctx, taskID)
	if err != nil {
		return DeliverResult{}, err
	}
	if !ok {

		return DeliverResult{Sent: false, Reason: "not_channel_ingested"}, nil
	}

	ch, ok := s.senders.Get(stamp.InstallationID)
	if !ok {

		return DeliverResult{Reason: "channel_not_connected"}, nil
	}
	if !ch.Capabilities().Has(wire.CapText) {

		return DeliverResult{Reason: "cap_text_absent"}, nil
	}

	if s.tasks == nil {
		return DeliverResult{}, ErrTaskOutcomePortMissing //nolint:staticcheck // wiring sentinel
	}
	output, found, err := s.tasks.TaskOutput(ctx, taskID)
	if err != nil {
		return DeliverResult{}, err
	}
	if !found || output == "" {

		return DeliverResult{Reason: "no_output"}, nil
	}

	text := redactx.String(output)
	if s.guard != nil {
		text = s.guard.BreakLinks(text)
	}

	res, err := ch.Send(ctx, wire.OutboundMessage{
		ChatID:   stamp.ExternalChatID,
		Text:     text,
		ThreadID: stamp.ExternalThreadID,
	})
	if err != nil {
		return DeliverResult{}, err
	}
	_ = s.repo.RecordOutboundMessage(ctx, OutboundRecord{
		InstallationID:    stamp.InstallationID,
		Kind:              stamp.Kind,
		ExternalMessageID: res.MessageID,
		BindingID:         stamp.BindingID,
		RouteRevision:     stamp.RouteRevision,
		TaskID:            taskID,
		OutboundKind:      "reply",
	})

	if advanced, _ := s.repo.MarkReplyTerminal(ctx, taskID, res.MessageID); advanced {
		_ = s.repo.FinishCardMessage(ctx, taskID)
	}
	_ = s.repo.SettleReplyDelivery(ctx, taskID, taskID, stamp.BindingID, stamp.InstallationID,
		stamp.Kind, stamp.ExternalChatID, res.MessageID, "delivered")
	return DeliverResult{Sent: true, Reason: "delivered"}, nil
}

func (s *OutboundService) BeginStreamingReply(ctx context.Context, taskID string) (DeliverResult, error) {
	stamp, ok, err := s.repo.GetTaskDelivery(ctx, taskID)
	if err != nil {
		return DeliverResult{}, err
	}
	if !ok {
		return DeliverResult{Reason: "not_channel_ingested"}, nil
	}
	ch, ok := s.senders.Get(stamp.InstallationID)
	if !ok {
		return DeliverResult{Reason: "channel_not_connected"}, nil
	}
	if !ch.Capabilities().Has(wire.CapText | wire.CapStreamingReply) {

		return DeliverResult{Reason: "cap_streaming_absent"}, nil
	}
	opened, err := s.repo.BeginReplyStreaming(ctx, taskID, taskID, stamp.BindingID,
		stamp.InstallationID, stamp.Kind, stamp.ExternalChatID)
	if err != nil {
		return DeliverResult{}, err
	}
	if !opened {

		return DeliverResult{Reason: "phase_not_openable"}, nil
	}
	res, err := ch.Send(ctx, wire.OutboundMessage{
		ChatID:   stamp.ExternalChatID,
		Text:     domain.StreamingPlaceholderText,
		ThreadID: stamp.ExternalThreadID,
	})
	if err != nil {
		return DeliverResult{}, err
	}
	_ = s.repo.RecordReplyPlaceholder(ctx, taskID, res.MessageID)

	if route, ok, err := s.repo.GetRoute(ctx, stamp.InstallationID, stamp.ExternalChatID); err == nil && ok {
		_ = s.repo.InsertCardMessage(ctx, CardMessage{
			ChatSessionID:         route.ChatSessionID,
			TaskID:                taskID,
			Kind:                  stamp.Kind,
			ExternalChatID:        stamp.ExternalChatID,
			ExternalCardMessageID: res.MessageID,
			Status:                domain.CardStatusStreaming,
		})
	}
	return DeliverResult{Sent: true, Reason: "streaming_opened"}, nil
}

var ErrTaskOutcomePortMissing = errors.New("channels: task outcome reader port not wired")
