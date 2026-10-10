package webx

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolStats 返回连接池的（借出, 空闲, 总数）计数，供健康检查与 /readyz 展示。
// 连接级超时保护不在此做——按请求给 context 加超时（dbTimeout 中间件）是
// 推荐路径；本包历史上的 timeoutPool 浅包装因无法覆写 pgxpool 内部 Acquire
// 已删除（2026-10-06 清理，见 docs/archive/2026-10/repo-cleanup-benchmark-2026-10-06.md）。
func PoolStats(pool *pgxpool.Pool) (acquired, idle, total int32) {
	s := pool.Stat()
	return s.AcquiredConns(), s.IdleConns(), s.TotalConns()
}
