package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeToolExec 记录代调并按脚本返回。
type fakeToolExec struct {
	calls     int
	lastTool  string
	lastWS    string
	lastWI    string
	lastAgent string
	lastArgs  map[string]any
	resp      json.RawMessage
	respErr   error
}

func (f *fakeToolExec) Invoke(_ context.Context, ws, wi, agentID, tool string, args map[string]any) (json.RawMessage, error) {
	f.calls++
	f.lastWS, f.lastWI, f.lastAgent, f.lastTool, f.lastArgs = ws, wi, agentID, tool, args
	if f.respErr != nil {
		return nil, f.respErr
	}
	return f.resp, nil
}

func sourceBatch(source map[string]any) *Batch {
	return &Batch{
		ID: "b-1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Kind: "mcp_tool",
		Source: source,
		Params: map[string]any{"workspace_id": "ws-1", "work_item_id": "wi-1", "agent_id": "ag-1"},
	}
}

const attachmentsResp = `{"attachments":[
  {"file_id":"f-1","name":"a.png"},
  {"file_id":"f-2","name":"b.png"},
  {"file_id":"","name":"no-id.png"},
  {"name":"no-file-id.png"}
]}`

func TestMaterializeFromSource_HappyPath(t *testing.T) {
	exec := &fakeToolExec{resp: json.RawMessage(attachmentsResp)}
	b := sourceBatch(map[string]any{
		"source": map[string]any{
			"tool": "list_attachments",
			"args": map[string]any{"work_item_id": "{work_item_id}"},
			"pick": "$.attachments[*]",
		},
		"payload_template": map[string]any{
			"image_ref": "$.file_id",
			"lang":      "zh",
		},
		"required": []any{"image_ref"},
	})
	out, err := MaterializeFromSource(context.Background(), exec, b)
	require.NoError(t, err)

	assert.Equal(t, 4, out.SourceCount)
	assert.Equal(t, 2, out.Materialized)
	assert.Equal(t, 2, out.Skipped)
	require.Len(t, out.Payloads, 2)
	assert.Equal(t, map[string]any{"image_ref": "f-1", "lang": "zh"}, out.Payloads[0])
	assert.Equal(t, map[string]any{"image_ref": "f-2", "lang": "zh"}, out.Payloads[1])
	require.Len(t, out.Sample, 2) // 成品不足 3 条时 sample=全部成品
	require.Len(t, out.SkipReasons, 2)
	assert.Contains(t, out.SkipReasons[0], "image_ref")

	// 对账：source_count = materialized + skipped
	assert.Equal(t, out.SourceCount, out.Materialized+out.Skipped)

	// 代调用参数：占位符已替换、上下文透传
	assert.Equal(t, 1, exec.calls)
	assert.Equal(t, "list_attachments", exec.lastTool)
	assert.Equal(t, "ws-1", exec.lastWS)
	assert.Equal(t, "wi-1", exec.lastWI)
	assert.Equal(t, "ag-1", exec.lastAgent)
	assert.Equal(t, map[string]any{"work_item_id": "wi-1"}, exec.lastArgs)
}

func TestMaterializeFromSource_SampleCapsAt3(t *testing.T) {
	exec := &fakeToolExec{resp: json.RawMessage(`[
		{"file_id":"f-1"},{"file_id":"f-2"},{"file_id":"f-3"},{"file_id":"f-4"},{"file_id":"f-5"}
	]`)}
	b := sourceBatch(map[string]any{
		"source":           map[string]any{"tool": "list"}, // 缺省 pick = 结果本身是数组
		"payload_template": map[string]any{"image_ref": "$.file_id"},
	})
	out, err := MaterializeFromSource(context.Background(), exec, b)
	require.NoError(t, err)
	assert.Equal(t, 5, out.SourceCount)
	assert.Equal(t, 5, out.Materialized)
	assert.Equal(t, 0, out.Skipped)
	require.Len(t, out.Sample, 3)
	assert.Equal(t, "f-1", out.Sample[0]["image_ref"])
}

func TestMaterializeFromSource_StructuralFailures(t *testing.T) {
	src := map[string]any{
		"source":           map[string]any{"tool": "list", "pick": "$.data.items[*]"},
		"payload_template": map[string]any{"x": "$.a"},
	}
	t.Run("exec not wired", func(t *testing.T) {
		_, err := MaterializeFromSource(context.Background(), nil, sourceBatch(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ToolExec not wired")
	})
	t.Run("source.tool missing", func(t *testing.T) {
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"pick": "$.x"},
			"payload_template": map[string]any{"x": "1"},
		})
		_, err := MaterializeFromSource(context.Background(), &fakeToolExec{}, b)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source.tool required")
	})
	t.Run("tool invocation error", func(t *testing.T) {
		exec := &fakeToolExec{respErr: errors.New("boom")}
		_, err := MaterializeFromSource(context.Background(), exec, sourceBatch(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `source tool "list" failed`)
	})
	t.Run("non-JSON response", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage("not json")}
		_, err := MaterializeFromSource(context.Background(), exec, sourceBatch(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-JSON")
	})
	t.Run("pick matches nothing is fatal", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`{"other":[]}`)}
		_, err := MaterializeFromSource(context.Background(), exec, sourceBatch(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched nothing")
	})
	t.Run("unsupported path syntax is fatal", func(t *testing.T) {
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list", "pick": "items[0]"},
			"payload_template": map[string]any{"x": "1"},
		})
		exec := &fakeToolExec{resp: json.RawMessage(`{}`)}
		_, err := MaterializeFromSource(context.Background(), exec, b)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported path")
	})
	t.Run("missing payload_template", func(t *testing.T) {
		b := sourceBatch(map[string]any{
			"source": map[string]any{"tool": "list"},
		})
		exec := &fakeToolExec{resp: json.RawMessage(`[{}]`)}
		_, err := MaterializeFromSource(context.Background(), exec, b)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payload_template required")
	})
}

func TestMaterializeFromSource_TemplateAndRequired(t *testing.T) {
	t.Run("literal values pass through", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`[{"file_id":"f-1"}]`)}
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list"},
			"payload_template": map[string]any{"image_ref": "$.file_id", "lang": "zh", "n": 3},
		})
		out, err := MaterializeFromSource(context.Background(), exec, b)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"image_ref": "f-1", "lang": "zh", "n": 3}, out.Payloads[0])
	})
	t.Run("nested direct path", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`[{"meta":{"file":{"id":"f-9"}}}]`)}
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list"},
			"payload_template": map[string]any{"image_ref": "$.meta.file.id"},
		})
		out, err := MaterializeFromSource(context.Background(), exec, b)
		require.NoError(t, err)
		assert.Equal(t, "f-9", out.Payloads[0]["image_ref"])
	})
	t.Run("array field projection", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`{"items":[{"files":[{"id":"a"},{"id":"b"}]}]}`)}
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list", "pick": "$.items[*]"},
			"payload_template": map[string]any{"first_id": "$.files[*].id"},
		})
		out, err := MaterializeFromSource(context.Background(), exec, b)
		require.NoError(t, err)
		require.Len(t, out.Payloads, 1)
		// 投影出多个值 → 节点流数组原样进 payload（由消费方/工具契约解释）
		ids, ok := out.Payloads[0]["first_id"].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{"a", "b"}, ids)
	})
	t.Run("required empty string counts as missing", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`[{"file_id":"   "},{"file_id":"ok"}]`)}
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list"},
			"payload_template": map[string]any{"image_ref": "$.file_id"},
			"required":         []any{"image_ref"},
		})
		out, err := MaterializeFromSource(context.Background(), exec, b)
		require.NoError(t, err)
		assert.Equal(t, 1, out.Materialized)
		assert.Equal(t, 1, out.Skipped)
		assert.Equal(t, "ok", out.Payloads[0]["image_ref"])
	})
	t.Run("required absent key fails even without template ref", func(t *testing.T) {
		exec := &fakeToolExec{resp: json.RawMessage(`[{"a":"1"}]`)}
		b := sourceBatch(map[string]any{
			"source":           map[string]any{"tool": "list"},
			"payload_template": map[string]any{"a": "$.a"},
			"required":         []any{"nope"},
		})
		out, err := MaterializeFromSource(context.Background(), exec, b)
		require.NoError(t, err)
		assert.Equal(t, 0, out.Materialized)
		assert.Equal(t, 1, out.Skipped)
		assert.Contains(t, out.SkipReasons[0], "required field nope")
	})
}

func TestMaterializeFromSource_TokenReplacement(t *testing.T) {
	exec := &fakeToolExec{resp: json.RawMessage(`[]`)}
	b := sourceBatch(map[string]any{
		"source": map[string]any{
			"tool": "list_attachments",
			"args": map[string]any{
				"work_item_id": "{work_item_id}",
				"workspace":    "{workspace_id}",
				"agent":        "{agent_id}",
				"unknown":      "{keep_me}",
			},
		},
		"payload_template": map[string]any{"x": "1"},
	})
	_, err := MaterializeFromSource(context.Background(), exec, b)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"work_item_id": "wi-1",
		"workspace":    "ws-1",
		"agent":        "ag-1",
		"unknown":      "{keep_me}", // 未知占位符原样保留
	}, exec.lastArgs)
}
