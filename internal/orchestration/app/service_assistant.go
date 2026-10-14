package app

// AssistantTaskService 助理接入的任务协议面（docs/assistant-access-design-
// 2026-10-14.md v4 §4）：用户本机 AI 以用户本人身份认领/执行/结算工单，任务
// 行挂在用户的 proxy agent 行下（runtime_kind='assistant_proxy'），时间线上
// 与引擎任务同构。有人值守假设（§1.2）：平台不做活性检测，僵尸窗口期的唯一
// 动作是 stalled 超时挂起+交还（sweeper 见 workers 包）。

import (
	"context"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

// AssistantStatusUpdater 推进工单的函数端口（跨域经 wiring 注入，域层保持
// 干净）：work 域 UpdateStatusByAgent 通道 + status 目录查询。done 取该工作
// 区 category='done' 且 position 最小的 status_key；todo 取待处理类（unstarted
// 非 backlog 优先，回落 todo 语义）——status_key 每区可配，AI 与 sweeper 都
// 不能靠猜。terminal 判断工单当前 key 是否终态类（done/closed）。
type AssistantStatusUpdater interface {
	// UpdateStatusByAgent 经代理通道推进工单（revision 冲突由实现返回
	// "revision conflict" 错误，CompleteWorkItem 降级为只结算）。
	UpdateStatusByAgent(ctx context.Context, ws, workItemID, agentID, statusKey string) error
	// DoneStatusKey 完成态 status_key（category='done' 最小 position）。
	DoneStatusKey(ctx context.Context, ws string) (string, error)
	// TodoStatusKey 待处理态 status_key（stalled 交还目标）。
	TodoStatusKey(ctx context.Context, ws string) (string, error)
	// IsTerminalStatus 工单当前 key 是否终态类。
	IsTerminalStatus(ctx context.Context, ws, key string) (bool, error)
}

// AssistantNotifier stalled 交还通知的最薄路径：直写 notification 域的
// notice 面（dedup 去重），不经事件投影——超时挂起不是任何事件的副产物，
// 是 sweeper 的主动动作，直写比造事件+订阅投影少两层间接。
type AssistantNotifier interface {
	WriteStalledNotice(ctx context.Context, ws, userID, workItemID, title string) error
}

type AssistantTaskService struct {
	repo Repo
	emit Emitter

	outbox OutboxRecorder
	uow    Within

	summaries *SummaryService
	settle    *SettlementService
	status    AssistantStatusUpdater
	notifier  AssistantNotifier
}

func NewAssistantTaskService(repo Repo, emit Emitter) *AssistantTaskService {
	return &AssistantTaskService{repo: repo, emit: emit}
}

func (s *AssistantTaskService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

// SetSummarySummarizer 接线 summary 生成器（与 ExternalTaskService 同口径：
// 未接线时纯截断回落）。
func (s *AssistantTaskService) SetSummarySummarizer(ss *SummaryService) { s.summaries = ss }

// SetSettlement 接线结算尾部公共层（outcome 段共用）。
func (s *AssistantTaskService) SetSettlement(ss *SettlementService) { s.settle = ss }

// SetStatusUpdater 接线工单推进/回退端口（wiring 注入 work 域实现）。
func (s *AssistantTaskService) SetStatusUpdater(u AssistantStatusUpdater) { s.status = u }

// SetNotifier 接线 stalled 交还通知。
func (s *AssistantTaskService) SetNotifier(n AssistantNotifier) { s.notifier = n }

// ResolveProxyAgent 解析（必要时惰性创建）用户的代理行 id。
func (s *AssistantTaskService) ResolveProxyAgent(ctx context.Context, ws, userID string) (string, error) {
	return s.repo.EnsureAssistantProxyAgent(ctx, ws, userID)
}

func (s *AssistantTaskService) summaryForComplete(ctx context.Context, t Task, result string) string {
	if s.summaries == nil {
		return domain.TaskSummary(s.lastTaskText(ctx, t.ID), result)
	}
	return s.summaries.ForComplete(ctx, t, result)
}

func (s *AssistantTaskService) summaryForFail(ctx context.Context, t Task, reason, errMsg string) string {
	if s.summaries == nil {
		return domain.TaskSummary(s.lastTaskText(ctx, t.ID), reason+": "+errMsg)
	}
	return s.summaries.ForFail(ctx, t, reason, errMsg)
}

func (s *AssistantTaskService) lastTaskText(ctx context.Context, taskID string) string {
	txt, err := s.repo.LastTaskText(ctx, taskID)
	if err != nil {
		return ""
	}
	return txt
}

// guard 语义参照 service_external.go（任务属于该代理行+状态校验），
// agentID 换 proxy 行 id。
func (s *AssistantTaskService) guard(ctx context.Context, ws, taskID, agentID string, wantRunning bool) (Task, error) {
	t, ok, err := s.repo.GetTask(ctx, ws, taskID)
	if err != nil {
		return t, err
	}
	if !ok {
		return t, apierr.New(apierr.NotFound, "task not found")
	}
	if t.AgentID != agentID {
		return t, apierr.New(apierr.Forbidden, "task belongs to another agent")
	}
	if wantRunning && t.Status != "running" {
		return t, apierr.New(apierr.Conflict, "task not running")
	}
	return t, nil
}

// ClaimWorkItem 自动接单入口（v4 §4.2）：proxy 行惰性创建 + 单事务行锁接单
// （防重/工单校验/插任务/CAS 置 running 全在 repo.ClaimAssistantTask 一条
// 事务里）。冲突与校验失败直接以 apierr 透出（409/403/404）。
func (s *AssistantTaskService) ClaimWorkItem(ctx context.Context, ws, userID, workItemID string) (*Task, error) {
	// 步骤 0（幂等，事务外）：proxy 行 ON CONFLICT DO NOTHING——部分唯一
	// 索引兜底并发首触。
	agentID, err := s.repo.EnsureAssistantProxyAgent(ctx, ws, userID)
	if err != nil {
		return nil, err
	}
	t, ok, err := s.repo.ClaimAssistantTask(ctx, AssistantClaim{
		WorkspaceID: ws, AgentID: agentID, UserID: userID, WorkItemID: workItemID,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.Conflict, "already claimed")
	}
	s.emit.Emit(ctx, envelope(events.TaskRunning, ws, userID, TaskPayload{
		ID: t.ID, WorkspaceID: ws, AgentID: t.AgentID,
		WorkItemID: t.WorkItemID, Status: t.Status}))
	return &t, nil
}

// ReportProgress 纯进度消息（AppendTaskMessage）——v4 已删活性检测，不再
// 承担心跳职能，AI 随做随报，节奏自由。
func (s *AssistantTaskService) ReportProgress(ctx context.Context, ws, userID, taskID, content string) (int64, error) {
	agentID, err := s.repo.EnsureAssistantProxyAgent(ctx, ws, userID)
	if err != nil {
		return 0, err
	}
	if _, err := s.guard(ctx, ws, taskID, agentID, true); err != nil {
		return 0, err
	}
	return s.repo.AppendTaskMessage(ctx, ws, taskID, "text", "", content, false)
}

// CompleteWorkItem 结算 + 推进工单（v4 §4.2）：
//  1. 公共尾部落 outcome（与引擎 Settle 同一份 WriteOutcomeFor）；
//  2. 推进规则（有人值守强度——克制）：推到 done 类最小 position 的
//     status_key；工单已在终态类 → 只结算不推进；revision 冲突 → 结算已
//     成功，放弃推进（不重试——人就在旁边）。
func (s *AssistantTaskService) CompleteWorkItem(ctx context.Context, ws, userID, taskID, result string) error {
	agentID, err := s.repo.EnsureAssistantProxyAgent(ctx, ws, userID)
	if err != nil {
		return err
	}
	t, err := s.guard(ctx, ws, taskID, agentID, true)
	if err != nil {
		return err
	}
	env := envelope(events.TaskCompleted, ws, userID, TaskPayload{
		ID: t.ID, WorkspaceID: ws, AgentID: t.AgentID, WorkItemID: t.WorkItemID,
		Status: "completed", Result: result, OriginatorSource: t.OriginatorSource})
	summary := s.summaryForComplete(ctx, t, result)
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if _, err := s.repo.AppendTaskMessage(ctx, ws, t.ID, "text", "", result, false); err != nil {
			return err
		}
		return s.repo.CompleteRun(ctx, t.ID, result, "", CurrentEngineTag(), summary, UsageReport{})
	}); err != nil {
		return err
	}
	// 公共尾部：工单产出后写覆盖（结算已成功，outcome 失败只留痕）。
	if s.settle != nil {
		s.settle.WriteOutcomeFor(ctx, t, result)
	}
	s.advanceAfterComplete(ctx, ws, t)
	return nil
}

// advanceAfterComplete 按规则推进工单到完成态。终态类（done/closed）不动
// （不越权覆盖用户手动结果）；revision 冲突降级为只结算（留痕于时间线
// ——任务行已完成，工单停在原地，用户可见）。
func (s *AssistantTaskService) advanceAfterComplete(ctx context.Context, ws string, t Task) {
	if s.status == nil || t.WorkItemID == "" {
		return
	}
	wi, ok, err := s.repo.GetTaskWorkItemStatus(ctx, ws, t.WorkItemID)
	if err != nil || !ok {
		return
	}
	terminal, err := s.status.IsTerminalStatus(ctx, ws, wi)
	if err != nil || terminal {
		return
	}
	doneKey, err := s.status.DoneStatusKey(ctx, ws)
	if err != nil || doneKey == "" || doneKey == wi {
		return
	}
	// 经 UpdateStatusByAgent 通道推进；失败（revision 冲突等并发编辑）直接
	// 放弃——结算已成功，不重试。人就在旁边，工单停在原地+时间线留痕即可。
	_ = s.status.UpdateStatusByAgent(ctx, ws, t.WorkItemID, t.AgentID, doneKey)
}

// FailWorkItem 终态无重试：max_attempts=1 已在 claim 时强制，FailRun 的
// 重试分支（attempt≥max）天然不触发。工单不自动推进（失败=交还人决策）。
func (s *AssistantTaskService) FailWorkItem(ctx context.Context, ws, userID, taskID, reason, detail string) error {
	agentID, err := s.repo.EnsureAssistantProxyAgent(ctx, ws, userID)
	if err != nil {
		return err
	}
	t, err := s.guard(ctx, ws, taskID, agentID, true)
	if err != nil {
		return err
	}
	env := envelope(events.TaskFailed, ws, userID, TaskPayload{
		ID: t.ID, WorkspaceID: ws, AgentID: t.AgentID, WorkItemID: t.WorkItemID,
		Status: "failed", OriginatorSource: t.OriginatorSource})
	summary := s.summaryForFail(ctx, t, reason, detail)
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		_, serr := s.repo.FailRun(ctx, t.ID, reason, detail, summary)
		return serr
	}); err != nil {
		return err
	}
	return nil
}

// ListRunningTasks get_my_running_tasks 底面：代理行下 running 任务
// （AI 重启后的活动上下文入口）。
func (s *AssistantTaskService) ListRunningTasks(ctx context.Context, ws, userID string) ([]Task, error) {
	agentID, ok, err := s.repo.GetAssistantProxyAgent(ctx, ws, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return s.repo.ListRunningByAgent(ctx, ws, agentID)
}

// StallExpired 挂起过期代理任务的领域动作（由 workers 的 sweeper 周期调）：
// 置 stalled → 通知用户 → 工单转回待处理（终态类工单不动——已经有人收尾）。
// 返回处理行数供 sweeper 记日志/指标。
func (s *AssistantTaskService) StallExpired(ctx context.Context, hours int) (int, error) {
	rows, err := s.repo.StallIdleAssistantTasks(ctx, hours)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		s.handBackAfterStall(ctx, r)
	}
	return len(rows), nil
}

// handBackAfterStall 交还动作：通知 + 工单转回待处理。两步都 best-effort
// （stalled 已落库，通知/回退失败不该让 sweeper 反复重扫同一行）。
func (s *AssistantTaskService) handBackAfterStall(ctx context.Context, r StalledAssistantTask) {
	if s.notifier != nil && r.OriginatorUserID != "" {
		_ = s.notifier.WriteStalledNotice(ctx, r.WorkspaceID, r.OriginatorUserID,
			r.WorkItemID, "AI 处理超时挂起，工单已回到你的待办")
	}
	if s.status == nil || r.WorkItemID == "" {
		return
	}
	cur, ok, err := s.repo.GetTaskWorkItemStatus(ctx, r.WorkspaceID, r.WorkItemID)
	if err != nil || !ok {
		return
	}
	// 已被推进到终态类（用户/他人收尾）→ 不动。
	terminal, err := s.status.IsTerminalStatus(ctx, r.WorkspaceID, cur)
	if err != nil || terminal {
		return
	}
	todoKey, err := s.status.TodoStatusKey(ctx, r.WorkspaceID)
	if err != nil || todoKey == "" || todoKey == cur {
		return
	}
	_ = s.status.UpdateStatusByAgent(ctx, r.WorkspaceID, r.WorkItemID, r.AgentID, todoKey)
}

// SrcAssistantClaimIs 用途标记：供 payload 分流（TaskCompleted 消费者跳过
// 引擎闲置复位）。字符串比较收敛于此，避免散落字面量。
func SrcAssistantClaimIs(source string) bool { return source == domain.SrcAssistantClaim }
