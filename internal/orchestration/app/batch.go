package app

import (
	"context"
	"errors"
	"time"

	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

type Batch struct {
	ID                  string         `json:"id"`
	WorkspaceID         string         `json:"workspace_id"`
	WorkItemID          string         `json:"work_item_id"`
	Kind                string         `json:"kind"`
	Status              string         `json:"status"`
	Params              map[string]any `json:"params"`
	Source              map[string]any `json:"source"`
	Total               int            `json:"total"`
	Succeeded           int            `json:"succeeded"`
	Failed              int            `json:"failed"`
	Skipped             int            `json:"skipped"`
	Concurrency         int            `json:"concurrency"`
	ChunkSize           int            `json:"chunk_size"`
	PaceIntervalSeconds int            `json:"pace_interval_seconds"`
	OriginatorUserID    string         `json:"originator_user_id"`
	// agent/task 维度（batch v2.0）：agent 通道创建时填充，批次终态唤醒
	// 创建者收割用；面板/第三方通道为空（走 condition 唤醒扫描）。
	OriginatorTaskID      string         `json:"originator_task_id,omitempty"`
	OriginatorAgentID     string         `json:"originator_agent_id,omitempty"`
	SourceMaterialization map[string]any `json:"source_materialization,omitempty"`
	LastError             string         `json:"last_error,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	CompletedAt           *time.Time     `json:"completed_at,omitempty"`
}

type BatchItem struct {
	ID          string         `json:"id"`
	BatchID     string         `json:"batch_id"`
	Seq         int            `json:"seq"`
	PayloadRef  map[string]any `json:"payload_ref"`
	Status      string         `json:"status"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	ResultRef   map[string]any `json:"result,omitempty"`
	SkipReason  string         `json:"skip_reason,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type BatchProcessor interface {
	Kind() string

	Process(ctx context.Context, params map[string]any, payload map[string]any) (result map[string]any, err error)
}

type BatchRepo interface {
	InsertBatch(ctx context.Context, b Batch, items []map[string]any) (Batch, error)
	GetBatch(ctx context.Context, workspaceID, id string) (Batch, bool, error)
	SetBatchStatus(ctx context.Context, id, status string) error

	ClaimItems(ctx context.Context, batchID, worker string, limit int) ([]BatchItem, error)

	FinishItem(ctx context.Context, itemID, status string, result map[string]any, errMsg string) (done bool, err error)

	CompleteIfAllDone(ctx context.Context, batchID, finalStatus string) (bool, error)

	ReclaimExpiredLeases(ctx context.Context) (int, error)

	RequeueFailed(ctx context.Context, batchID string) (int, error)

	GetBatchCounts(ctx context.Context, batchID string) (pending, claimed, processing, done int, err error)

	ListActiveBatches(ctx context.Context) ([]Batch, error)
	ListBatchesByWorkItem(ctx context.Context, ws, workItemID string) ([]Batch, error)
	ListItems(ctx context.Context, ws, batchID, status string, limit int) ([]BatchItem, error)
	ListItemsPage(ctx context.Context, ws, batchID, status string, afterSeq, limit int) ([]BatchItem, error)
	StartBatch(ctx context.Context, id string) (bool, error)
	// InsertSourceItems 通道 B 落表：成品与 skipped 占位同序写成（seq 连续），
	// skip_reason 随行落列；batch 行带 status=draft + source_materialization。
	InsertSourceItems(ctx context.Context, b Batch, mat *MaterializedSource) (Batch, error)
}

type ProgressEmitter interface {
	EmitBatchProgress(ctx context.Context, batchID string, done, total int) error
}

type BatchService struct {
	repo BatchRepo
	emit ProgressEmitter
	done BatchDoneNotifier
	bus  BatchEmitter
	now  func() time.Time

	// OriginatorWaker 批次终态唤醒创建者 agent（batch v2.0 设计 §2.2，
	// automation 域 WakeupService.WakeAgentForBatch 接线）。nil = 跳过
	//（agent 唤醒缺席，面板/第三方批次走现有 condition 扫描）。
	originatorWaker OriginatorWaker

	// ParamsFiller 按 kind 补全 params（llm_comment 需要 workspace_id/
	// work_item_id/agent_id——agent 取工单指派人）。nil = 不补，params 原样
	// 入库；缺必填字段的条目在执行期 fail，错误直达条目（前端可见）。
	ParamsFiller func(ctx context.Context, b *Batch) error
}

// OriginatorWaker 创建者 agent 唤醒端口：批次终态后给 originator agent 插
// 收割任务（evidence 带 batch_id + 四态计数摘要）。nil = 未接线。
type OriginatorWaker interface {
	WakeBatchOriginator(ctx context.Context, ws, workItemID, batchID, originatorTaskID, originatorAgentID string, summary map[string]any) error
}

// SetOriginatorWaker 接线创建者 agent 唤醒（组装根调用，幂等覆盖）。
func (s *BatchService) SetOriginatorWaker(w OriginatorWaker) { s.originatorWaker = w }

func NewBatchService(repo BatchRepo, emit ProgressEmitter) *BatchService {
	return &BatchService{repo: repo, emit: emit, now: time.Now}
}

func (s *BatchService) CreateAndMaterialize(ctx context.Context, b Batch, payloads []map[string]any) (*Batch, error) {
	if len(payloads) == 0 {
		return nil, errors.New("empty batch")
	}
	if len(payloads) > domain.MaxItemsPerBatch {
		return nil, errors.New("batch exceeds 100000 items")
	}

	for _, p := range payloads {
		if big, ok := p["content"].(string); ok && len(big) > 4096 {
			return nil, errors.New("payload inlines large object (max inline 4KB)")
		}
	}
	if b.Concurrency <= 0 || b.Concurrency > 64 {
		b.Concurrency = 4
	}
	if b.ChunkSize <= 0 {
		b.ChunkSize = 100
	}
	if b.Params == nil {
		b.Params = map[string]any{}
	}
	if b.Source == nil {
		b.Source = map[string]any{}
	}
	if s.ParamsFiller != nil {
		if err := s.ParamsFiller(ctx, &b); err != nil {
			return nil, err
		}
	}
	b.Total = len(payloads)
	b.Status = domain.BatchPending
	out, err := s.repo.InsertBatch(ctx, b, payloads)
	if err != nil {
		return nil, err
	}
	if s.bus != nil {
		s.bus.Emit(ctx, events.BatchCreated, map[string]any{"batch_id": out.ID,
			"work_item_id": out.WorkItemID, "kind": out.Kind, "total": out.Total,
			// FP-20 F3：供 cmd 装配层 batchEmitter 提取 Envelope.WorkspaceID，
			// 否则 batch 帧被实时广播门禁（空 WorkspaceID）整族丢弃。
			"workspace_id": out.WorkspaceID})
	}
	return &out, nil
}

func (s *BatchService) Pause(ctx context.Context, ws, id string) error {
	b, ok, err := s.repo.GetBatch(ctx, ws, id)
	if err != nil || !ok {
		return errors.New("batch not found")
	}
	if b.Status != domain.BatchRunning && b.Status != domain.BatchPending {
		return errors.New("cannot pause")
	}
	return s.repo.SetBatchStatus(ctx, id, domain.BatchPaused)
}

func (s *BatchService) Resume(ctx context.Context, ws, id string) error {
	b, ok, err := s.repo.GetBatch(ctx, ws, id)
	if err != nil || !ok {
		return errors.New("batch not found")
	}
	if b.Status != domain.BatchPaused {
		return errors.New("cannot resume")
	}
	return s.repo.SetBatchStatus(ctx, id, domain.BatchRunning)
}

func (s *BatchService) Cancel(ctx context.Context, ws, id string) error {
	b, ok, err := s.repo.GetBatch(ctx, ws, id)
	if err != nil || !ok {
		return errors.New("batch not found")
	}
	if domain.BatchTerminal(b.Status) {
		return errors.New("already terminal")
	}
	return s.repo.SetBatchStatus(ctx, id, domain.BatchCancelled)
}

func (s *BatchService) RequeueFailed(ctx context.Context, ws, id string) (int, error) {
	if _, ok, err := s.repo.GetBatch(ctx, ws, id); err != nil || !ok {
		return 0, errors.New("batch not found")
	}
	return s.repo.RequeueFailed(ctx, id)
}

func (s *BatchService) RunWorker(ctx context.Context, b Batch, proc BatchProcessor, worker string, ticks int) (int, error) {
	processed := 0
	lastEmittedChunk := -1
	for t := 0; t < ticks; t++ {
		items, err := s.repo.ClaimItems(ctx, b.ID, worker, b.Concurrency)
		if err != nil {
			return processed, err
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			result, perr := proc.Process(ctx, b.Params, it.PayloadRef)
			status := domain.ItemSucceeded
			msg := ""
			if perr != nil {
				if it.Attempts+1 >= it.MaxAttempts {
					status = domain.ItemFailed
				} else {
					status = domain.ItemPending
				}
				msg = perr.Error()
			}
			done, err := s.repo.FinishItem(ctx, it.ID, status, result, msg)
			if err != nil {
				return processed, err
			}
			processed++

			if done {
				_, _, _, doneN, _ := s.repo.GetBatchCounts(ctx, b.ID)
				final := domain.BatchCompleted
				if doneN >= b.Total {

					if b.Failed > 0 && b.Succeeded == 0 {
						final = domain.BatchFailed
					}
				}
				won, err := s.repo.CompleteIfAllDone(ctx, b.ID, final)
				if err == nil && won {
					if s.bus != nil {
						evName := events.BatchCompleted
						if final == domain.BatchFailed {
							evName = events.BatchFailed
						}
						s.bus.Emit(ctx, evName,
							map[string]any{"batch_id": b.ID, "work_item_id": b.WorkItemID,
								"total": b.Total, "done": doneN, "workspace_id": b.WorkspaceID})
					}
					// 创建者 agent 唤醒（batch v2.0 设计 §2.2）：completed/failed
					// 都唤——failed 批次同样需要 agent 收割失败原因并处置。
					// 面板/无 agent 通道（originator_agent_id 空）由实现方跳过，
					// 走现有 condition 扫描（s.done.NotifyBatchDone）。唤醒失败
					// 只记日志不阻断——批次已是终态，唤醒可由面板手动触发。
					if s.originatorWaker != nil {
						// 终态不可逆：唤醒失败不阻断 item 循环（日志由实现方记，
						// 面板通道兜底可见批次终态）。
						_ = s.originatorWaker.WakeBatchOriginator(ctx, b.WorkspaceID, b.WorkItemID,
							b.ID, b.OriginatorTaskID, b.OriginatorAgentID, s.terminalSummary(b, final, doneN))
					}
					if final == domain.BatchCompleted && s.done != nil {
						_ = s.done.NotifyBatchDone(ctx, b.WorkspaceID, b.WorkItemID, b.ID)
					}
				}
			}
		}

		if s.emit != nil || s.bus != nil {
			_, _, _, doneN, _ := s.repo.GetBatchCounts(ctx, b.ID)
			if doneN/b.ChunkSize > lastEmittedChunk || doneN >= b.Total {
				if s.emit != nil {
					_ = s.emit.EmitBatchProgress(ctx, b.ID, doneN, b.Total)
				}
				if s.bus != nil {
					s.bus.Emit(ctx, events.BatchProgress, map[string]any{
						"batch_id": b.ID, "done": doneN, "total": b.Total,
						"workspace_id": b.WorkspaceID})
				}
				lastEmittedChunk = doneN / b.ChunkSize
			}
		}
	}
	return processed, nil
}

func (s *BatchService) Get(ctx context.Context, ws, id string) (Batch, bool, error) {
	return s.repo.GetBatch(ctx, ws, id)
}

func (s *BatchService) Counts(ctx context.Context, id string) (int, int, int, int, error) {
	return s.repo.GetBatchCounts(ctx, id)
}

func (s *BatchService) ListByWorkItem(ctx context.Context, ws, workItemID string) ([]Batch, error) {
	return s.repo.ListBatchesByWorkItem(ctx, ws, workItemID)
}

func (s *BatchService) ListItems(ctx context.Context, ws, batchID, status string, limit int) ([]BatchItem, error) {
	return s.repo.ListItems(ctx, ws, batchID, status, limit)
}

func (s *BatchService) ListActive(ctx context.Context) ([]Batch, error) {
	return s.repo.ListActiveBatches(ctx)
}

func (s *BatchService) SweepExpiredLeases(ctx context.Context) (int, error) {
	return s.repo.ReclaimExpiredLeases(ctx)
}

type BatchDoneNotifier interface {
	NotifyBatchDone(ctx context.Context, workspaceID, workItemID, batchID string) error
}

type BatchEmitter interface {
	Emit(ctx context.Context, event string, payload map[string]any)
}

func (s *BatchService) SetBatchPorts(n BatchDoneNotifier, e BatchEmitter) {
	s.done, s.bus = n, e
}

// Start 把 draft 批次转入 pending（通道 B：物化预览确认后显式开跑）。
// 非 draft（已开跑/终态）拒绝——重试失败走 RequeueFailed，不重复 start。
func (s *BatchService) Start(ctx context.Context, ws, id string) error {
	b, ok, err := s.repo.GetBatch(ctx, ws, id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("batch not found")
	}
	if b.Status != domain.BatchDraft {
		return errors.New("cannot start: batch is not in draft state")
	}
	started, err := s.repo.StartBatch(ctx, id)
	if err != nil {
		return err
	}
	if !started {
		return errors.New("cannot start: batch is not in draft state")
	}
	if s.bus != nil {
		s.bus.Emit(ctx, events.BatchCreated, map[string]any{"batch_id": b.ID,
			"work_item_id": b.WorkItemID, "kind": b.Kind, "total": b.Total,
			"workspace_id": b.WorkspaceID})
	}
	return nil
}

// ListItemsPage 结果收割分页（batch_results 工具/面板共用）：afterSeq 游标。
func (s *BatchService) ListItemsPage(ctx context.Context, ws, batchID, status string, afterSeq, limit int) ([]BatchItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListItemsPage(ctx, ws, batchID, status, afterSeq, limit)
}

// terminalSummary 终态四态计数摘要（唤醒 evidence 与 source_materialization
// 对账同口径：total = succeeded + failed + skipped + pending 残余）。
func (s *BatchService) terminalSummary(b Batch, final string, doneN int) map[string]any {
	return map[string]any{
		"batch_id": b.ID, "kind": b.Kind, "final_status": final,
		"total": b.Total, "done": doneN,
		"succeeded": b.Succeeded, "failed": b.Failed, "skipped": b.Skipped,
	}
}

// CreateDraftFromSource 通道 B：source+template 交给物化器展开成品 payloads
// 落 draft 批次（worker 不取 draft），预览确认后 Start 开跑。ParamsFiller
// 先行（agent_id 等上下文补全后物化器占位符才有值），物化结构性失败 =
// 创建失败（fail-fast，不产生半物化批次）；条目级字段缺失计 skipped 带
// skip_reason，对账落 source_materialization。skipped 条目与成品同序落库
// （占位 payload_ref + skipped 状态），InsertSourceItems 一次写成。
func (s *BatchService) CreateDraftFromSource(ctx context.Context, b Batch, matSource func(ctx context.Context, b *Batch) (*MaterializedSource, error)) (*Batch, error) {
	if b.Params == nil {
		b.Params = map[string]any{}
	}
	if s.ParamsFiller != nil {
		if err := s.ParamsFiller(ctx, &b); err != nil {
			return nil, err
		}
	}
	if matSource == nil {
		return nil, errors.New("source materialization not wired")
	}
	mat, err := matSource(ctx, &b)
	if err != nil {
		return nil, err
	}
	b.Total = mat.SourceCount
	b.Status = domain.BatchDraft
	// 与通道 A 同款默认化（CreateAndMaterialize 内联那份的镜像）：draft 也
	// 必须带合法并发——0 并发会让 worker 的 ClaimItems LIMIT 0 永远空转。
	if b.Concurrency <= 0 || b.Concurrency > 64 {
		b.Concurrency = 4
	}
	if b.ChunkSize <= 0 {
		b.ChunkSize = 100
	}
	b.SourceMaterialization = map[string]any{
		"source_count": mat.SourceCount, "materialized": mat.Materialized, "skipped": mat.Skipped,
	}
	out, err := s.repo.InsertSourceItems(ctx, b, mat)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// BatchToolPort 是 batch 工具面（batch_create/status/results）的能力窄口：
// 引擎按此注入，不依赖 *BatchService 具体类型。
type BatchToolPort interface {
	CreateAndMaterialize(ctx context.Context, b Batch, payloads []map[string]any) (*Batch, error)
	CreateDraftFromSource(ctx context.Context, b Batch, matSource func(ctx context.Context, b *Batch) (*MaterializedSource, error)) (*Batch, error)
	GetBatch(ctx context.Context, ws, id string) (Batch, bool, error)
	Start(ctx context.Context, ws, id string) error
	ListItemsPage(ctx context.Context, ws, batchID, status string, afterSeq, limit int) ([]BatchItem, error)
}

// GetBatch 单批次查询（batch_status 工具面）。
func (s *BatchService) GetBatch(ctx context.Context, ws, id string) (Batch, bool, error) {
	return s.repo.GetBatch(ctx, ws, id)
}
