package pgrepo

// 工单产出（"需求→产出"一等公民，迁移 1039）真库门控用例：
// ① 列存在且 NULL 语义；② WriteOutcome 后写覆盖；③ GET 随行读回；
// ④ PATCH outcome 人工覆盖（含空 summary 清除）。Requires TEST_DATABASE_URL.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	workapp "orkome/internal/work/app"
)

func TestPG_WorkItemOutcome(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	_ = db
	ctx := context.Background()
	r := &Repo{DB: db}

	// ① 新工单 outcome 为 NULL
	w, ok, err := r.GetWorkItem(ctx, ws, wi)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Nil(t, w.Outcome, "新工单 outcome 应为 NULL（尚无完成产出）")

	// ② WriteOutcome 写入 + 后写覆盖
	o1 := workapp.Outcome{Summary: "conclusion v1", TaskID: "", CompletedAt: time.Now()}
	require.NoError(t, r.WriteOutcome(ctx, ws, wi, o1))
	got, ok, err := r.GetWorkItem(ctx, ws, wi)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "conclusion v1", got.Outcome.Summary)

	o2 := workapp.Outcome{Summary: "conclusion v2 (latest wins)", TaskID: "", CompletedAt: time.Now()}
	require.NoError(t, r.WriteOutcome(ctx, ws, wi, o2))
	got, _, err = r.GetWorkItem(ctx, ws, wi)
	require.NoError(t, err)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "conclusion v2 (latest wins)", got.Outcome.Summary, "后写覆盖：最终展示最新结论")

	// ③ 不存在的工单报错（结算侧裁决吞错回落评论）
	err = r.WriteOutcome(ctx, ws, "00000000-0000-0000-0000-000000000000", o2)
	require.Error(t, err)

	// ④ PATCH outcome 人工覆盖（经 UpdateWorkItem 同路径）
	patch := workapp.WorkItemPatch{Outcome: &workapp.Outcome{Summary: "human override", TaskID: "", CompletedAt: time.Now()}}
	updated, err := r.UpdateWorkItem(ctx, ws, wi, patch, got.Revision)
	require.NoError(t, err)
	require.NotNil(t, updated.Outcome)
	assert.Equal(t, "human override", updated.Outcome.Summary)

	// ⑤ 人工清除（空 summary 语义由上层封装，这里直写空对象验证 jsonb 往返）
	clear := workapp.Outcome{Summary: "", CompletedAt: time.Now()}
	require.NoError(t, r.WriteOutcome(ctx, ws, wi, clear))
	got, _, err = r.GetWorkItem(ctx, ws, wi)
	require.NoError(t, err)
	require.NotNil(t, got.Outcome)
	assert.Empty(t, got.Outcome.Summary)

	_ = pool
	_ = user
}
