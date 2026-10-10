package engine

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
)

const checkpointFingerprint = "eino/1"

type CheckpointStore struct{ DB *pg.DB }

func (s *CheckpointStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	var state []byte
	err := s.DB.Tx(ctx).QueryRow(ctx,
		`SELECT state FROM agent_task_checkpoint WHERE id=$1`, id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	payload, ok := stripFingerprint(state)
	if !ok {
		return nil, false, nil
	}
	return payload, true, nil
}

const zeroUUID = "00000000-0000-0000-0000-000000000000"

func (s *CheckpointStore) Set(ctx context.Context, id string, cp []byte) error {

	_, err := s.DB.Tx(ctx).Exec(ctx, `
INSERT INTO agent_task_checkpoint (id, workspace_id, agent_id, state, updated_at)
VALUES ($1,$2,$3,$4,now())
ON CONFLICT (id) DO UPDATE SET state=EXCLUDED.state, updated_at=now()`,
		id, zeroUUID, zeroUUID, withFingerprint(cp))
	return err
}

func (s *CheckpointStore) Delete(ctx context.Context, id string) error {
	_, err := s.DB.Tx(ctx).Exec(ctx, `DELETE FROM agent_task_checkpoint WHERE id=$1`, id)
	return err
}

func withFingerprint(cp []byte) []byte {
	out := make([]byte, 0, len(checkpointFingerprint)+1+len(cp))
	out = append(out, checkpointFingerprint...)
	out = append(out, '\n')
	return append(out, cp...)
}

func stripFingerprint(state []byte) ([]byte, bool) {
	for i, b := range state {
		if b == '\n' {
			if string(state[:i]) == checkpointFingerprint {
				return state[i+1:], true
			}
			return nil, false
		}
	}
	return nil, false
}
