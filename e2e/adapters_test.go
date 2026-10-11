package e2e

import (
	"context"
	"net/http"
	"time"

	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/automation/domain"
	"orkome/internal/contract/events"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	notifapp "orkome/internal/notification/app"
	"orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	workhttp "orkome/internal/work/adapters/http"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"

	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"

	orchhttp "orkome/internal/orchestration/adapters/http"
	skillhttp "orkome/internal/skills/adapters/http"
)

func workhttpDeps(db *pg.DB, bus *eventbus.Bus, wRepo *workpgrepo.Repo, idRepo *idpgrepo.Repo) workhttp.Deps {
	return workhttp.Deps{
		WorkItems: workapp.NewWorkItemService(wRepo, bus, time.Now),
		Comments:  workapp.NewCommentService(wRepo, bus, time.Now),
		SourceCtx: workapp.NewSourceContextService(wRepo, bus, nil),
		Catalog:   workapp.NewCatalogService(wRepo, bus),
		Views:     workapp.NewViewService(wRepo),
		Search:    workapp.NewSearchService(wRepo),
		Timeline:  wRepo.ListTimeline, Subscribers: wRepo.ListSubscribers,
		MemberCheck: func(ctx context.Context, ws, uid string) (bool, error) {
			_, ok, err := idRepo.GetMember(ctx, ws, uid)
			return ok, err
		},
		ClientUsage: workapp.NewClientUsageService(wRepo),
	}
}

func workhttpMount(api *http.ServeMux, d workhttp.Deps, authMw func(http.Handler) http.Handler) {
	workhttp.Mount(api, d, authMw)
}

type chatCancel struct{ settle *orchapp.SettlementService }

func (c chatCancel) CancelChatTask(ctx context.Context, ws, sessionID string) (string, string, bool, error) {
	return c.settle.CancelChatTask(ctx, ws, sessionID)
}

func labelViews(ls []workapp.Label, err error) ([]orchhttp.AgentLabelView, error) {
	if err != nil {
		return nil, err
	}
	out := make([]orchhttp.AgentLabelView, 0, len(ls))
	for _, l := range ls {
		out = append(out, orchhttp.AgentLabelView{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	return out, nil
}

func skillLabelViewsOf(ls []workapp.Label, err error) ([]skillhttp.SkillLabelView, error) {
	if err != nil {
		return nil, err
	}
	out := make([]skillhttp.SkillLabelView, 0, len(ls))
	for _, l := range ls {
		out = append(out, skillhttp.SkillLabelView{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	return out, nil
}

type openLimiter struct{}

func (openLimiter) Allow(context.Context, string, int) (bool, time.Duration) { return true, 0 }

type capMail struct{ codes map[string]string }

func (c *capMail) SendLoginCode(_ context.Context, to, code string) error {
	c.codes[to] = code
	return nil
}
func (c *capMail) SendInvite(context.Context, string, string, string) error    { return nil }
func (c *capMail) SendPasswordReset(context.Context, string, string) error     { return nil }
func (c *capMail) SendEmailVerification(context.Context, string, string) error { return nil }

type wbAdapter struct{ wRepo *workpgrepo.Repo }

func (a wbAdapter) PostAgentComment(ctx context.Context, ws, wi, agentID, content string) error {
	_, err := a.wRepo.CreateComment(ctx, workapp.NewComment{
		WorkspaceID: ws, WorkItemID: wi, AuthorType: "agent", AuthorID: agentID, Content: content,
	})
	return err
}
func (a wbAdapter) GetWorkItemBrief(ctx context.Context, ws, wi string) (string, string, error) {
	w, _, err := a.wRepo.GetWorkItem(ctx, ws, wi)
	return w.Title, w.Description, err
}

type cwAdapter struct{ wRepo *workpgrepo.Repo }

func (a cwAdapter) WriteAgentComment(ctx context.Context, ws, wi, agentID, content string) error {
	_, err := a.wRepo.CreateComment(ctx, workapp.NewComment{
		WorkspaceID: ws, WorkItemID: wi, AuthorType: "agent", AuthorID: agentID, Content: content,
	})
	return err
}
func (a cwAdapter) HasAgentComment(ctx context.Context, wi string) (bool, error) {
	cs, err := a.wRepo.ListComments(ctx, wi)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if c.AuthorType == "agent" {
			return true, nil
		}
	}
	return false, nil
}

type chatW struct{ repo *convpgrepo.Repo }

func (c chatW) AppendAssistantMessage(ctx context.Context, sessionID, taskID, content, kind string) error {
	return convapp.NewPointersService(c.repo).AppendAssistantMessage(ctx, sessionID, taskID, content, kind)
}

func (c chatW) AppendAssistantParts(ctx context.Context, sessionID, taskID, content, kind string, parts []orchapp.ContentPart) error {
	ps := convapp.NewPointersService(c.repo)
	pm := make([]map[string]any, len(parts))
	for i, p := range parts {
		pm[i] = map[string]any{"type": p.Type, "text": p.Text, "url": p.URL}
	}
	return ps.AppendAssistantParts(ctx, sessionID, taskID, content, kind, pm)
}
func (c chatW) UpdateResumePointer(ctx context.Context, sessionID, ref string) error {
	return c.repo.UpdateResumePointer(ctx, sessionID, ref)
}

func (c chatW) ClearResumePointer(ctx context.Context, sessionID string) error {
	return c.repo.ClearResumePointer(ctx, sessionID)
}

type apvNotif struct{ repo *notifpgrepo.Repo }

func (a apvNotif) WriteApprovalRequest(ctx context.Context, ws, user, taskID, payload string) error {
	return a.repo.WriteApprovalRequest(ctx, ws, user, taskID, payload)
}

type engWrap struct{ e *engine.EinoEngine }

func (w engWrap) Name() string { return w.e.Name() }
func (w engWrap) Execute(ctx context.Context, t orchapp.Task, a orchapp.Agent, s string) (orchapp.EngineOutput, error) {
	return w.e.Execute(ctx, t, a, s)
}
func (w engWrap) Resume(ctx context.Context, t orchapp.Task, a orchapp.Agent, s, tid string, d orchapp.ApprovalDecision) (orchapp.EngineOutput, error) {
	return w.e.Resume(ctx, t, a, s, tid, d)
}
func (w engWrap) Generate(ctx context.Context, a orchapp.Agent, s, system, input string) (string, orchapp.UsageReport, error) {
	return w.e.Generate(ctx, a, s, system, input)
}

type trigAdapter struct{ trig *orchapp.TriggerService }

func (a trigAdapter) Preview(ctx context.Context, in workapp.TriggerInput) ([]workapp.PreviewHit, []workapp.PreviewHit, error) {
	out, err := a.trig.Preview(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	})
	if err != nil {
		return nil, nil, err
	}
	var tg, sp []workapp.PreviewHit
	for _, h := range out.Triggers {
		tg = append(tg, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	for _, h := range out.Suppressions {
		sp = append(sp, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	return tg, sp, nil
}

func (a trigAdapter) OnComment(ctx context.Context, in workapp.TriggerInput, commentID string) (string, string, error) {
	return a.trig.OnComment(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	}, commentID)
}

type wiReader struct{ wRepo *workpgrepo.Repo }

func (r wiReader) GetWorkItem(ctx context.Context, ws, id string) (orchapp.WorkItemInfo, bool, error) {
	w, ok, err := r.wRepo.GetWorkItem(ctx, ws, id)
	return orchapp.WorkItemInfo{ID: w.ID, Title: w.Title, Desc: w.Description}, ok, err
}

type subsReader struct{ wRepo *workpgrepo.Repo }

func (r subsReader) ListSubscriberUserIDs(ctx context.Context, wi string) ([]string, error) {
	ss, err := r.wRepo.ListSubscribers(ctx, wi)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.UserID
	}
	return out, nil
}

type creatorAdapter struct{ wRepo *workpgrepo.Repo }

func (a creatorAdapter) CreateForAutopilot(ctx context.Context, ws, title, desc, agentID, creator, origin string) (string, error) {
	wi, err := a.wRepo.CreateWorkItem(ctx, workapp.NewWorkItem{
		WorkspaceID: ws, Title: title, Description: desc, StatusKey: "todo",
		AssigneeType: "agent", AssigneeID: agentID, CreatorType: "user", CreatorID: creator, OriginType: origin,
	})
	return wi.ID, err
}

type enqAdapter struct{ repo orchapp.Repo }

func (a enqAdapter) EnqueueForWakeupWithID(ctx context.Context, ws, wi, agentID, userID, wakeupID, evidence string) (string, bool, error) {
	if wi != "" {
		merged, err := a.repo.MergeIntoPending(ctx, ws, wi, agentID, "")
		if err != nil {
			return "", false, err
		}
		if merged {
			return "", true, nil
		}
	}
	t, err := a.repo.InsertTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "wakeup", "wakeup_note": evidence, "wakeup_id": wakeupID},
		OriginatorUserID: userID, OriginatorSource: "trigger_owner", TriggerEvidenceKind: "rule",
	})
	if err != nil {
		return "", false, err
	}
	return t.ID, false, nil
}

func (a enqAdapter) EnqueueForWakeup(ctx context.Context, ws, wi, agentID, userID, evidence string) (string, bool, error) {
	if wi != "" {
		merged, err := a.repo.MergeIntoPending(ctx, ws, wi, agentID, "")
		if err != nil {
			return "", false, err
		}
		if merged {
			return "", true, nil
		}
	}
	t, err := a.repo.InsertTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "wakeup", "wakeup_note": evidence},
		OriginatorUserID: userID, OriginatorSource: "trigger_owner", TriggerEvidenceKind: "rule",
	})
	return t.ID, false, err
}

type factsAdapter struct{ wRepo *workpgrepo.Repo }

func (a factsAdapter) WorkItemFacts(ctx context.Context, ws, wi string) (string, int, error) {
	w, _, err := a.wRepo.GetWorkItem(ctx, ws, wi)
	return w.StatusKey, w.Priority, err
}

func (a factsAdapter) ChildStageFacts(ctx context.Context, ws, wi string) ([]domain.ChildStageFact, error) {
	terminal := map[string]bool{"done": true, "cancelled": true}
	if defs, err := a.wRepo.ListStatuses(ctx, ws); err == nil {
		for _, d := range defs {
			if d.Category == "done" || d.Category == "closed" {
				terminal[d.Key] = true
			}
		}
	}
	rows, err := a.wRepo.DB.Tx(ctx).Query(ctx, `
SELECT coalesce(stage,''), status_key FROM work_item
WHERE workspace_id=$1 AND parent_work_item_id=$2::uuid`, ws, wi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChildStageFact{}
	for rows.Next() {
		var f domain.ChildStageFact
		var st string
		if err := rows.Scan(&f.Stage, &st); err != nil {
			return nil, err
		}
		f.Terminal = terminal[st]
		out = append(out, f)
	}
	return out, rows.Err()
}

func (a factsAdapter) ParentOf(ctx context.Context, ws, wi string) (string, error) {
	var parent string
	err := a.wRepo.DB.Tx(ctx).QueryRow(ctx, `
SELECT coalesce(parent_work_item_id::text,'') FROM work_item
WHERE workspace_id=$1 AND id=$2::uuid`, ws, wi).Scan(&parent)
	if err != nil {
		return "", err
	}
	return parent, nil
}

func (a factsAdapter) WakeupMemberTarget(ctx context.Context, ws, wi string) (string, error) {
	var typ, id string
	err := a.wRepo.DB.Tx(ctx).QueryRow(ctx, `
SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item
WHERE workspace_id=$1 AND id=$2::uuid`, ws, wi).Scan(&typ, &id)
	if err != nil {
		return "", err
	}
	if typ == "user" {
		return id, nil
	}
	return "", nil
}

func (a factsAdapter) WakeupTargetAgent(context.Context, string, string) (string, error) {
	return "", nil
}

var _ = events.TaskQueued
var _ = webx.SourceSession
var _ = notifapp.ParseMentionedUsers
