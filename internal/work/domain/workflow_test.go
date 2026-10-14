package domain

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateWorkflowStages(t *testing.T) {
	valid := WorkflowStageDef{
		Position: 1, Name: "需求澄清", ExitStatusKey: "clarified",
		ExecutorType: WorkflowExecutorAgent, ExecutorID: "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f",
		Instruction: "澄清需求",
	}
	agent := func(p int, id string) WorkflowStageDef {
		d := valid
		d.Position = p
		d.Name = fmt.Sprintf("stage-%d", p)
		d.ExecutorID = id
		return d
	}

	cases := []struct {
		name string
		defs []WorkflowStageDef
	}{
		{"empty", nil},
		{"position below one", []WorkflowStageDef{{Position: 0, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone}}},
		{"duplicate position", []WorkflowStageDef{
			{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone},
			{Position: 1, Name: "b", ExitStatusKey: "done", ExecutorType: WorkflowExecutorNone},
		}},
		{"position gap", []WorkflowStageDef{
			{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone},
			{Position: 3, Name: "b", ExitStatusKey: "done", ExecutorType: WorkflowExecutorNone},
		}},
		{"empty name", []WorkflowStageDef{{Position: 1, Name: "", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone}}},
		{"duplicate name", []WorkflowStageDef{
			{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone},
			{Position: 2, Name: "a", ExitStatusKey: "done", ExecutorType: WorkflowExecutorNone},
		}},
		{"empty exit_status_key", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "", ExecutorType: WorkflowExecutorNone}}},
		{"unknown executor_type", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: "robot"}}},
		{"agent without executor_id", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorAgent}}},
		{"squad without executor_id", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorSquad}}},
		{"agent with non-uuid executor_id", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorAgent, ExecutorID: "agent-1"}}},
		{"none with executor_id", []WorkflowStageDef{{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone, ExecutorID: "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWorkflowStages(tc.defs)
			assert.Error(t, err)
		})
	}

	t.Run("single none stage ok", func(t *testing.T) {
		assert.NoError(t, ValidateWorkflowStages([]WorkflowStageDef{
			{Position: 1, Name: "a", ExitStatusKey: "done", ExecutorType: WorkflowExecutorNone},
		}))
	})

	t.Run("out of order contiguous positions ok", func(t *testing.T) {
		assert.NoError(t, ValidateWorkflowStages([]WorkflowStageDef{
			agent(2, "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"),
			{Position: 1, Name: "a", ExitStatusKey: "todo", ExecutorType: WorkflowExecutorNone},
			{Position: 3, Name: "c", ExitStatusKey: "done", ExecutorType: WorkflowExecutorNone},
		}))
	})

	t.Run("uppercase uuid accepted", func(t *testing.T) {
		assert.NoError(t, ValidateWorkflowStages([]WorkflowStageDef{
			{Position: 1, Name: "a", ExitStatusKey: "done", ExecutorType: WorkflowExecutorSquad,
				ExecutorID: "0B7D1F5E-6B2A-4C9E-8D3F-1A2B3C4D5E6F"},
		}))
	})
}

func TestSortWorkflowStages(t *testing.T) {
	defs := []WorkflowStageDef{
		{Position: 3, Name: "c"}, {Position: 1, Name: "a"}, {Position: 2, Name: "b"},
	}
	SortWorkflowStages(defs)
	for i, d := range defs {
		assert.Equal(t, i+1, d.Position)
	}
}
