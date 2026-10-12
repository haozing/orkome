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
		require.NoError(t, svc.UpsertWithSlots(ctx, "ws", "p", "L", "https://e.example", []PackUISlot{
			{Slot: SlotWorkItemDetailTab, Label: "x", URLTemplate: tpl, Height: 480},
		}), tpl)
	}
	for _, tpl := range bad {
		err := svc.UpsertWithSlots(ctx, "ws", "p", "L", "https://e.example", []PackUISlot{
			{Slot: SlotWorkItemDetailTab, Label: "x", URLTemplate: tpl, Height: 480},
		})
		require.Error(t, err, tpl)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
		// entry 模板同一纪律。
		err = svc.UpsertWithSlots(ctx, "ws", "p", "L", tpl, nil)
		require.Error(t, err, "entry "+tpl)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	}
}

// uiEntryFakeRepoStub 只为构造 service：合法模板会触达仓储（校验通过即落库），
// 其余方法永不调用。
type uiEntryFakeRepoStub struct {
	PackUIEntryRepo
}

func (f *uiEntryFakeRepoStub) UpsertPackUIEntry(context.Context, PackUIEntry) error { return nil }
