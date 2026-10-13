package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeRepo 是 pack 表的内存实现：记录 Upsert 调用并支持按 (ws,name) 幂等。
type fakeRepo struct {
	packs map[string]Pack // key: ws+"/"+name，模拟 UNIQUE(workspace_id, name)
	byID  map[string]Pack
	seq   int

	deleted []string
	nextErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{packs: map[string]Pack{}, byID: map[string]Pack{}}
}

func (f *fakeRepo) Upsert(_ context.Context, ws, name, version string) (Pack, error) {
	if f.nextErr != nil {
		return Pack{}, f.nextErr
	}
	key := ws + "/" + name
	if p, ok := f.packs[key]; ok {
		p.Version = version // 重装覆盖 version，ID/installed_at 不变（升级语义）
		f.packs[key] = p
		f.byID[p.ID] = p
		return p, nil
	}
	f.seq++
	p := Pack{ID: "pack-" + string(rune('a'+f.seq-1)), WorkspaceID: ws, Name: name, Version: version,
		InstalledAt: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}
	f.packs[key] = p
	f.byID[p.ID] = p
	return p, nil
}

func (f *fakeRepo) GetByID(_ context.Context, ws, packID string) (Pack, bool, error) {
	if f.nextErr != nil {
		return Pack{}, false, f.nextErr
	}
	p, ok := f.byID[packID]
	if !ok || p.WorkspaceID != ws {
		return Pack{}, false, nil
	}
	return p, true, nil
}

func (f *fakeRepo) Delete(_ context.Context, ws, packID string) error {
	if f.nextErr != nil {
		return f.nextErr
	}
	p, ok := f.byID[packID]
	if !ok || p.WorkspaceID != ws {
		return errNotFound
	}
	delete(f.byID, packID)
	delete(f.packs, ws+"/"+p.Name)
	f.deleted = append(f.deleted, packID)
	return nil
}

func (f *fakeRepo) List(_ context.Context, ws string) ([]Pack, error) {
	if f.nextErr != nil {
		return nil, f.nextErr
	}
	var out []Pack
	for _, p := range f.byID {
		if p.WorkspaceID == ws {
			out = append(out, p)
		}
	}
	return out, nil
}

// fake 读写端口：可注入区块数据与错误。
type fakeMCP struct{ refs []MCPServerRef; err error; calls int }
func (f *fakeMCP) ListByPack(context.Context, string, string) ([]MCPServerRef, error) {
	f.calls++
	return f.refs, f.err
}

type fakeTools struct{ refs []ToolSchemaRef; err error }
func (f *fakeTools) ListByPack(context.Context, string, string) ([]ToolSchemaRef, error) {
	return f.refs, f.err
}

type fakeAgents struct{ refs []AgentRef }
func (f *fakeAgents) ListByPack(context.Context, string, string) ([]AgentRef, error) {
	return f.refs, nil
}

type fakeSkills struct{ refs []SkillRef }
func (f *fakeSkills) ListByPack(context.Context, string, string) ([]SkillRef, error) {
	return f.refs, nil
}

type fakeUI struct{ entry *UIEntryInfo; err error }
func (f *fakeUI) GetByPack(context.Context, string, string) (*UIEntryInfo, error) {
	return f.entry, f.err
}

type fakeSubs struct {
	linked  [][2]string // packID, subscriptionID
	unlinked []string
	refs    []SubRef
}
func (f *fakeSubs) Link(_ context.Context, packID, subID string) error {
	f.linked = append(f.linked, [2]string{packID, subID})
	return nil
}
func (f *fakeSubs) UnlinkAll(_ context.Context, packID string) error {
	f.unlinked = append(f.unlinked, packID)
	return nil
}
func (f *fakeSubs) ListByPack(context.Context, string, string) ([]SubRef, error) {
	return f.refs, nil
}

var errNotFound = errCode{"E_NOT_FOUND", "pack not found"}

// errCode 模拟 apierr.Error 的形状（服务层透传 repo 错误，http 层按 Code 映射状态码）。
type errCode struct{ code, msg string }

func (e errCode) Error() string     { return e.code + ": " + e.msg }
func (e errCode) ErrorCode() string { return e.code }

func notFoundErr() error { return errNotFound }

func testPorts(m *fakeMCP, t *fakeTools, a *fakeAgents, s *fakeSkills, u *fakeUI, sub *fakeSubs) Ports {
	return Ports{MCP: m, Tools: t, Agents: a, Skills: s, UIEntry: u, Subs: sub}
}

func TestUpsertIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewPackService(repo, Ports{})

	p1, err := svc.Upsert(ctx, "ws1", "agentblog", "0.1.0")
	require.NoError(t, err)
	require.Equal(t, "agentblog", p1.Name)
	require.Equal(t, "0.1.0", p1.Version)
	require.NotEmpty(t, p1.ID)

	// 重装（同名同 workspace）：ID 与 installed_at 不变，仅 version 覆盖——升级语义。
	p2, err := svc.Upsert(ctx, "ws1", "agentblog", "0.2.0")
	require.NoError(t, err)
	require.Equal(t, p1.ID, p2.ID)
	require.Equal(t, p1.InstalledAt, p2.InstalledAt)
	require.Equal(t, "0.2.0", p2.Version)
	require.Len(t, repo.packs, 1, "upsert must not create a second row")

	// 同名不同 workspace 互不干扰（UNIQUE 是 (workspace_id, name) 二元组）。
	p3, err := svc.Upsert(ctx, "ws2", "agentblog", "0.1.0")
	require.NoError(t, err)
	require.NotEqual(t, p1.ID, p3.ID)

	// name 必填（E_VALIDATION）。
	_, err = svc.Upsert(ctx, "ws1", "", "0.1.0")
	require.Error(t, err)
	var ae interface{ ErrorCode() string }
	require.True(t, errors.As(err, &ae), "validation error must carry apierr code")
	require.Equal(t, "E_VALIDATION", ae.ErrorCode())
}

func TestGetAggregatesAllBlocks(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	mcp := &fakeMCP{refs: []MCPServerRef{{Name: "agentblog-mcp", Transport: "http", URL: "https://mcp.example", HasCreds: true}}}
	tools := &fakeTools{refs: []ToolSchemaRef{{Server: "agentblog-mcp", ToolName: "agentblog_publish", Source: "protocol"}}}
	agents := &fakeAgents{refs: []AgentRef{{ID: "a1", Name: "blogger", Archived: false}}}
	skills := &fakeSkills{refs: []SkillRef{{ID: "s1", Name: "write-post"}}}
	ui := &fakeUI{entry: &UIEntryInfo{ID: "e1", Label: "Blog console", URLTemplate: "https://app.example?t={uiToken}",
		Slots: []UISlotInfo{{Slot: "workitem.detail.tab", Label: "Article preview", URLTemplate: "https://app.example/{workItemId}", Height: 480}}}}
	subs := &fakeSubs{refs: []SubRef{{SubscriptionID: "sub-1", URL: "https://hooks.example", EventTypes: []string{"work_item.completed"}, IsActive: true}}}

	svc := NewPackService(repo, testPorts(mcp, tools, agents, skills, ui, subs))
	p, err := svc.Upsert(ctx, "ws1", "agentblog", "0.1.0")
	require.NoError(t, err)

	d, err := svc.Get(ctx, "ws1", p.ID)
	require.NoError(t, err)
	require.Equal(t, "agentblog", d.Name)
	require.Len(t, d.Servers, 1)
	require.True(t, d.Servers[0].HasCreds)
	require.Len(t, d.ToolSchemas, 1)
	require.Equal(t, "protocol", d.ToolSchemas[0].Source)
	require.Len(t, d.Agents, 1)
	require.Len(t, d.Skills, 1)
	require.NotNil(t, d.UIEntry)
	require.Len(t, d.UIEntry.Slots, 1)
	require.Equal(t, "workitem.detail.tab", d.UIEntry.Slots[0].Slot)
	require.Len(t, d.Subscriptions, 1)
	require.True(t, d.Subscriptions[0].IsActive)

	// Get 未命中 → NotFound（跨 workspace 取别人的 pack 同样 404）。
	_, err = svc.Get(ctx, "ws2", p.ID)
	require.Error(t, err)
	code := ""
	var coder interface{ ErrorCode() string }
	require.True(t, errors.As(err, &coder))
	code = coder.ErrorCode()
	require.Equal(t, "E_NOT_FOUND", code)

	// 区块端口为 nil（未接线）→ 空切片不报错（区块可独立缺失）。
	empty := NewPackService(repo, Ports{})
	d2, err := empty.Get(ctx, "ws1", p.ID)
	require.NoError(t, err)
	require.Empty(t, d2.Servers)
	require.Empty(t, d2.ToolSchemas)
	require.Empty(t, d2.Subscriptions)
	require.Nil(t, d2.UIEntry)
}

func TestListCarriesAssetCounts(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	mcp := &fakeMCP{refs: []MCPServerRef{{Name: "s1"}, {Name: "s2"}}}
	agents := &fakeAgents{refs: []AgentRef{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}}}
	skills := &fakeSkills{refs: []SkillRef{{ID: "sk1"}}}

	svc := NewPackService(repo, Ports{MCP: mcp, Agents: agents, Skills: skills})
	_, err := svc.Upsert(ctx, "ws1", "agentblog", "0.1.0")
	require.NoError(t, err)
	_, err = svc.Upsert(ctx, "ws1", "otherpack", "1.0.0")
	require.NoError(t, err)

	list, err := svc.List(ctx, "ws1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, sum := range list {
		require.Equal(t, 2, sum.ServerCount)
		require.Equal(t, 3, sum.AgentCount)
		require.Equal(t, 1, sum.SkillCount)
	}
	require.Equal(t, 2, mcp.calls, "count per pack row")
}

func TestDeleteRemovesPackRowOnly(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	subs := &fakeSubs{}
	svc := NewPackService(repo, testPorts(nil, nil, nil, nil, nil, subs))
	p, err := svc.Upsert(ctx, "ws1", "agentblog", "0.1.0")
	require.NoError(t, err)

	// P1 最小语义：只删 pack 行；派生物 CASCADE / 资产 SET NULL 全交给 FK，
	// 服务层不做显式资产清理（unsub 也是 P2 uninstall 的显式动作）。
	require.NoError(t, svc.Delete(ctx, "ws1", p.ID))
	require.Len(t, repo.deleted, 1)
	require.Empty(t, subs.unlinked, "P1 delete must not touch subscriptions itself")

	_, ok, err := repo.GetByID(ctx, "ws1", p.ID)
	require.NoError(t, err)
	require.False(t, ok)

	// 再删 → NotFound。
	err = svc.Delete(ctx, "ws1", p.ID)
	require.Error(t, err)
	var coder interface{ ErrorCode() string }
	require.True(t, errors.As(err, &coder))
	require.Equal(t, "E_NOT_FOUND", coder.ErrorCode())

	// 跨 workspace 删别人的 pack → NotFound（workspace 限定生效）。
	p2, err := svc.Upsert(ctx, "ws2", "agentblog", "0.1.0")
	require.NoError(t, err)
	err = svc.Delete(ctx, "ws1", p2.ID)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "not found"))
}
