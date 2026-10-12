package pgrepo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
)

// 回归（batch1 根因 D / FP-18 D-07）：TriggerService.Preview/OnComment 的两条
// repo 数据路径——ListSquadFacts 与 HistoryAgentIDs——曾双双把评论/预览打爆成 500：
//
//   - ListSquadFacts：可空 squad.leader_agent_id::text 直接 Scan 进非空 string（NULL
//     扫描报错）；且成员列 array_agg 产出 text[]（OID 1009）在 pgx 二进制协议下扫不进
//     []byte——只要工作区有 ≥1 个小队即炸（第二层断点，repo 层已改 jsonb_agg 修复）。
//   - HistoryAgentIDs：顶层 POST /api/work-items/preview-trigger 无 workItemId，
//     空串进 $1::uuid → 22P02。
//
// fake repo 不经 pgx 扫描，NULL/数组 OID 在 fake 层不可见——这正是既有 45+ 测试
// 全绿的漏网原因，故本组用例门控在真 PG（TEST_DATABASE_URL）上运行。
// 两个 DB 子测试共享一个 summaryEnv 夹具（每起一次夹具要全量跑迁移 ~100s，
// 本包在 TEST_DATABASE_URL 下已逼近 go test 默认 600s 包级超时，勿再逐用例起夹具）。
func TestTriggerFactsP0Regression_DB(t *testing.T) {
	db, pool, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()

	t.Run("ListSquadFacts_LeaderlessSquadScansAsEmpty", func(t *testing.T) {
		var leaderAgent, leaderless, withLeader string
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO agent (workspace_id, name) VALUES ($1,'lead-agent') RETURNING id::text`,
			ws).Scan(&leaderAgent))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO squad (workspace_id, name) VALUES ($1,'squad-noleader') RETURNING id::text`,
			ws).Scan(&leaderless))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO squad (workspace_id, name, leader_agent_id) VALUES ($1,'squad-leader',$2) RETURNING id::text`,
			ws, leaderAgent).Scan(&withLeader))
		_, err := pool.Exec(ctx, `INSERT INTO squad_member (squad_id, agent_id) VALUES ($1,$2)`,
			withLeader, leaderAgent)
		require.NoError(t, err)

		facts, err := r.ListSquadFacts(ctx, ws)
		require.NoError(t, err, "NULL leader must scan as empty string, not error (root cause D)")
		require.Len(t, facts, 2)

		byID := map[string]domain.SquadFacts{}
		for _, sq := range facts {
			byID[sq.ID] = sq
		}
		require.Empty(t, byID[leaderless].LeaderID, "leaderless squad → empty LeaderID")
		require.Equal(t, leaderAgent, byID[withLeader].LeaderID)
		require.Equal(t, []string{leaderAgent}, byID[withLeader].MemberIDs,
			"members must parse as JSON array (jsonb_agg); the old PG array literal left MemberIDs always empty")
	})

	t.Run("HistoryAgentIDs_DistinctAgentsForWorkItem", func(t *testing.T) {
		wi := seedWorkItemRow(t, pool, ws)
		ag2, err := r.CreateAgent(ctx, orchapp.Agent{WorkspaceID: ws, Name: "second-agent",
			RuntimeConfig: map[string]any{}, MaxConcurrent: 1, Visibility: "public"})
		require.NoError(t, err)

		// uq_one_pending_task_per_work_item_agent：同一 (wi,agent) 只能有一条 pending，
		// 第一条置 completed 覆盖"历史含已完成 run"。
		for i, agentID := range []string{ag, ag2.ID} {
			task, err := r.InsertTask(ctx, orchapp.Task{WorkspaceID: ws, AgentID: agentID,
				WorkItemID: wi, Priority: 1, MaxAttempts: 1, Context: map[string]any{}})
			require.NoError(t, err)
			if i == 0 {
				_, err = r.q(ctx).Exec(ctx,
					`UPDATE agent_task_queue SET status='completed' WHERE id=$1`, task.ID)
				require.NoError(t, err)
			}
		}

		ids, err := r.HistoryAgentIDs(ctx, wi)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{ag, ag2.ID}, ids, "DISTINCT agent_id per work item (completed + pending)")
	})
}

// 回归（FP-18 D-07 因 A）：空 workItemId 必须在触库前短路返回空历史。
// 守卫在 DB 之前，所以本用例不需要真库、恒跑（无 TEST_DATABASE_URL 也覆盖）。
func TestHistoryAgentIDs_EmptyWorkItemIDShortCircuits(t *testing.T) {
	r := &Repo{}
	ids, err := r.HistoryAgentIDs(context.Background(), "")
	require.NoError(t, err, "empty workItemId must short-circuit, not reach $1::uuid (22P02)")
	require.Empty(t, ids)
}
