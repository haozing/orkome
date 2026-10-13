package app

import (
	"context"
	"net/url"
	"time"

	"orkome/internal/contract/apierr"
)

type PackUIEntry struct {
	ID          string       `json:"id"`
	WorkspaceID string       `json:"workspace_id"`
	Pack        string       `json:"pack"`
	PackID      string       `json:"pack_id,omitempty"` // 迁移 1042：FK→pack；空=旧数据（v0 无兼容）
	Label       string       `json:"label"`
	URLTemplate string       `json:"url_template"`
	Slots       []PackUISlot `json:"slots"`
	InstalledAt time.Time    `json:"installed_at"`
}

// PackUISlot 是 pack.yaml ui.slots[] 的落库形状（迁移 1036 的 jsonb 元素）。
// v0 slot 目录冻结：slot 标识只认 workitem.detail.tab（前端设计 §1.4）。
type PackUISlot struct {
	Slot        string `json:"slot"`
	Label       string `json:"label"`
	URLTemplate string `json:"url_template"`
	Height      int    `json:"height"`
}

// SlotWorkItemDetailTab 是 v0 唯一放行的 slot 标识（目录冻结，防概念漂移）。
const SlotWorkItemDetailTab = "workitem.detail.tab"

type PackUIEntryRepo interface {
	UpsertPackUIEntry(ctx context.Context, e PackUIEntry) error
	ListPackUIEntries(ctx context.Context, ws string) ([]PackUIEntry, error)
	DeletePackUIEntry(ctx context.Context, ws, pack string) error
}

type PackUIEntryService struct{ repo PackUIEntryRepo }

func NewPackUIEntryService(repo PackUIEntryRepo) *PackUIEntryService {
	return &PackUIEntryService{repo: repo}
}

func (s *PackUIEntryService) Upsert(ctx context.Context, ws, pack, label, urlTemplate string) error {
	return s.UpsertWithSlots(ctx, ws, pack, "", label, urlTemplate, nil)
}

// UpsertWithSlots 与 entry 同一 PUT 路径：slots 随行落库（nil = 无 slots，覆盖旧值）。
// packID 非空时按 (workspace_id, pack_id) 冲突写（迁移 1042 的一等公民口径；
// provision 第 0 步先 upsert pack 拿 id 再传此）——空 packID 拒绝，防漂回字符串键。
func (s *PackUIEntryService) UpsertWithSlots(ctx context.Context, ws, pack, packID, label, urlTemplate string, slots []PackUISlot) error {
	if ws == "" || pack == "" || packID == "" || label == "" || urlTemplate == "" {
		return apierr.NewValidation("pack, pack_id, label and url_template required")
	}
	if err := validateURLTemplate("url_template", "", urlTemplate); err != nil {
		return err
	}
	if err := validateSlots(slots); err != nil {
		return err
	}
	return s.repo.UpsertPackUIEntry(ctx, PackUIEntry{WorkspaceID: ws, Pack: pack, PackID: packID, Label: label, URLTemplate: urlTemplate, Slots: slots})
}

func (s *PackUIEntryService) List(ctx context.Context, ws string) ([]PackUIEntry, error) {
	return s.repo.ListPackUIEntries(ctx, ws)
}

func (s *PackUIEntryService) Delete(ctx context.Context, ws, pack string) error {
	return s.repo.DeletePackUIEntry(ctx, ws, pack)
}

// validateSlots：v0 目录冻结——slot 标识只认 workitem.detail.tab，其他值拒绝；
// label/url_template 必填（label 空由 provision 侧回退 pack 名，这里兜底）。
func validateSlots(slots []PackUISlot) error {
	for _, s := range slots {
		if s.Slot != SlotWorkItemDetailTab {
			return apierr.NewValidation("unknown slot " + s.Slot + " (v0 catalog frozen: workitem.detail.tab only)")
		}
		if s.Label == "" || s.URLTemplate == "" {
			return apierr.NewValidation("slot label and url_template required")
		}
		if err := validateURLTemplate("slot url_template", s.Slot, s.URLTemplate); err != nil {
			return err
		}
		if s.Height <= 0 || s.Height > 4096 {
			return apierr.NewValidation("slot height must be in (0, 4096] px")
		}
	}
	return nil
}

// validateURLTemplate 强制 url_template 为绝对 http(s) URL（FP-15 P1-3）：
// 相对路径模板会把 slot 渲染成同源 iframe（sandbox 的
// allow-scripts+allow-same-origin 组合对同源内容形同虚设），javascript:/data:
// 模板在 iframe src 继承父源执行 = 确定的 XSS。解析后 scheme 必须 http/https
// 且 host 非空——协议相对（//host/x）与其他 scheme 一律拒绝。
func validateURLTemplate(field, slot, template string) error {
	u, err := url.Parse(template)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		msg := field + " must be an absolute http(s) URL"
		if slot != "" {
			msg += " (slot " + slot + ")"
		}
		return apierr.NewValidation(msg)
	}
	return nil
}
