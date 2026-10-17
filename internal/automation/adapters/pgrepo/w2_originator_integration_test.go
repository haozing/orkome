package pgrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/migrations"
)

// W2-2 回归（真 PG 门控）：1037 迁移 up/down 往返。
//
// up 做两件事：① 固化 agent_task_queue.originator_user_id 可空（1003:89 基线本就
// 可空，条件 DROP NOT NULL 为防御性 no-op）；② 清洗零 UUID 哨兵行（FK 之下历史上
// 从未成功落库，防御性幂等 UPDATE）。
// down 策略（写在迁移文件里，这里锁行为）：NULL 行 = 系统发起任务、无法归属
// （零 UUID 撞 originator FK），存在时 down 必须报错中止要求先归属；down 不做
// SET NOT NULL——基线从未有过该约束，收紧属过度回滚。
func TestMigration1037_OriginatorNullableRoundTrip_DB(t *testing.T) {
	f := newQuotaFixture(t) // 夹具已跑全量 up（含 1037）

	nullableOf := func() bool {
		t.Helper()
		var nullable bool
		require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT NOT a.attnotnull FROM pg_attribute a
WHERE a.attrelid = 'agent_task_queue'::regclass AND a.attname = 'originator_user_id'`).
			Scan(&nullable))
		return nullable
	}

	// up 后：列可空，零 UUID 哨兵清零。
	require.True(t, nullableOf(), "originator_user_id must be nullable after 1037 up")
	var zeros int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM agent_task_queue WHERE originator_user_id = '00000000-0000-0000-0000-000000000000'::uuid`).
		Scan(&zeros))
	require.Zero(t, zeros, "zero-UUID sentinel rows must not survive 1037 up")

	m := pgmigrate.New(f.pool, migrations.FS, ".")

	// down 守卫：存在 NULL originator 行（系统发起）时必须拒绝回滚。
	var agentID string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'w2-agent') RETURNING id::text`, f.wsID).
		Scan(&agentID))
	_, err := f.pool.Exec(f.ctx,
		`INSERT INTO agent_task_queue (workspace_id, agent_id, status) VALUES ($1,$2,'queued')`,
		f.wsID, agentID)
	require.NoError(t, err, "seed a NULL-originator (system-initiated) task")
	// ploykit 0.7.0 起 Down(ctx, limit) 语义=回滚最近 N 个（不再是 goose 式
	// 目标版本）：从 1047 头到达 1037 的 down 需先滚 1047..1038 十个，第 11
	// 步才触守卫。limit 给足 11，中途在 1037 报错返回（applied 停在 1038）。
	_, err = m.Down(f.ctx, 11)
	require.Error(t, err, "down must refuse while unattributable NULL-originator rows exist")
	require.Contains(t, err.Error(), "originator_user_id")

	// 清掉系统任务行后 down 放行；down 不收紧约束（基线即可空）。
	_, err = f.pool.Exec(f.ctx, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agentID)
	require.NoError(t, err)
	rolled, err := m.Down(f.ctx, 1)
	require.NoError(t, err)
	require.NotEmpty(t, rolled)
	require.True(t, nullableOf(),
		"down must restore the 1003 baseline (column nullable), not invent NOT NULL")

	// 再 up：幂等回到可空形态。
	_, err = m.Up(f.ctx, 0)
	require.NoError(t, err)
	require.True(t, nullableOf(), "re-up must restore the nullable design")
}
