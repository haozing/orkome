package workers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/domain"
)

func TestB15_UsageHourlyRollup(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b15@example.com")
	ws := e.createWS(t, user, "b15w")

	_, err := e.pool.Exec(ctx0(), `
INSERT INTO task_usage (workspace_id, agent_id, provider, model, prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, created_at)
VALUES
 ($1, NULL, 'fake', 'm', 100, 50, 0, 'primary', date_trunc('hour', now()) - interval '1 hour'),
 ($1, NULL, 'fake', 'm', 20, 10, 0, 'primary', date_trunc('hour', now()) - interval '1 hour' + interval '5 minutes'),
 ($1, NULL, 'fake', 'm', 999, 999, 0, 'primary', date_trunc('hour', now()) + interval '1 minute'),
 ($1, NULL, 'eval', 'rules+llm', 5, 5, 0, 'eval', date_trunc('hour', now()) - interval '1 hour')`, ws)
	require.NoError(t, err)

	boundary := domain.ClosedHourBoundary(time.Now().UTC())
	n, err := e.repo.RollupUsageHourly(ctx0(), boundary)
	require.NoError(t, err)
	assert.Greater(t, n, 0, "closed hour buckets should aggregate")

	var pt, ct int64
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT prompt_tokens, completion_tokens FROM task_usage_hourly
WHERE workspace_id=$1 AND provider='fake' AND agent_key=''`, ws).Scan(&pt, &ct))
	assert.EqualValues(t, 120, pt, "closed two-row aggregation")
	assert.EqualValues(t, 60, ct)

	var evalRuns int64
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT runs FROM task_usage_hourly WHERE workspace_id=$1 AND provider='eval'`, ws).Scan(&evalRuns))
	assert.EqualValues(t, 1, evalRuns)

	var total int64
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT coalesce(sum(runs),0) FROM task_usage_hourly WHERE workspace_id=$1`, ws).Scan(&total))
	assert.EqualValues(t, 3, total, "unclosed hour rows excluded (2+1 buckets, 3 rows total)")

	n2, err := e.repo.RollupUsageHourly(ctx0(), boundary)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "no new rows above the watermark")
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT coalesce(sum(prompt_tokens),0) FROM task_usage_hourly WHERE workspace_id=$1 AND provider='fake'`, ws).Scan(&pt))
	assert.EqualValues(t, 120, pt, "repeated execution does not double count")
}
