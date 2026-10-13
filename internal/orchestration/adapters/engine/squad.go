package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"orkome/internal/contract/modelmeta"
	orchapp "orkome/internal/orchestration/app"
)

func squadBriefing(sq orchapp.SquadInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Squad Briefing]\nYou are the leader of squad %s. Squad instructions: %s\n", sq.Name, orDash(sq.Instructions))
	fmt.Fprintf(&b, "Members (%d):\n", len(sq.Members))
	for _, m := range sq.Members {
		fmt.Fprintf(&b, "  - %s（%s）：%s\n", m.Name, m.Role, orDash(m.Description))
	}
	// 双持（choreography 布尔退役）：consult 同轮秒回适合轻量征询；
	// dispatch 正式派发带独立生命周期（全工具/审批/台账）适合长时执行。
	// 何时用哪种由 leader 按任务性质现场判断。
	b.WriteString("Dispatch options per member:\n" +
		"- Consult (member-<name> tools): runs within the same turn, seconds-level, member has NO tools — for quick opinions/drafts.\n" +
		"- Formal dispatch (dispatch_to_member): member gets an independent task with full tools and own approval/budget — use for real execution.\n" +
		"Tracking: use create_child_work_item for sub work items (completion wakes you automatically).\n" +
		"Closing duty: call record_evaluation before finishing for formally dispatched work; when no_action, do not substitute with a comment.")
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (e *EinoEngine) buildMemberTool(ctx context.Context, wsID string, m orchapp.SquadMember) (tool.BaseTool, *usageCounter, error) {
	sec := ""
	if e.CredentialFn != nil {
		if id, _ := m.RuntimeConfig["credential_id"].(string); id != "" {
			sec, _ = e.CredentialFn(ctx, wsID, id)
		}
	}
	rc := memberRC(m)
	inner, err := modelmeta.Build(ctx, rc, sec)
	if err != nil {
		return nil, nil, err
	}
	counter := &usageCounter{provider: rc.Provider, model: rc.Model}
	// 成员未填 description 时给默认说明——eino NewAgentTool 要求非空
	// Description，空值会让整个成员工具构建失败（leader 静默失去该成员，
	// 真模型 E2E 实证：成员任务挂 "non-empty Description"）。
	desc := strings.TrimSpace(m.Description)
	if desc == "" {
		desc = fmt.Sprintf("小队成员 %s（角色：%s）。未填写描述。", m.Name, orDash(m.Role))
	}
	mAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "member-" + m.Name,
		Description: desc,
		Instruction: desc,
		Model:       &countingModel{inner: inner, c: counter},
	})
	if err != nil {
		return nil, nil, err
	}
	return adk.NewAgentTool(ctx, mAgent), counter, nil
}

func memberRC(m orchapp.SquadMember) modelmeta.RuntimeConfig {
	var rc modelmeta.RuntimeConfig
	b, _ := json.Marshal(m.RuntimeConfig)
	_ = json.Unmarshal(b, &rc)
	return rc
}

type countingModel struct {
	inner model.BaseChatModel
	c     *usageCounter
}

type usageCounter struct {
	provider, model    string
	prompt, completion int64
}

func (c *countingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := c.inner.Generate(ctx, input, opts...)
	if err == nil && msg != nil {
		c.add(msg)
	}
	return msg, err
}

func (c *countingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return c.inner.Stream(ctx, input, opts...)
}

func (c *countingModel) add(msg *schema.Message) {
	if meta := msg.ResponseMeta; meta != nil && meta.Usage != nil {
		c.c.prompt += int64(meta.Usage.PromptTokens)
		c.c.completion += int64(meta.Usage.CompletionTokens)
	}
}

func (c *usageCounter) report() orchapp.UsageReport {
	return orchapp.UsageReport{Provider: c.provider, Model: c.model,
		PromptTokens: c.prompt, CompletionTokens: c.completion}
}

type memberRun struct {
	agentID, label string
	ctr            *usageCounter
}

func memberUsageOf(runs []memberRun) []orchapp.MemberUsage {
	out := make([]orchapp.MemberUsage, 0, len(runs))
	for _, r := range runs {
		if r.ctr == nil {
			continue
		}
		u := r.ctr.report()
		if u.PromptTokens == 0 && u.CompletionTokens == 0 {
			continue
		}
		out = append(out, orchapp.MemberUsage{AgentID: r.agentID, Label: r.label, Usage: u})
	}
	return out
}

func multimodalOf(msg *schema.Message) ([]orchapp.ContentPart, string) {
	if len(msg.MultiContent) > 0 {
		return partsOf(msg.MultiContent)
	}
	var out []orchapp.ContentPart
	var text string
	for _, p := range msg.AssistantGenMultiContent {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			text += p.Text
			out = append(out, orchapp.ContentPart{Type: "text", Text: p.Text})
		case schema.ChatMessagePartTypeImageURL:
			u := ""
			if p.Image != nil && p.Image.URL != nil {
				u = *p.Image.URL
			}
			out = append(out, orchapp.ContentPart{Type: "image", URL: u})
		case schema.ChatMessagePartTypeAudioURL:
			u := ""
			if p.Audio != nil && p.Audio.URL != nil {
				u = *p.Audio.URL
			}
			out = append(out, orchapp.ContentPart{Type: "audio", URL: u})
		case schema.ChatMessagePartTypeVideoURL:
			u := ""
			if p.Video != nil && p.Video.URL != nil {
				u = *p.Video.URL
			}
			out = append(out, orchapp.ContentPart{Type: "video", URL: u})
		}
	}
	return out, text
}

func partsOf(in []schema.ChatMessagePart) (out []orchapp.ContentPart, text string) {
	for _, p := range in {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			text += p.Text
			out = append(out, orchapp.ContentPart{Type: "text", Text: p.Text})
		case schema.ChatMessagePartTypeImageURL:
			u := ""
			if p.ImageURL != nil {
				u = p.ImageURL.URL
			}
			out = append(out, orchapp.ContentPart{Type: "image", URL: u})
		case schema.ChatMessagePartTypeAudioURL:
			out = append(out, orchapp.ContentPart{Type: "audio", URL: p.AudioURL.URL})
		case schema.ChatMessagePartTypeVideoURL:
			out = append(out, orchapp.ContentPart{Type: "video", URL: p.VideoURL.URL})
		case schema.ChatMessagePartTypeFileURL:
			out = append(out, orchapp.ContentPart{Type: "file", URL: p.FileURL.URL})
		}
	}
	return out, text
}
