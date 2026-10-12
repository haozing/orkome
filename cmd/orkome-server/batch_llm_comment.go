package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	orchengine "orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

// llmCommentBatchProcessor 首个真模型 BatchProcessor（kind=llm_comment）：
// 对每个 payload 用工单指派 agent 的模型生成一段中文结论，并以 agent 身份
// post_comment 到工单——为 batch 扇出骨架（Claim/Finish/计数/batch_done
// 唤醒）提供首个真实执行面参考实现。此前 Registry 为空，任何 batch 都被
// worker 静默跳过（执行面插件留白）。
//
// payload 形状：{"seed": string}（必填，生成主题）；{"lang": string}（可选，
// 默认 zh）。
type llmCommentBatchProcessor struct {
	engine  *orchengine.EinoEngine
	wb      orchapp.WorkBench
	resolve func(ctx context.Context, ws, agentID string) (orchapp.Agent, string, error)
	log     *slog.Logger
}

func (p *llmCommentBatchProcessor) Kind() string { return "llm_comment" }

func (p *llmCommentBatchProcessor) Process(ctx context.Context, params, payload map[string]any) (map[string]any, error) {
	seed, _ := payload["seed"].(string)
	if strings.TrimSpace(seed) == "" {
		return nil, fmt.Errorf("payload.seed required")
	}
	ws, _ := params["workspace_id"].(string)
	wi, _ := params["work_item_id"].(string)
	agentID, _ := params["agent_id"].(string)
	if ws == "" || wi == "" || agentID == "" {
		return nil, fmt.Errorf("params.workspace_id/work_item_id/agent_id required")
	}

	agent, secret, err := p.resolve(ctx, ws, agentID)
	if err != nil {
		return nil, err
	}

	lang, _ := payload["lang"].(string)
	if lang == "" {
		lang = "zh"
	}
	prompt := fmt.Sprintf("请针对主题「%s」写一段不超过 120 字的中文要点摘要（面向工单评论区读者），直接输出正文。", seed)
	out, _, err := p.engine.Generate(ctx, agent, secret,
		"你是内容工厂的批量摘要员工。只输出要求的正文，不要额外解释。", prompt)
	if err != nil {
		return nil, err
	}
	if err := p.wb.PostAgentComment(ctx, ws, wi, agent.ID, out); err != nil {
		return nil, err
	}
	p.log.Info("batch llm_comment processed", "seed", seed, "out_len", len([]rune(out)))
	return map[string]any{"seed": seed, "lang": lang, "posted": true}, nil
}
