package main

import (
	"net/http"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
)

// 反向走查（mux → 表）：routeTable 的单向对账（TestRouteContractWalk）只证明
// "声明过的都已挂载"，证明不了反方向 —— 新端点挂了但没登记进契约表时无人发现
// （batch1 P1 / batch2 FP-19 实证：pins×4、cli-token、usage、audit、admin×10、
// share-links/redeem、pack-tool-schemas×3 全部漏登记）。本文件从真实挂载面出发
// 枚举 stdlib ServeMux 上注册的全部 pattern，逐条要求它在 routeTable 声明过，
// 或命中下方的显式豁免清单（豁免≠漏登，一条一行写明理由）。

// reverseWalkExemptions maps an exact registered pattern (or method-less
// subtree mount) to the reason it is intentionally absent from routeTable.
// Every entry must carry its own justification; exemptions are reserved for
// platform/infrastructure faces — business endpoints belong in routeTable.
var reverseWalkExemptions = map[string]string{
	// workspacehttp.Mount 把 14 条工作区子路由挂在无方法前缀的子树 mount 后面
	// （webx 子 mux + StripPrefix 委托）。子树 mount 本身不是端点；叶子路由由
	// TestWorkspaceSubroutesDeclared 通过 workspacehttp.SubrouteInfos() 逐条
	// 对账（registration 与枚举共用 workspaceSubroutes，不会漂移）。
	"/api/workspaces/{workspaceId}/": "subtree delegation mount, not an endpoint; leaves enumerated via workspacehttp.SubrouteInfos",

	// 以下平台面不在本测试的走查面内（buildTestAPIRouter 不重建它们——需要
	// DB/redis/框架 identity 栈），列于此处仅为记录走查边界，永不匹配：
	//   GET /healthz、GET /readyz      — 进程存活/就绪探针（main.go 内联）
	//   /metrics                       — 指标面，MetricsToken 防护（框架 metrics）
	//   GET /config                    — 框架 ConfigHandler 前端引导面
	//   GET /ws                        — 实时推送升级端点（需 wswire Hub + redis）
	//   /auth/** + /api/tokens×3       — 框架 identityhttp.Mount 认证域
	//                                    （会话/PAT/OAuth/Fed SSO），正向由
	//                                    e2e TestRoutesWalkGuards G1 兜底
}

// TestRouteContractReverseWalk asserts the other direction of the contract:
// every pattern registered on the composition mux must be declared in
// routeTable or explicitly exempted above. It fails with the FULL list of
// violations, not just the first one.
func TestRouteContractReverseWalk(t *testing.T) {
	mux := buildTestAPIRouter()
	registered := enumerateServeMuxPatterns(t, mux)
	require.NotEmpty(t, registered, "mux enumeration found no patterns — extractor drift")

	declared := make(map[string]bool, len(routeTable))
	for _, e := range routeTable {
		p := e.Pattern
		if !rawRoutes[e.Method+" "+e.Pattern] {
			p = "/api" + p
		}
		declared[canonical(p)] = true
	}

	var undeclared []string
	for _, raw := range registered {
		pattern := raw
		method := ""
		if i := strings.IndexByte(raw, ' '); i >= 0 {
			method, pattern = raw[:i], raw[i+1:]
		}
		if method == "" {
			// Method-less patterns are subtree mounts, not endpoints.
			if _, ok := reverseWalkExemptions[pattern]; !ok {
				undeclared = append(undeclared, "MOUNT "+pattern+" (subtree mount: exempt or document)")
			}
			continue
		}
		if declared[canonical(pattern)] {
			continue
		}
		if _, ok := reverseWalkExemptions[method+" "+pattern]; ok {
			continue
		}
		undeclared = append(undeclared, method+" "+pattern)
	}
	sort.Strings(undeclared)
	require.Empty(t, undeclared,
		"mounted on the composition mux but neither declared in routeTable nor exempted (new endpoints must be registered into the contract): %v", undeclared)
}

// TestWorkspaceSubroutesDeclared reconciles the workspacehttp sub-mux leaves
// (hidden behind the /api/workspaces/{workspaceId}/ subtree mount, invisible
// to the top-level walk) against the routeTable Sub entries. The enumeration
// shares registration with the serving path (workspaceSubroutes), so the two
// cannot drift — this pins the table side.
func TestWorkspaceSubroutesDeclared(t *testing.T) {
	declared := make(map[string]bool, len(subRoutes))
	for key := range subRoutes {
		declared[canonical("/api"+strings.SplitN(key, " ", 2)[1])] = true
	}
	require.NotEmpty(t, declared, "routeTable declares no workspace subroutes")

	var missing []string
	served := map[string]bool{}
	for _, ri := range workspacehttp.SubrouteInfos() {
		if ri.Method == "" {
			continue
		}
		served[canonical(ri.Path)] = true
		if !declared[canonical(ri.Path)] {
			missing = append(missing, ri.Pattern+" "+ri.Path)
		}
	}
	// 反向一致性：subRoutes 里写错方法/路径的条目（对不上子 mux 实际服务面）
	// 也要当场报出，防止对账键漂移成死条目。
	var stale []string
	for key := range subRoutes {
		if !served[canonical("/api"+strings.SplitN(key, " ", 2)[1])] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	require.Empty(t, stale,
		"routeTable sub entries that the workspace sub-mux does not serve (typo/drift): %v", stale)
	require.Empty(t, missing,
		"workspace sub-mux serves routes the contract table does not declare: %v", missing)
}

// TestRouteContractExtractorSelfCheck pins the enumeration itself: every
// /api-relative routeTable entry must be seen by the tree walk. If this fails
// the stdlib layout mirror mis-reads (Go release drift) — the reverse walk
// result is meaningless until the enumerator is fixed.
func TestRouteContractExtractorSelfCheck(t *testing.T) {
	mux := buildTestAPIRouter()
	extracted := make(map[string]bool)
	for _, raw := range enumerateServeMuxPatterns(t, mux) {
		pattern := raw
		if i := strings.IndexByte(raw, ' '); i >= 0 {
			pattern = raw[i+1:]
		}
		extracted[canonical(pattern)] = true
	}

	var unseen []string
	for _, e := range routeTable {
		key := e.Method + " " + e.Pattern
		if rawRoutes[key] || subRoutes[key] {
			continue // absolute/root-surface and subtree-leaf entries live outside the walked api mux
		}
		if !extracted[canonical("/api"+e.Pattern)] {
			unseen = append(unseen, key)
		}
	}
	sort.Strings(unseen)
	require.Empty(t, unseen,
		"declared routes invisible to the ServeMux tree walk (extractor/layout drift on %s, not a contract gap): %v", runtime.Version(), unseen)
}

// enumerateServeMuxPatterns extracts every pattern registered on mux by
// walking the stdlib ServeMux routing tree. net/http exposes no enumeration
// API (the ploykit issue "route-recording mux" notes this), so the walk reads
// the unexported tree reflectively — fully self-describing (fields located by
// name and kind at runtime, no hardcoded offsets), which survives the
// go1.26 (tree *routingNode + map children) → go1.27 (tree routingNode +
// mapping children) layout change. Any future layout change fails LOUDLY via
// t.Fatalf here instead of silently returning garbage: every test in this
// file also cross-validates the extraction against the known routeTable.
//
// GODEBUG=httpmuxgo121=1 bypasses the 1.22+ tree entirely and is therefore
// unsupported by design (the self-check test will fail loudly).
func enumerateServeMuxPatterns(t *testing.T, mux *http.ServeMux) []string {
	t.Helper()

	muxV := reflect.ValueOf(mux).Elem()
	treeV := muxV.FieldByName("tree")
	if !treeV.IsValid() {
		t.Fatalf("net/http ServeMux has no 'tree' field on %s — update the router-contract enumerator for this Go release", runtime.Version())
	}

	// Address of the tree field; routingNode type from its static type.
	treeAddr := treeV.Addr().UnsafePointer()
	var nodeType reflect.Type
	switch treeV.Kind() {
	case reflect.Pointer: // go1.22–go1.26: tree *routingNode
		nodeType = treeV.Type().Elem()
	case reflect.Struct: // go1.27+: tree routingNode (value)
		nodeType = treeV.Type()
	default:
		t.Fatalf("unsupported ServeMux.tree kind %v on %s", treeV.Kind(), runtime.Version())
	}

	patternField, ok := nodeType.FieldByName("pattern")
	if !ok || patternField.Type.Kind() != reflect.Pointer {
		t.Fatalf("routingNode has no 'pattern *…' field on %s", runtime.Version())
	}
	patternType := patternField.Type.Elem()
	strField, ok := patternType.FieldByName("str")
	if !ok || strField.Type.Kind() != reflect.String {
		t.Fatalf("pattern struct has no 'str string' field on %s", runtime.Version())
	}
	childrenField, ok := nodeType.FieldByName("children")
	if !ok {
		t.Fatalf("routingNode has no 'children' field on %s", runtime.Version())
	}

	// Child-node pointer fields other than `pattern` (multiChild/emptyChild in
	// go1.27+, discovered generically so future fields keep working).
	var selfPtrOffsets []uintptr
	for i := 0; i < nodeType.NumField(); i++ {
		f := nodeType.Field(i)
		if f.Name != "pattern" && f.Type.Kind() == reflect.Pointer && f.Type.Elem() == nodeType {
			selfPtrOffsets = append(selfPtrOffsets, f.Offset)
		}
	}

	// Children accessor for both layouts.
	readChildren := func(node unsafe.Pointer) map[string]unsafe.Pointer {
		base := unsafe.Add(node, childrenField.Offset)
		out := map[string]unsafe.Pointer{}
		switch childrenField.Type.Kind() {
		case reflect.Map: // go1.22–go1.26: map[string]*routingNode
			require.Equal(t, reflect.String, childrenField.Type.Key().Kind(), "children map key must be string")
			for k, v := range *(*map[string]unsafe.Pointer)(base) {
				out[k] = v
			}
		case reflect.Struct: // go1.27+: mapping[string,*routingNode]{s []entry; m map}
			mf, ok := childrenField.Type.FieldByName("m")
			if !ok || mf.Type.Kind() != reflect.Map {
				t.Fatalf("routingNode.children mapping has no 'm map' field on %s", runtime.Version())
			}
			for k, v := range *(*map[string]unsafe.Pointer)(unsafe.Add(base, mf.Offset)) {
				out[k] = v
			}
			sf, ok := childrenField.Type.FieldByName("s")
			if !ok || sf.Type.Kind() != reflect.Slice {
				t.Fatalf("routingNode.children mapping has no 's []entry' field on %s", runtime.Version())
			}
			// entry[K,V] is struct{K; V} = struct{string; *node} — 24 bytes,
			// layout-identical to the mirror below.
			et := sf.Type.Elem()
			if et.Kind() != reflect.Struct || et.NumField() != 2 ||
				et.Field(0).Type.Kind() != reflect.String || et.Field(1).Type.Kind() != reflect.Pointer {
				t.Fatalf("routingNode.children mapping entry layout unexpected on %s", runtime.Version())
			}
			for _, e := range *(*[]struct {
				Key string
				Val unsafe.Pointer
			})(unsafe.Add(base, sf.Offset)) {
				out[e.Key] = e.Val
			}
		default:
			t.Fatalf("unsupported routingNode.children kind %v on %s", childrenField.Type.Kind(), runtime.Version())
		}
		return out
	}

	var root unsafe.Pointer
	if treeV.Kind() == reflect.Pointer {
		root = *(*unsafe.Pointer)(treeAddr)
	} else {
		root = treeAddr
	}

	var out []string
	seen := map[unsafe.Pointer]bool{}
	var visit func(unsafe.Pointer)
	visit = func(node unsafe.Pointer) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		if p := *(*unsafe.Pointer)(unsafe.Add(node, patternField.Offset)); p != nil {
			out = append(out, *(*string)(unsafe.Add(p, strField.Offset)))
		}
		for _, child := range readChildren(node) {
			visit(child)
		}
		for _, off := range selfPtrOffsets {
			visit(*(*unsafe.Pointer)(unsafe.Add(node, off)))
		}
	}
	visit(root)
	return out
}
