package app

import "time"

// WorkflowDefinition 项目流程定义（HTTP 形状按 docs/wf-interfaces-frozen.md §3）。
type WorkflowDefinition struct {
	ID                  string          `json:"id"`
	WorkspaceID         string          `json:"workspace_id,omitempty"`
	ProjectID           string          `json:"project_id"`
	Name                string          `json:"name"`
	TriggerMode         string          `json:"trigger_mode"`
	EntryStagePosition  int             `json:"entry_stage_position"`
	Enabled             bool            `json:"enabled"`
	CreatedBy           string          `json:"created_by"`
	Stages              []WorkflowStage `json:"stages"` // 按 position 升序
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// WorkflowStage 流程阶段（读面形状；executor_id 为空串表示 none）。
type WorkflowStage struct {
	ID            string `json:"id"`
	Position      int    `json:"position"`
	Name          string `json:"name"`
	ExitStatusKey string `json:"exit_status_key"`
	ExecutorType  string `json:"executor_type"`
	ExecutorID    string `json:"executor_id,omitempty"`
	Instruction   string `json:"instruction"`
}
