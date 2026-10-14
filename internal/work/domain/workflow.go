package domain

import (
	"errors"
	"fmt"
	"sort"
)

// Workflow 执行者类型（workflow_stage.executor_type 值域）。
const (
	WorkflowExecutorAgent = "agent"
	WorkflowExecutorSquad = "squad"
	WorkflowExecutorNone  = "none"
)

// WorkflowStageDef 流程阶段定义的形状（保存前校验用；纯值，无 IO）。
type WorkflowStageDef struct {
	Position      int
	Name          string
	ExitStatusKey string
	ExecutorType  string
	ExecutorID    string
	Instruction   string
}

// SortWorkflowStages 按 position 升序排序（保存前的规范化；调用方可先乱序传入）。
func SortWorkflowStages(defs []WorkflowStageDef) {
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].Position < defs[j].Position })
}

// ValidateWorkflowStages 校验整套阶段定义（保存时整体校验，纯函数无 IO）：
//   - 非空；
//   - position ≥1、无重复、连续（1..N，可乱序传入，保存时排序）；
//   - name 非空、无重复；
//   - executor_type ∈ {agent,squad,none}；agent/squad 时 executor_id 必填且为 uuid
//     形状；none 时 executor_id 必须为空（人工环节不绑执行者）；
//   - exit_status_key 非空（存在性/closed 校验在 service 层做——要查库）。
func ValidateWorkflowStages(defs []WorkflowStageDef) error {
	if len(defs) == 0 {
		return errors.New("workflow requires at least one stage")
	}
	positions := make(map[int]bool, len(defs))
	names := make(map[string]bool, len(defs))
	for i, d := range defs {
		if d.Position < 1 {
			return fmt.Errorf("stage %d: position must be >= 1", i+1)
		}
		if positions[d.Position] {
			return fmt.Errorf("stage %d: duplicate position %d", i+1, d.Position)
		}
		positions[d.Position] = true

		if d.Name == "" {
			return fmt.Errorf("stage %d: name required", i+1)
		}
		if names[d.Name] {
			return fmt.Errorf("stage %d: duplicate stage name %q", i+1, d.Name)
		}
		names[d.Name] = true

		if d.ExitStatusKey == "" {
			return fmt.Errorf("stage %d: exit_status_key required", i+1)
		}

		switch d.ExecutorType {
		case WorkflowExecutorAgent, WorkflowExecutorSquad:
			if _, ok := ParseUUID(d.ExecutorID); !ok {
				return fmt.Errorf("stage %d: executor_id must be a uuid for executor_type %s", i+1, d.ExecutorType)
			}
		case WorkflowExecutorNone:
			if d.ExecutorID != "" {
				return fmt.Errorf("stage %d: executor_id must be empty for executor_type none", i+1)
			}
		default:
			return fmt.Errorf("stage %d: executor_type must be agent|squad|none", i+1)
		}
	}
	// 连续性：position 集合必须恰好是 1..N（允许乱序，不允许断档）。
	for p := 1; p <= len(defs); p++ {
		if !positions[p] {
			return fmt.Errorf("positions must be contiguous 1..%d (missing %d)", len(defs), p)
		}
	}
	return nil
}
