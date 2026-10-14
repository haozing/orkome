package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- 安装编排的四端口 fake：记录调用序（全局 log），可注入失败 ----

type installFakes struct {
	log  []string
	fail map[string]error // 步骤键 → 注入错误（"mcp[1]" / "event[0]" / ...）

	cred CreatedRec
	mcp  MCPCreateRec
	hook WebhookRec
	ui   UIRec
}

type CreatedRec struct{ calls []string } // ws/provider/secret/createdBy
type MCPCreateRec struct{ calls []string }
type WebhookRec struct {
	n      int
	ids    []string
	secret string
}
type UIRec struct{ calls []string }

func newInstallFakes() *installFakes {
	return &installFakes{fail: map[string]error{}, hook: WebhookRec{secret: "whsec_abc123"}}
}

func (f *installFakes) credsPort() InstallPorts {
	return InstallPorts{
		Credentials: credPortFake{f},
		MCP:         mcpPortFake{f},
		Webhooks:    hookPortFake{f},
		UIEntry:     uiPortFake{f},
	}
}

type credPortFake struct{ f *installFakes }

func (p credPortFake) Create(_ context.Context, ws, provider, secret, createdBy string) (string, error) {
	if err := p.f.fail["cred"]; err != nil {
		return "", err
	}
	p.f.log = append(p.f.log, "cred:"+ws+"/"+provider)
	p.f.cred.calls = append(p.f.cred.calls, ws+"|"+provider+"|"+secret+"|"+createdBy)
	return "cred-1", nil
}

type mcpPortFake struct{ f *installFakes }

func (p mcpPortFake) Create(_ context.Context, ws string, m InstallMCP, credID, packID string) error {
	// 失败键支持索引限定（"mcp[1]"）以测中途失败；裸 "mcp" 命中任意一次。
	if err := p.f.fail["mcp"]; err != nil {
		return err
	}
	key := "mcp[" + string(rune('0'+len(p.f.mcp.calls))) + "]"
	if err := p.f.fail[key]; err != nil {
		return err
	}
	p.f.log = append(p.f.log, "mcp:"+m.Name)
	p.f.mcp.calls = append(p.f.mcp.calls, ws+"|"+m.Name+"|"+credID+"|"+packID)
	return nil
}

type hookPortFake struct{ f *installFakes }

func (p hookPortFake) CreateSubscription(_ context.Context, ws, url, desc string, types []string) (string, string, error) {
	if err := p.f.fail["hook"]; err != nil {
		return "", "", err
	}
	p.f.hook.n++
	id := "sub-" + string(rune('0'+p.f.hook.n))
	p.f.log = append(p.f.log, "hook:"+url)
	p.f.hook.ids = append(p.f.hook.ids, id)
	return id, p.f.hook.secret, nil
}

type uiPortFake struct{ f *installFakes }

func (p uiPortFake) UpsertWithSlots(_ context.Context, ws, pack, packID, label, urlTemplate string, slots []UISlotInfo) error {
	if err := p.f.fail["ui"]; err != nil {
		return err
	}
	p.f.log = append(p.f.log, "ui:"+pack)
	p.f.ui.calls = append(p.f.ui.calls, ws+"|"+pack+"|"+packID+"|"+label+"|"+urlTemplate)
	return nil
}

func fullSpec() InstallSpec {
	return InstallSpec{
		Name: "agentblog", Version: "1.0.0",
		MCP: []InstallMCP{
			{Name: "agentblog", Transport: "streamable_http", URL: "https://mcp.agentblog.dev",
				Credential: &InstallCredential{Provider: "agentblog", Secret: "sk-secret"}},
			{Name: "agentblog-files", Transport: "streamable_http", URL: "https://files.agentblog.dev"},
		},
		Events: []InstallEvent{
			{URL: "https://vendor.example/hooks", Types: []string{"task:completed"}, Description: "安装回调"},
		},
		UI: &InstallUI{Label: "AgentBlog", URLTemplate: "https://app.vendor.example/ui/{uiToken}/{workspaceId}",
			Slots: []InstallUISlot{{Slot: "workitem.detail.tab", Label: "联动", URLTemplate: "https://app.vendor.example/tab", Height: 400}}},
	}
}

func TestInstallHappyPath(t *testing.T) {
	f := newInstallFakes()
	repo := newFakeRepo()
	subs := &fakeSubs{}
	svc := NewPackService(repo, Ports{Subs: subs}).WithInstallPorts(f.credsPort())

	res, err := svc.Install(context.Background(), "ws-1", "user-9", fullSpec())
	require.NoError(t, err)

	// 定序：pack → cred → mcp[0] → mcp[1] → hook → ui
	require.Equal(t, []string{
		"cred:ws-1/agentblog", "mcp:agentblog", "mcp:agentblog-files", "hook:https://vendor.example/hooks", "ui:agentblog",
	}, f.log)

	require.Equal(t, "ws-1|agentblog|sk-secret|user-9", f.cred.calls[0], "credential createdBy=userID")
	require.Equal(t, "ws-1|agentblog|cred-1|pack-a", f.mcp.calls[0], "MCP 带凭证 id 与 pack id")
	require.Equal(t, "ws-1|agentblog-files||pack-a", f.mcp.calls[1], "无凭证 MCP credID 空串")
	require.Equal(t, "ws-1|agentblog|pack-a|AgentBlog|https://app.vendor.example/ui/{uiToken}/{workspaceId}", f.ui.calls[0], "UI 进驻带 pack 名与 pack id")

	// 关联表写入 + 回执
	require.Len(t, res.Subscriptions, 1)
	require.Equal(t, [][2]string{{"pack-a", "sub-1"}}, subs.linked)
	require.Equal(t, "https://vendor.example/hooks", res.Subscriptions[0].URL)
	require.Equal(t, "sub-1", res.Subscriptions[0].SubscriptionID)
	require.Equal(t, "whsec_abc123", res.Subscriptions[0].Secret, "secret 一次性全量回显")
	require.Equal(t, "agentblog", res.Pack.Name)
}

func TestInstallLinksSubscriptions(t *testing.T) {
	f := newInstallFakes()
	repo := newFakeRepo()
	subs := &fakeSubs{}
	svc := NewPackService(repo, Ports{Subs: subs}).WithInstallPorts(f.credsPort())

	res, err := svc.Install(context.Background(), "ws-1", "u", fullSpec())
	require.NoError(t, err)
	require.Len(t, res.Subscriptions, 1)
	require.Equal(t, [][2]string{{"pack-a", "sub-1"}}, subs.linked, "订阅建后必须 Link 进连接表")
}

func TestInstallFailFastAtSecondMCP(t *testing.T) {
	f := newInstallFakes()
	f.fail["mcp[1]"] = errors.New("boom")
	repo := newFakeRepo()
	subs := &fakeSubs{}
	svc := NewPackService(repo, Ports{Subs: subs}).WithInstallPorts(f.credsPort())

	_, err := svc.Install(context.Background(), "ws-1", "u", fullSpec())
	require.Error(t, err)
	require.Contains(t, err.Error(), "step mcp[1]")
	// fail-fast：第二步失败后事件与 UI 不再执行
	require.Equal(t, []string{"cred:ws-1/agentblog", "mcp:agentblog"}, f.log)
	require.Empty(t, subs.linked)
}

func TestInstallFailFastAtEvent(t *testing.T) {
	f := newInstallFakes()
	f.fail["hook"] = errors.New("vendor down")
	repo := newFakeRepo()
	svc := NewPackService(repo, Ports{}).WithInstallPorts(f.credsPort())

	_, err := svc.Install(context.Background(), "ws-1", "u", fullSpec())
	require.Error(t, err)
	require.Contains(t, err.Error(), "step event[0]")
	require.NotContains(t, f.log, "ui:agentblog", "订阅失败后 UI 不进驻")
}

func TestInstallNameRequired(t *testing.T) {
	svc := NewPackService(newFakeRepo(), Ports{}).WithInstallPorts(newInstallFakes().credsPort())
	_, err := svc.Install(context.Background(), "ws-1", "u", InstallSpec{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "name required")
}

func TestInstallValidationErrors(t *testing.T) {
	f := newInstallFakes()
	svc := NewPackService(newFakeRepo(), Ports{}).WithInstallPorts(f.credsPort())
	ctx := context.Background()

	// transport 必填（不给服务端默认）
	_, err := svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", MCP: []InstallMCP{{Name: "m", URL: "https://x.dev"}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "transport required")

	// 非绝对 URL 拒绝
	_, err = svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", MCP: []InstallMCP{{Name: "m", Transport: "streamable_http", URL: "/relative"}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute http(s)")

	// UI 模板校验
	_, err = svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", UI: &InstallUI{Label: "L", URLTemplate: "javascript:alert(1)"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "step ui")
}

func TestInstallNotWiredPorts(t *testing.T) {
	repo := newFakeRepo()
	svc := NewPackService(repo, Ports{}) // 不注入任何 install 端口
	ctx := context.Background()

	_, err := svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", MCP: []InstallMCP{{Name: "m", Transport: "streamable_http", URL: "https://x.dev"}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mcp port not wired")

	_, err = svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", Events: []InstallEvent{{URL: "https://v.example/h"}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "webhooks port not wired")

	_, err = svc.Install(ctx, "ws-1", "u", InstallSpec{Name: "p", UI: &InstallUI{Label: "L", URLTemplate: "https://x.dev/ui"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ui entry port not wired")
}
