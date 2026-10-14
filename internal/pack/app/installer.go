package app

// Install：能力包一键安装编排器（能力包平台化）。把 provision 向导此前要
// 跨 4 个域手工排序的调用（pack upsert → MCP(+凭证) → webhook 订阅+关联 →
// UI 进驻）收进 pack 域一个事务性语义的入口。跨域写经 4 个窄接口端口，
// 各域类型在这里不可见（arch iron law：端口方法集只说本域语言，形状转换
// 由组合根适配层做）。
//
// 编排纪律：定序 fail-fast——任何一步失败立即中止并回传带步骤定位的错误，
// 不做补偿回滚（已建行的清理归 uninstall/重装语义，与既有 provision 的
// 手工排序行为一致）。

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// 安装编排的哨兵错误：装配缺失类（*NotWired）在 HTTP 层映射 503 风格报错，
// spec 缺陷类映射 400。
var (
	errInstallNameRequired        = errors.New("name required")
	errInstallTransportRequired   = errors.New("mcp transport required (no server-side default)")
	errInstallURLTemplateInvalid  = errors.New("url must be an absolute http(s) URL")
	errInstallMCPOptionalMissing  = errors.New("mcp name and url required")
	errInstallUIOptionMissing     = errors.New("ui label and url_template required")
	errInstallCredentialsNotWired = errors.New("credentials port not wired")
	errInstallMCPNotWired         = errors.New("mcp port not wired")
	errInstallWebhooksNotWired    = errors.New("webhooks port not wired")
	errInstallUIEntryNotWired     = errors.New("ui entry port not wired")
)

// InstallSpec 是安装单：前端安装向导一次提交的全部资产。
type InstallSpec struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	MCP     []InstallMCP   `json:"mcp"`
	Events  []InstallEvent `json:"events"`
	UI      *InstallUI     `json:"ui"`
}

// InstallMCP 是一台要注册的 MCP 服务器；Credential 非空时先建凭证再注册
// （凭证是独立生命体，走 workspace 凭证库，不随 pack 删除）。
type InstallMCP struct {
	Name       string             `json:"name"`
	Transport  string             `json:"transport"`
	URL        string             `json:"url"`
	Credential *InstallCredential `json:"credential"`
}

// InstallCredential 是随装凭证：Provider 必填、Secret 必填，密封语义由
// 凭证域实现负责（本域只见"建了、拿到 id"）。
type InstallCredential struct {
	Provider string `json:"provider"`
	Secret   string `json:"secret"`
}

// InstallEvent 是一条厂商事件订阅。
type InstallEvent struct {
	URL         string   `json:"url"`
	Types       []string `json:"types"`
	Description string   `json:"description"`
}

// InstallUI 是控制台进驻（入口 + 可选 slots）。
type InstallUI struct {
	Label       string          `json:"label"`
	URLTemplate string          `json:"url_template"`
	Slots       []InstallUISlot `json:"slots"`
}

// InstallUISlot 是一个工单详情 tab。
type InstallUISlot struct {
	Slot        string `json:"slot"`
	Label       string `json:"label"`
	URLTemplate string `json:"url_template"`
	Height      int    `json:"height"`
}

// InstalledSubscription 是安装结果的订阅回执：secret 只在此刻一次性回显
// （与 vendor API 的创建语义一致，之后只存密封件）。
type InstalledSubscription struct {
	URL            string `json:"url"`
	SubscriptionID string `json:"subscription_id"`
	Secret         string `json:"secret"`
}

// InstallResult 是安装回执：pack 本体 + 全部订阅回执。
type InstallResult struct {
	Pack          Pack                    `json:"pack"`
	Subscriptions []InstalledSubscription `json:"subscriptions"`
}

// InstallPorts 聚合安装编排的 4 个跨域写端口（窄接口，方法集与各域服务
// 具体类型解耦；wiring 适配后注入。nil 端口 = 该类资产未接线，Install
// 在 spec 需要它时报错而不是静默跳过——装配缺失必须显性暴露）。
type InstallPorts struct {
	// Credentials 建 workspace 凭证（返回凭证 id）。
	Credentials interface {
		Create(ctx context.Context, ws, provider, secret, createdBy string) (id string, err error)
	}
	// MCP 注册一台 workspace MCP 服务器（credID 空串=无凭证关联；适配层
	// 负责转本域模型并落 pack_id）。bearerSecret 是凭证明文——引擎的 MCP
	// 鉴权约定是 credentials 信封 {"headers":{"Authorization":"Bearer X"}}
	//（mcpHeadersOf 只认 headers 键；{"credential_id":id} 引用形状在 MCP
	// 链路无解引用点，真机 payload pack 探针 401 实证），适配层用它构造。
	MCP interface {
		Create(ctx context.Context, ws string, m InstallMCP, credID, bearerSecret, packID string) error
	}
	// Webhooks 建厂商事件订阅（返回订阅 id + 一次性 secret 明文）。
	Webhooks interface {
		CreateSubscription(ctx context.Context, ws, url, desc string, types []string) (id, secret string, err error)
	}
	// UIEntry 进驻控制台（upsert 语义；完整 slot 冻结校验在实现内——它内部
	// 就是 orchestration 的 UpsertWithSlots，v0 目录约束不在这里重复）。
	UIEntry interface {
		UpsertWithSlots(ctx context.Context, ws, pack, packID, label, urlTemplate string, slots []UISlotInfo) error
	}
}

// Install 按 pack → MCP(+凭证) → 订阅(+关联) → UI 定序执行，fail-fast。
// userID 记凭证创建者。返回 pack 行与订阅回执（secret 一次性明文）。
func (s *PackService) Install(ctx context.Context, ws, userID string, spec InstallSpec) (InstallResult, error) {
	if spec.Name == "" {
		return InstallResult{}, fmt.Errorf("install %s: %w", spec.Name, errInstallNameRequired)
	}
	pack, err := s.Upsert(ctx, ws, spec.Name, spec.Version)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: step pack: %w", spec.Name, err)
	}
	res := InstallResult{Pack: pack}

	for i, m := range spec.MCP {
		if err := validateInstallMCP(m); err != nil {
			return res, fmt.Errorf("install %s: step mcp[%d]: %w", spec.Name, i, err)
		}
		credID := ""
		bearerSecret := ""
		if m.Credential != nil {
			if s.install.Credentials == nil {
				return res, fmt.Errorf("install %s: step mcp[%d]: %w", spec.Name, i, errInstallCredentialsNotWired)
			}
			id, err := s.install.Credentials.Create(ctx, ws, m.Credential.Provider, m.Credential.Secret, userID)
			if err != nil {
				return res, fmt.Errorf("install %s: step mcp[%d]: create credential: %w", spec.Name, i, err)
			}
			credID = id
			bearerSecret = m.Credential.Secret
		}
		if s.install.MCP == nil {
			return res, fmt.Errorf("install %s: step mcp[%d]: %w", spec.Name, i, errInstallMCPNotWired)
		}
		if err := s.install.MCP.Create(ctx, ws, m, credID, bearerSecret, pack.ID); err != nil {
			return res, fmt.Errorf("install %s: step mcp[%d]: register: %w", spec.Name, i, err)
		}
	}

	for i, ev := range spec.Events {
		if s.install.Webhooks == nil {
			return res, fmt.Errorf("install %s: step event[%d]: %w", spec.Name, i, errInstallWebhooksNotWired)
		}
		if err := s.Owns(ctx, ws, pack.ID); err != nil {
			return res, fmt.Errorf("install %s: step event[%d]: %w", spec.Name, i, err)
		}
		id, secret, err := s.install.Webhooks.CreateSubscription(ctx, ws, ev.URL, ev.Description, ev.Types)
		if err != nil {
			return res, fmt.Errorf("install %s: step event[%d]: subscribe: %w", spec.Name, i, err)
		}
		if err := s.LinkSubscription(ctx, pack.ID, id); err != nil {
			return res, fmt.Errorf("install %s: step event[%d]: link: %w", spec.Name, i, err)
		}
		res.Subscriptions = append(res.Subscriptions, InstalledSubscription{URL: ev.URL, SubscriptionID: id, Secret: secret})
	}

	if spec.UI != nil {
		if err := validateInstallUI(spec.UI); err != nil {
			return res, fmt.Errorf("install %s: step ui: %w", spec.Name, err)
		}
		if s.install.UIEntry == nil {
			return res, fmt.Errorf("install %s: step ui: %w", spec.Name, errInstallUIEntryNotWired)
		}
		slots := make([]UISlotInfo, 0, len(spec.UI.Slots))
		for _, sl := range spec.UI.Slots {
			slots = append(slots, UISlotInfo{Slot: sl.Slot, Label: sl.Label, URLTemplate: sl.URLTemplate, Height: sl.Height})
		}
		if err := s.install.UIEntry.UpsertWithSlots(ctx, ws, spec.Name, pack.ID, spec.UI.Label, spec.UI.URLTemplate, slots); err != nil {
			return res, fmt.Errorf("install %s: step ui: upsert: %w", spec.Name, err)
		}
	}
	return res, nil
}

// validateInstallMCP 只做本域最小校验：name/url 必填、url 为绝对 http(s)。
// transport 必填不给默认——默认值是安装向导前端的职责（服务端默认会掩盖
// 厂商 manifest 的缺失字段）。url 的网络可达性校验在 MCP 域实现内。
func validateInstallMCP(m InstallMCP) error {
	if m.Name == "" || m.URL == "" {
		return errInstallMCPOptionalMissing
	}
	if m.Transport == "" {
		return errInstallTransportRequired
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errInstallURLTemplateInvalid
	}
	return nil
}

// validateInstallUI 与 MCP 同口径的最小校验：label/url_template 必填、
// 模板为绝对 http(s)。slot 的完整冻结校验（v0 目录只认 workitem.detail.tab、
// height 范围）交给注入的 UIEntry 端口实现——那份规则活在 orchestration 域，
// 在这里复制一份必然漂移。
func validateInstallUI(ui *InstallUI) error {
	if ui.Label == "" || ui.URLTemplate == "" {
		return errInstallUIOptionMissing
	}
	u, err := url.Parse(ui.URLTemplate)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errInstallURLTemplateInvalid
	}
	return nil
}
