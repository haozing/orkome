package workers

// AssistantStalledSweeper 单测：钩子空转/命中路径与周期缺省（v4 §4.3）。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantStalledSweeper_RunOnce_NoHook(t *testing.T) {
	s := &AssistantStalledSweeper{Log: logNoop()}
	n, err := s.RunOnce(ctx0())
	require.NoError(t, err)
	assert.Zero(t, n, "未接线（Stall nil）空转")
}

func TestAssistantStalledSweeper_RunOnce_StallHook(t *testing.T) {
	calls := 0
	s := &AssistantStalledSweeper{
		Stall: func(ctx context.Context) (int, error) {
			calls++
			return 2, nil
		},
		Log: logNoop(),
	}
	n, err := s.RunOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "透传挂起行数")
	assert.Equal(t, 1, calls)

	errHook := &AssistantStalledSweeper{
		Stall: func(ctx context.Context) (int, error) { return 0, errors.New("db down") },
		Log:   logNoop(),
	}
	_, err = errHook.RunOnce(ctx0())
	require.Error(t, err, "错误透传给调用方（worker 层记日志不崩）")
}
