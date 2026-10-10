package app

import (
	"context"

	"orkome/internal/contract/events"
	"github.com/haozing/ploykit/platform/ids"
)

func emitCommittedEnvs(ctx context.Context, emit Emitter, outbox OutboxRecorder, uow Within,
	envs *[]events.Envelope, write func(context.Context) error) error {
	if outbox != nil && uow != nil {
		if err := uow(ctx, func(ctx context.Context) error {
			if err := write(ctx); err != nil {
				return err
			}
			for i := range *envs {
				if (*envs)[i].EventID == "" {

					(*envs)[i].EventID = ids.NewV4().String()
				}
			}
			if len(*envs) == 0 {
				return nil
			}
			return outbox.Append(ctx, *envs...)
		}); err != nil {
			return err
		}
		for _, env := range *envs {
			if emit != nil {
				emit.Emit(ctx, env)
			}
			_ = outbox.MarkPublished(ctx, env.EventID)
		}
		return nil
	}

	if err := write(ctx); err != nil {
		return err
	}
	for _, env := range *envs {
		if emit != nil {
			emit.Emit(ctx, env)
		}
	}
	return nil
}

func emitCommitted(ctx context.Context, emit Emitter, outbox OutboxRecorder, uow Within,
	env *events.Envelope, write func(context.Context) error) error {
	snapshot := make([]events.Envelope, 0, 1)
	err := emitCommittedEnvs(ctx, emit, outbox, uow, &snapshot, func(ctx context.Context) error {
		if err := write(ctx); err != nil {
			return err
		}
		if env != nil {
			snapshot = append(snapshot, *env)
		}
		return nil
	})
	if env != nil && len(snapshot) > 0 {
		*env = snapshot[0]
	}
	return err
}
