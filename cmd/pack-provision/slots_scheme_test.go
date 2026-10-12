package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FP-15 P1-3：url_template scheme 校验（provision 侧）——模板必须是绝对
// http(s) URL。相对路径会把 slot 渲染成同源 iframe（sandbox 的
// allow-scripts+allow-same-origin 对同源内容形同虚设），javascript:/data:
// 模板在 iframe src 继承父源执行 = 确定的父 DOM 接管。违反即 fail the install。

func writeUIPack(t *testing.T, apiVersion, entryTemplate, slotTemplate string) string {
	t.Helper()
	dir := t.TempDir()
	pack := "apiVersion: " + apiVersion + "\nname: fakeblog\nversion: 0.1.0\n" +
		"ui:\n  entry:\n    label: x\n    urlTemplate: " + entryTemplate + "\n"
	if slotTemplate != "" {
		pack += "  slots:\n    - slot: workitem.detail.tab\n      label: 预览\n" +
			"      urlTemplate: " + slotTemplate + "\n      height: 480\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(pack), 0o644))
	return dir
}

// TestValidateAbsHTTPURL 纯函数矩阵：https 过 / javascript: 拒 / 相对拒 /
// ftp 拒 / 协议相对拒 / data: 拒 / host 缺失拒。
func TestValidateAbsHTTPURL(t *testing.T) {
	for _, tc := range []struct {
		name, template string
		wantOK         bool
	}{
		{"https absolute", "https://pack.example/console?wi={workItemId}", true},
		{"http absolute", "http://127.0.0.1:8080/preview", true},
		{"javascript scheme", `javascript:alert(document.domain)`, false},
		{"data scheme", "data:text/html,<script>alert(1)</script>", false},
		{"relative path", "/packs/github?wi={id}", false},
		{"bare word", "packs/github", false},
		{"protocol-relative", "//evil.example/slot", false},
		{"ftp scheme", "ftp://files.example/slot", false},
		{"scheme but no host", "https:///path/only", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAbsHTTPURL(tc.template)
			if tc.wantOK {
				assert.NoError(t, err, tc.template)
			} else {
				require.Error(t, err, tc.template)
				assert.Contains(t, err.Error(), "http(s)")
			}
		})
	}
}

// TestParsePackSlotTemplateScheme v1 slots 模板矩阵：违规模板 fail the install
//（解析期即拒，早于任何网络交互）。
func TestParsePackSlotTemplateScheme(t *testing.T) {
	for _, tc := range []struct {
		name, template string
		wantOK         bool
	}{
		{"https slot passes", "https://pack.example/slot?item={workItemId}&t={uiToken}", true},
		{"http slot passes", "http://127.0.0.1:9999/slot", true},
		{"javascript slot rejected", `javascript:alert(1)`, false},
		{"relative slot rejected", "/packs/github?wi={workItemId}", false},
		{"ftp slot rejected", "ftp://files.example/slot", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeUIPack(t, "pack/v1", "https://pack.example/console", tc.template)
			p, err := parsePack(dir)
			if tc.wantOK {
				require.NoError(t, err)
				require.Len(t, p.UI.Slots, 1)
			} else {
				require.Error(t, err, tc.template)
				assert.Contains(t, err.Error(), "http(s)")
				assert.Contains(t, err.Error(), "ui.slots")
			}
		})
	}
}

// TestParsePackEntryTemplateScheme entry 模板同一纪律，v0/v1 都查（entry 模板
// 走 PackEntries 侧栏 window.open，javascript: 同样直通）。
func TestParsePackEntryTemplateScheme(t *testing.T) {
	for _, tc := range []struct {
		name, version, template string
		wantOK                  bool
	}{
		{"v0 https entry passes", "pack/v0", "https://pack.example/console", true},
		{"v0 javascript entry rejected", "pack/v0", `javascript:alert(1)`, false},
		{"v0 relative entry rejected", "pack/v0", "/packs/x", false},
		{"v1 ftp entry rejected", "pack/v1", "ftp://files.example/console", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeUIPack(t, tc.version, tc.template, "")
			_, err := parsePack(dir)
			if tc.wantOK {
				require.NoError(t, err)
			} else {
				require.Error(t, err, tc.template)
				assert.Contains(t, err.Error(), "http(s)")
				assert.Contains(t, err.Error(), "ui.entry")
			}
		})
	}
}
