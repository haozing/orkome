package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeV1SlotsPack 写一个带 ui.slots 的 pack/v1 pack（无 mcp.tools 声明段——
// fake server 全部自描述 outputSchema，可直过 B1 闸门）。slotURL 进 slots 模板。
func writeV1SlotsPack(t *testing.T, mcpURL, slotURL string) string {
	t.Helper()
	dir := t.TempDir()
	pack := "apiVersion: pack/v1\nname: fakeblog\nversion: 0.3.0\n" +
		"mcp:\n  - name: fakeblog\n    transport: http\n    url: " + mcpURL + "\n" +
		"ui:\n  entry:\n    label: 内容工厂\n    urlTemplate: https://fakeblog.example?ws={workspaceId}&t={uiToken}\n" +
		"  slots:\n    - slot: workitem.detail.tab\n      label: 文章预览\n" +
		"      urlTemplate: " + slotURL + "?item={workItemId}&t={uiToken}\n      height: 480\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(pack), 0o644))
	return dir
}

// fakeSlotServer 起一个静态 slot 页：csp 控制 Content-Security-Policy 响应头。
func fakeSlotServer(t *testing.T, csp string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if csp != "" {
			w.Header().Set("Content-Security-Policy", csp)
		}
		fmt.Fprintln(w, "<html><body>slot</body></html>")
	}))
	t.Cleanup(srv.Close)
	return srv
}

type capturedUIEntryPut struct {
	Seen  bool
	Slots []map[string]any
}

// newSlotsCaptureAPI 起 fake 平台 API：捕获 PUT /api/ui-entries/<pack> 的 body。
func newSlotsCaptureAPI(t *testing.T) (*apiClient, *capturedUIEntryPut) {
	t.Helper()
	var mu sync.Mutex
	cap := &capturedUIEntryPut{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answerProvisionContract(w, r) {
			return
		}
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/api/ui-entries/") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			cap.Seen = true
			if raw, ok := body["slots"].([]any); ok {
				cap.Slots = make([]map[string]any, 0, len(raw))
				for _, it := range raw {
					if m, ok := it.(map[string]any); ok {
						cap.Slots = append(cap.Slots, m)
					}
				}
			}
			mu.Unlock()
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(api.Close)
	return &apiClient{Base: api.URL, WS: "ws1", PAT: "tok", HC: &http.Client{Timeout: 5e9}}, cap
}

// selfDescribedJobServer 起三件套全自描述的 fake MCP server（过 B1 闸门）。
func selfDescribedJobServer(t *testing.T) *httptest.Server {
	t.Helper()
	return fakeBlogJobServer(t, [][2]string{
		{"fakeblog_submit_generation", "true"},
		{"fakeblog_job_status", "true"},
		{"fakeblog_cancel_job", "true"},
	})
}

// TestInstallSlotsArchive 验证 C2 主路径：v1 pack 的 ui.slots 随 entry 同一 PUT
// 落库（wire 形状 slot/label/url_template/height）；探测关闭（第一方豁免默认）。
func TestInstallSlotsArchive(t *testing.T) {
	fakeMCP := selfDescribedJobServer(t)
	slot := fakeSlotServer(t, "") // 无 CSP 头也装——第一方豁免（frame-origin 空）
	dir := writeV1SlotsPack(t, fakeMCP.URL, slot.URL)
	p, err := parsePack(dir)
	require.NoError(t, err)

	c, cap := newSlotsCaptureAPI(t)
	require.NoError(t, install(context.Background(), c, p, map[string]string{}, true, ""))

	require.True(t, cap.Seen, "PUT /api/ui-entries 已发出")
	require.Len(t, cap.Slots, 1, "slots 随行落库")
	s := cap.Slots[0]
	assert.Equal(t, "workitem.detail.tab", s["slot"])
	assert.Equal(t, "文章预览", s["label"])
	assert.Contains(t, s["url_template"], "{workItemId}")
	assert.EqualValues(t, 480, s["height"])
}

// TestInstallSlotsProbeReject 验证 frame-ancestors 探测：CSP 不含 orkome origin
// 的 slot 被剔除（pack 其余件照常装，entry 仍注册、slots 为空）。
func TestInstallSlotsProbeReject(t *testing.T) {
	fakeMCP := selfDescribedJobServer(t)
	slot := fakeSlotServer(t, "frame-ancestors 'self'")
	dir := writeV1SlotsPack(t, fakeMCP.URL, slot.URL)
	p, err := parsePack(dir)
	require.NoError(t, err)

	c, cap := newSlotsCaptureAPI(t)
	require.NoError(t, install(context.Background(), c, p, map[string]string{}, true, "https://orkome.example.com"),
		"不合规 slot 只剔除该 slot，install 整体成功")
	require.True(t, cap.Seen, "entry 照常注册")
	assert.Empty(t, cap.Slots, "不合规 slot 不落库")
}

// TestInstallSlotsProbePass 验证 CSP 含 origin 或通配的 slot 保留。
func TestInstallSlotsProbePass(t *testing.T) {
	for _, tc := range []struct {
		name, csp string
	}{
		{"explicit origin", "default-src 'none'; frame-ancestors https://orkome.example.com"},
		{"wildcard", "frame-ancestors *"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeMCP := selfDescribedJobServer(t)
			slot := fakeSlotServer(t, tc.csp)
			dir := writeV1SlotsPack(t, fakeMCP.URL, slot.URL)
			p, err := parsePack(dir)
			require.NoError(t, err)

			c, cap := newSlotsCaptureAPI(t)
			require.NoError(t, install(context.Background(), c, p, map[string]string{}, true, "https://orkome.example.com"))
			require.Len(t, cap.Slots, 1, "合规 slot 保留")
		})
	}
}

// TestInstallSlotsProbeFailureExempt 验证探测失败（网络错）的抉择：视为豁免——
// slot URL 探测不通不代表 CSP 不合规（可能是只对带票据请求开放），装上、前端有
// 深链兜底，不会因探测通道故障把合法 pack 挡在门外。
func TestInstallSlotsProbeFailureExempt(t *testing.T) {
	fakeMCP := selfDescribedJobServer(t)
	dir := writeV1SlotsPack(t, fakeMCP.URL, "http://127.0.0.1:1/unreachable")
	p, err := parsePack(dir)
	require.NoError(t, err)

	c, cap := newSlotsCaptureAPI(t)
	require.NoError(t, install(context.Background(), c, p, map[string]string{}, true, "https://orkome.example.com"))
	require.Len(t, cap.Slots, 1, "探测失败 → 豁免保留")
}

// TestInstallV0NoSlots 验证 v0 回归：slots 不落库（PUT body 无 slots 字段）。
func TestInstallV0NoSlots(t *testing.T) {
	fake := fakeBlogMCPServer(t, "fakeblog_get_post")
	dir := writePack(t, fake.URL)
	raw, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	require.NoError(t, err)
	raw = append(raw, []byte("ui:\n  entry:\n    label: x\n    urlTemplate: https://x.example\n  slots:\n    - slot: workitem.detail.tab\n      label: 文章预览\n      urlTemplate: https://x.example/slot\n      height: 480\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), raw, 0o644))
	p, err := parsePack(dir)
	require.NoError(t, err)
	require.Empty(t, p.UI.Slots, "v0 解析期已丢弃 slots")

	c, cap := newSlotsCaptureAPI(t)
	require.NoError(t, install(context.Background(), c, p, map[string]string{"FAKEBLOG_MCP_TOKEN": "tok"}, true, ""))
	require.True(t, cap.Seen)
	assert.Empty(t, cap.Slots, "v0 PUT body 不带 slots")
}

// TestProbeFrameAncestors 直接单测探测函数的判定规则。
func TestProbeFrameAncestors(t *testing.T) {
	const origin = "https://orkome.example.com"
	hc := &http.Client{Timeout: 5e9}
	ctx := context.Background()

	for _, tc := range []struct {
		name, csp string
		want      bool
	}{
		{"exact origin", "frame-ancestors " + origin, true},
		{"origin among multiple", "default-src 'none'; frame-ancestors 'self' " + origin, true},
		{"wildcard", "frame-ancestors *", true},
		{"self only", "frame-ancestors 'self'", false},
		{"other host", "frame-ancestors https://evil.example.com", false},
		{"no frame-ancestors directive", "default-src 'none'", false},
		{"no CSP header", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeSlotServer(t, tc.csp)
			got, err := probeFrameAncestors(ctx, srv.URL, origin, hc)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("template vars substituted before GET", func(t *testing.T) {
		srv := fakeSlotServer(t, "frame-ancestors "+origin)
		got, err := probeFrameAncestors(ctx, srv.URL+"/slot?item={workItemId}&t={uiToken}", origin, hc)
		require.NoError(t, err)
		assert.True(t, got, "模板变量替换成哑值后探测可达")
	})

	t.Run("network error returns err (caller decides)", func(t *testing.T) {
		_, err := probeFrameAncestors(ctx, "http://127.0.0.1:1/x", origin, hc)
		require.Error(t, err)
	})
}
