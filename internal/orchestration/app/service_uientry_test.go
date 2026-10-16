package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

// FP-15 P1-3 服务层锚：url_template 必须是绝对 http(s) URL（http 层矩阵见
// adapters/http/handlers_uientry_test.go——同一规则在 PUT wire 上复核）。
func TestValidateSlotsURLTemplateScheme(t *testing.T) {
	ok := []string{
		"https://pack.example/slot?item={workItemId}&t={uiToken}",
		"http://127.0.0.1:8080/preview",
	}
	bad := []string{
		`javascript:alert(document.domain)`,
		"data:text/html,<script>alert(1)</script>",
		"/packs/github?wi={workItemId}",
		"//evil.example/slot",
		"ftp://files.example/slot",
		"https:///no-host",
	}

	fake := &uiEntryFakeRepoStub{}
	svc := NewPackUIEntryService(fake)
	ctx := context.Background()

	for _, tpl := range ok {
		require.NoError(t, svc.UpsertWithSlots(ctx, "ws", "p", "pack-1", "L", "https://e.example", []PackUISlot{
			{Slot: SlotWorkItemDetailTab, Label: "x", URLTemplate: tpl, Height: 480},
		}), tpl)
	}
	for _, tpl := range bad {
		err := svc.UpsertWithSlots(ctx, "ws", "p", "pack-1", "L", "https://e.example", []PackUISlot{
			{Slot: SlotWorkItemDetailTab, Label: "x", URLTemplate: tpl, Height: 480},
		})
		require.Error(t, err, tpl)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
		// entry 模板同一纪律。
		err = svc.UpsertWithSlots(ctx, "ws", "p", "pack-1", "L", tpl, nil)
		require.Error(t, err, "entry "+tpl)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	}
}

// 集成页终态 W1（docs/integrations-page-design-2026-10-15.md §2.3）：手动入口
// packID 为空放行（repo 侧落 NULL 并按 (workspace_id, pack) 部分唯一键覆盖，
// 迁移 1047）；ws/pack/label/url_template 必填不放行。
func TestUpsertWithSlotsManualEntry(t *testing.T) {
	fake := &uiEntryFakeRepoStub{}
	svc := NewPackUIEntryService(fake)
	ctx := context.Background()

	// packID 空 = 手动入口：校验通过、原样透传（不伪造 pack_id）。
	require.NoError(t, svc.UpsertWithSlots(ctx, "ws", "custom-side", "", "侧栏入口", "https://s.example", nil))
	require.Len(t, fake.stored, 1)
	assert.Equal(t, "", fake.stored[0].PackID)

	// 手动入口同样可带 slots（tab 型）。
	require.NoError(t, svc.UpsertWithSlots(ctx, "ws", "custom-tab", "", "工单 Tab", "https://t.example", []PackUISlot{
		{Slot: SlotWorkItemDetailTab, Label: "预览", URLTemplate: "https://t.example/slot?item={workItemId}", Height: 480},
	}))
	require.Len(t, fake.stored, 2)
	assert.Len(t, fake.stored[1].Slots, 1)

	// 其余必填不变：任一为空 → Validation，且不触达仓储。
	for _, tc := range []struct{ name, ws, pack, label, tpl string }{
		{"empty workspace", "", "custom-x", "L", "https://x.example"},
		{"empty pack", "ws", "", "L", "https://x.example"},
		{"empty label", "ws", "custom-x", "", "https://x.example"},
		{"empty url_template", "ws", "custom-x", "L", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpsertWithSlots(ctx, tc.ws, tc.pack, "", tc.label, tc.tpl, nil)
			require.Error(t, err)
			assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
		})
	}
	require.Len(t, fake.stored, 2, "被拒请求不落库")
}

// uiEntryFakeRepoStub 只为构造 service：合法模板会触达仓储（校验通过即落库），
// 其余方法永不调用。stored 记录落库行供手动入口用例断言。
type uiEntryFakeRepoStub struct {
	PackUIEntryRepo
	stored []PackUIEntry
}

func (f *uiEntryFakeRepoStub) UpsertPackUIEntry(_ context.Context, e PackUIEntry) error {
	f.stored = append(f.stored, e)
	return nil
}
