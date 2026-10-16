package main

import "testing"

// 配置面纯函数的钉子（addConfigTools 的辅助层；MCP 工具本体靠真机 E2E 验收）。

func TestSlugifyMe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"我的控制台", "custom-entry-"},       // 全非 ASCII → 时间戳回退前缀
		{"Vendor Console", "custom-vendor-console"},
		{"Retail  POS!!", "custom-retail-pos"},
		{"--Edge--", "custom-edge"},
	}
	for _, c := range cases {
		got := slugifyMe(c.in)
		if c.want == "custom-entry-" {
			if len(got) < len("custom-entry-") || got[:len("custom-entry-")] != "custom-entry-" {
				t.Errorf("slugifyMe(%q) = %q, want prefix custom-entry-", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("slugifyMe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 长名限长（60 + custom- 前缀）
	long := slugifyMe("abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz")
	if len(long) > 67 {
		t.Errorf("slugifyMe long = %d chars, want <= 67", len(long))
	}
}

func TestValidAbsHTTPMe(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://vendor.example.com/console?ws={workspaceId}&t={uiToken}", true},
		{"http://127.0.0.1:8091/hooks", true},
		{"javascript:alert(1)", false},
		{"data:text/html,x", false},
		{"//proto-relative/x", false},
		{"no-scheme.example.com", false},
		{"ftp://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := validAbsHTTPMe(c.in); got != c.want {
			t.Errorf("validAbsHTTPMe(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
