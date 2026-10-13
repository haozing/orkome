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
	LastError           string         `json:"last_error,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
}

type BatchItem struct {
	ID          string         `json:"id"`
	BatchID     string         `json:"batch_id"`
	Seq         int            `json:"seq"`
	PayloadRef  map[string]any `json:"payload_ref"`
	Status      string         `json:"status"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
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

	// ParamsFiller 按 kind 补全 params（llm_comment 需要 workspace_id/
	// work_item_id/agent_id——agent 取工单指派人）。nil = 不补，params 原样
	// 入库；缺必填字段的条目在执行期 fail，错误直达条目（前端可见）。
	ParamsFiller func(ctx context.Context, b *Batch) error
}

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
