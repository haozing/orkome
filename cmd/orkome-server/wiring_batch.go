package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"orkome/internal/contract/events"
	orchworkers "orkome/internal/orchestration/adapters/workers"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
)

func wiringBatch(log *slog.Logger, bus *eventbus.Bus, batches *orchapp.BatchService,
	wake WakeupBatchPort, registry map[string]orchapp.BatchProcessor,
) *orchworkers.BatchWorker {
	batches.SetBatchPorts(batchDoneAdapter{wake: wake}, batchEmitter{bus: bus})
	// batch v2.0 设计 §2.2：批次终态唤醒创建者 agent（复用 automation 域
	// WakeupService 的建任务机制，绕过条件规则）。nil 安全——wake 未接线
	//（如部分测试装配）时 waker 整体缺席，行为与接线前一致。
	if wake != nil {
		batches.SetOriginatorWaker(originatorWaker{wake: wake, log: log})
	}
	if registry == nil {
		registry = map[string]orchapp.BatchProcessor{}
	}
	return &orchworkers.BatchWorker{
		Svc: batches, Registry: registry, Log: log,
		Every: 2e9, WorkerID: "batch-1",
	}
}

type WakeupBatchPort interface {
	NotifyBatchDone(ctx context.Context, ws, workItemID, batchID string) error

	// WakeAgentForBatch 批次终态按创建者 agent 直接建收割任务（设计 §2.2
	// 接线裁决：不走条件规则扫描；agentID 为空时实现方跳过）。
	WakeAgentForBatch(ctx context.Context, ws, workItemID, agentID, taskID, evidence string) error
}

// originatorWaker orchapp.OriginatorWaker 的 automation 适配器：把终态计数
// 摘要编成 evidence 交给 WakeupService.WakeAgentForBatch（MergeIntoPending
// 去重 + trigger 上下文同款链路）。失败只记日志——批次已是终态，唤醒可由
// 面板手动补（condition 扫描通道仍在）。
type originatorWaker struct {
	wake WakeupBatchPort
	log  *slog.Logger
}

func (w originatorWaker) WakeBatchOriginator(ctx context.Context, ws, workItemID, batchID,
	originatorTaskID, originatorAgentID string, summary map[string]any) error {
	if originatorAgentID == "" {
		return nil // 面板/第三方通道：无创建者 agent，走现有 condition 扫描
	}
	summaryJSON, _ := json.Marshal(summary)
	evidence := fmt.Sprintf("Batch %s terminal. Summary: %s. Use batch_status/batch_results to harvest.",
		batchID, string(summaryJSON))
	if err := w.wake.WakeAgentForBatch(ctx, ws, workItemID, originatorAgentID, originatorTaskID, evidence); err != nil {
		if w.log != nil {
			w.log.Error("batch originator wake failed", "batch", batchID, "agent", originatorAgentID, "err", err)
		}
		return err
	}
	if w.log != nil {
		w.log.Info("batch originator wake enqueued", "batch", batchID, "agent", originatorAgentID)
	}
	return nil
}

type batchDoneAdapter struct{ wake WakeupBatchPort }

func (a batchDoneAdapter) NotifyBatchDone(ctx context.Context, ws, wi, batchID string) error {
	return a.wake.NotifyBatchDone(ctx, ws, wi, batchID)
}

type batchEmitter struct{ bus *eventbus.Bus }

func (e batchEmitter) Emit(ctx context.Context, event string, payload map[string]any) {
	e.bus.Emit(ctx, events.Envelope{
		Event: event,
		// FP-20 F3：batch 帧此前不带 WorkspaceID，被 wiring_ws 的广播门禁
		// （空 WorkspaceID 静默 return）整族丢弃——batch.created/progress/
		// completed/failed 四类帧全灭。工作区 ID 由 app 层从 batch 实体放进
		// payload（orchapp/batch.go 三个 Emit 位点）。
		WorkspaceID: payloadString(payload, "workspace_id"),
		Payload:     payload,
		OccurredAt:  time.Now().UTC(),
	})
}

func payloadString(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}
