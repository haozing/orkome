package arch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type allowRule struct {
	ID            string   `yaml:"id"`
	Domain        string   `yaml:"domain"`
	Source        string   `yaml:"source"`
	Anchor        string   `yaml:"anchor"`
	ForeignTables []string `yaml:"foreign_tables"`
	Func          string   `yaml:"func"`
	Reason        string   `yaml:"reason"`
}

type allowExemption struct {
	ID     string `yaml:"id"`
	Reason string `yaml:"reason"`
}

type allowlistFile struct {
	TableOwners map[string][]string `yaml:"table_owners"`
	Rules       []allowRule         `yaml:"rules"`
	Exemptions  []allowExemption    `yaml:"exemptions"`
}

const (
	settlementRel = "internal/orchestration/adapters/pgrepo/queries/settlement.sql"
	allowlistRel  = "arch/sql_allowlist.yml"
)

func loadAllowlist(t *testing.T) (allowlistFile, map[string]string) {
	t.Helper()
	root := findRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, allowlistRel))
	if err != nil {
		t.Fatalf("read %s: %v", allowlistRel, err)
	}
	var af allowlistFile
	if err := yaml.Unmarshal(raw, &af); err != nil {
		t.Fatalf("parse %s: %v", allowlistRel, err)
	}
	if len(af.TableOwners) == 0 {
		t.Fatal("table_owners is empty (register table owners per internal archive 03 §2)")
	}
	owner := make(map[string]string)
	for dom, tables := range af.TableOwners {
		for _, tb := range tables {
			if prev, dup := owner[tb]; dup {
				t.Errorf("table %s registered twice in table_owners (%s and %s)", tb, prev, dom)
				continue
			}
			owner[tb] = dom
		}
	}
	seenRule := map[string]bool{}
	for _, r := range af.Rules {
		if r.ID == "" || seenRule[r.ID] {
			t.Errorf("allowlist rule id missing or duplicated: %q", r.ID)
			continue
		}
		seenRule[r.ID] = true
		if _, ok := af.TableOwners[r.Domain]; !ok {
			t.Errorf("rule %s: domain %q not in table_owners", r.ID, r.Domain)
		}
		for _, ft := range r.ForeignTables {
			dom, ok := owner[ft]
			if !ok {
				t.Errorf("rule %s: foreign table %q has no registered owner", r.ID, ft)
				continue
			}
			if dom == r.Domain {
				t.Errorf("rule %s: foreign_tables contains own-domain table %q (own tables need no allowlist)", r.ID, ft)
			}
		}
	}
	return af, owner
}

type settleStmt struct {
	ID      string
	Func    string
	Anchor  string
	Foreign []string
	SQL     string
	Line    int
}

func parseSettlement(t *testing.T) []settleStmt {
	t.Helper()
	path := filepath.Join(findRoot(t), filepath.FromSlash(settlementRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", settlementRel, err)
	}
	var (
		out    []settleStmt
		cur    *settleStmt
		body   []string
		commit = func() {
			if cur == nil {
				return
			}
			cur.SQL = strings.TrimSpace(strings.Join(body, "\n"))
			if cur.SQL == "" {
				t.Errorf("settlement statement %s has no body", cur.ID)
			} else if !strings.HasSuffix(cur.SQL, ";") {
				t.Errorf("settlement statement %s must end with ;", cur.ID)
			}
			out = append(out, *cur)
			cur, body = nil, nil
		}
	)
	for i, ln := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(trimmed, "-- @id:"):
			commit()
			cur = &settleStmt{ID: strings.TrimSpace(strings.TrimPrefix(trimmed, "-- @id:")), Line: i + 1}
			if cur.ID == "" {
				t.Errorf("settlement.sql:%d empty @id", i+1)
			}
		case cur != nil && strings.HasPrefix(trimmed, "-- @func:"):
			cur.Func = strings.TrimSpace(strings.TrimPrefix(trimmed, "-- @func:"))
		case cur != nil && strings.HasPrefix(trimmed, "-- @anchor:"):
			cur.Anchor = strings.TrimSpace(strings.TrimPrefix(trimmed, "-- @anchor:"))
		case cur != nil && strings.HasPrefix(trimmed, "-- @foreign:"):
			f := strings.TrimSpace(strings.TrimPrefix(trimmed, "-- @foreign:"))
			if f != "none" && f != "" {
				cur.Foreign = strings.Fields(f)
			}
		case strings.HasPrefix(trimmed, "--"):

		case cur != nil:
			body = append(body, ln)
		}
	}
	commit()
	if len(out) == 0 {
		t.Fatalf("  %s has no registered statements (must start with -- @id:)", settlementRel)
	}
	return out
}

type goSQL struct {
	Domain   string
	File     string
	Func     string
	SQL      string
	Complete bool
}

const pgrepoGlob = "internal/*/adapters/pgrepo/*.go"

func extractGoSQL(t *testing.T, root string) []goSQL {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pgrepoGlob)))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no domain pgrepo files scanned (%s)", pgrepoGlob)
	}
	var out []goSQL
	seen := map[string]bool{}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		domain := strings.SplitN(strings.TrimPrefix(rel, "internal/"), "/", 2)[0]

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			continue
		}

		consts := pkgStringConsts(t, filepath.Dir(f))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				record := func(sqlText string, complete bool) {
					key := fn.Name.Name + "\x00" + normSQL(sqlText)
					if seen[key] {
						return
					}
					seen[key] = true
					out = append(out, goSQL{Domain: domain, File: rel, Func: fn.Name.Name, SQL: sqlText, Complete: complete})
				}

				switch sel.Sel.Name {
				case "Exec", "Query", "QueryRow":
					for _, arg := range call.Args {
						cand, c := resolveString(arg, consts)
						if isSQLish(cand) {
							record(cand, c)
							break
						}
					}
				case "Sprintf":

					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fmt" {
						if cand, c := resolveString(call, consts); isSQLish(cand) {
							record(cand, c)
						}
					}
				}
				return true
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Func < out[j].Func
	})
	return out
}

func pkgStringConsts(t *testing.T, dir string) map[string]string {
	t.Helper()
	consts := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range af.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if s, err := strconv.Unquote(lit.Value); err == nil {
					consts[vs.Names[0].Name] = s
				}
			}
		}
	}
	return consts
}

func resolveString(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return v.Value, false
		}
		return s, true
	case *ast.ParenExpr:
		return resolveString(v.X, consts)
	case *ast.Ident:
		if s, ok := consts[v.Name]; ok {
			return s, true
		}
		return v.Name, false
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := resolveString(v.X, consts)
		r, rok := resolveString(v.Y, consts)
		return l + r, lok && rok
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" {
			return "", false
		}
		format, ok := resolveString(v.Args[0], consts)
		if !ok {
			return "", false
		}
		var b strings.Builder
		complete, argIdx := true, 1
		for i := 0; i < len(format); i++ {
			if format[i] != '%' || i+1 >= len(format) {
				b.WriteByte(format[i])
				continue
			}
			verb := format[i+1]
			i++
			switch verb {
			case '%':
				b.WriteByte('%')
			case 's', 'd', 'v':
				if argIdx >= len(v.Args) {
					complete = false
					b.WriteString("%" + string(verb))
					continue
				}
				s, ok := resolveString(v.Args[argIdx], consts)
				argIdx++
				if ok {
					b.WriteString(s)
				} else {
					complete = false
					b.WriteString("%" + string(verb))
				}
			default:
				b.WriteString("%" + string(verb))
			}
		}
		return b.String(), complete
	}
	return "", false
}

var sqlTablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\binsert\s+into\s+"?([a-z_][a-z0-9_]*)"?`),
	regexp.MustCompile(`(?is)\bdelete\s+from\s+"?([a-z_][a-z0-9_]*)"?`),
	regexp.MustCompile(`(?is)\bupdate\s+(?:only\s+)?"?([a-z_][a-z0-9_]*)"?`),
	regexp.MustCompile(`(?is)\bfrom\s+"?([a-z_][a-z0-9_]*)"?`),
	regexp.MustCompile(`(?is)\bjoin\s+"?([a-z_][a-z0-9_]*)"?`),
}

var sqlNoiseWords = map[string]bool{
	"select": true, "lateral": true, "values": true, "set": true, "do": true,
	"skip": true, "nowait": true, "locked": true, "of": true, "only": true,
	"where": true, "as": true, "on": true, "using": true, "returning": true,
	"unnest": true, "generate_series": true, "jsonb_array_elements": true,
	"jsonb_array_elements_text": true, "jsonb_each": true, "jsonb_object_keys": true,
	"string_to_table": true, "regexp_split_to_table": true, "coalesce": true,
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
	"array_agg": true, "jsonb_agg": true, "string_agg": true, "date_trunc": true,
	"extract": true, "greatest": true, "least": true, "now": true,
	"interval": true, "dual": true, "table": true,
}

func isSQLish(cand string) bool {
	trimmed := strings.TrimSpace(cand)
	if trimmed == "" {
		return false
	}
	switch strings.ToUpper(strings.Fields(trimmed)[0]) {
	case "SELECT", "WITH", "INSERT", "UPDATE", "DELETE":
		return true
	}
	return false
}

var cteNameRe = regexp.MustCompile(`(?is)(?:\bwith\s+(?:recursive\s+)?|,\s*)([a-z_][a-z0-9_]*)\s+as\s*\(`)

func tablesInSQL(sql string) []string {

	var lines []string
	for _, ln := range strings.Split(sql, "\n") {
		if i := strings.Index(ln, "--"); i >= 0 {
			ln = ln[:i]
		}
		lines = append(lines, ln)
	}
	clean := strings.Join(lines, "\n")

	cte := map[string]bool{}
	for _, m := range cteNameRe.FindAllStringSubmatch(clean, -1) {
		cte[strings.ToLower(m[1])] = true
	}

	seen := map[string]bool{}
	var out []string
	for i, re := range sqlTablePatterns {
		for _, loc := range re.FindAllStringSubmatchIndex(clean, -1) {
			name := strings.ToLower(clean[loc[2]:loc[3]])
			rest := strings.TrimLeft(clean[loc[1]:], " \t\r\n")

			if i != 0 && strings.HasPrefix(rest, "(") {
				continue
			}
			if cte[name] || sqlNoiseWords[name] {
				continue
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func normSQL(s string) string {
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		if i := strings.Index(ln, "--"); i >= 0 {
			ln = ln[:i]
		}
		lines = append(lines, ln)
	}
	joined := strings.TrimSpace(strings.Join(lines, "\n"))
	joined = strings.TrimSuffix(joined, ";")
	return strings.Join(strings.Fields(joined), " ")
}

func TestSQLAllowlistCrossDomain(t *testing.T) {
	af, owner := loadAllowlist(t)
	root := findRoot(t)
	stmts := extractGoSQL(t, root)

	covered := map[string]bool{}
	for _, r := range af.Rules {
		for _, ft := range r.ForeignTables {
			covered[r.Domain+"|"+ft] = true
		}
	}

	unknown := map[string]string{}
	usedPairs := map[string][]string{}
	for _, s := range stmts {
		for _, tb := range tablesInSQL(s.SQL) {
			dom, ok := owner[tb]
			if !ok {
				if _, seen := unknown[tb]; !seen {
					unknown[tb] = fmt.Sprintf("%s %s", s.File, s.Func)
				}
				continue
			}
			if dom == s.Domain {
				continue
			}
			key := s.Domain + "|" + tb
			usedPairs[key] = append(usedPairs[key], fmt.Sprintf("%s %s", s.Func, filepath.ToSlash(s.File)))
			if !covered[key] {
				t.Errorf("domain %s SQL (%s · %s) touches foreign table %s (owned by %s) without allowlist — register in arch/sql_allowlist.yml or reroute via a port (internal archive 02 §5.3/§5.4)",
					s.Domain, s.Func, s.File, tb, dom)
			} else {
				t.Logf("  [allowlist pass] domain %s · %s · table %s (owned by %s)", s.Domain, s.Func, tb, dom)
			}
		}
	}
	for tb, where := range unknown {
		t.Errorf("SQL references table %s with no registered owner (first seen %s) — add to arch/sql_allowlist.yml table_owners first (internal archive 03 §2)", tb, where)
	}

	for _, r := range af.Rules {
		for _, ft := range r.ForeignTables {
			key := r.Domain + "|" + ft
			if len(usedPairs[key]) == 0 {
				t.Errorf("allowlist rule %s authorizes (domain %s, table %s) but no reference found (stale rule, should delete)", r.ID, r.Domain, ft)
			}
		}
	}
}

func TestSettlementSQLInSync(t *testing.T) {
	af, owner := loadAllowlist(t)
	settle := parseSettlement(t)
	stmts := extractGoSQL(t, findRoot(t))

	byID := map[string]bool{}
	for _, st := range settle {
		if byID[st.ID] {
			t.Errorf("settlement statement id duplicated: %s", st.ID)
		}
		byID[st.ID] = true
	}

	for _, r := range af.Rules {
		if strings.HasSuffix(filepath.ToSlash(r.Source), "queries/settlement.sql") && !byID[r.ID] {
			t.Errorf("rule %s declares source=settlement.sql but the file has no such @id", r.ID)
		}
	}

	effected := map[string]goSQL{}
	for _, s := range stmts {
		if s.Complete && s.Domain == "orchestration" {
			n := normSQL(s.SQL)
			if _, ok := effected[n]; !ok {
				effected[n] = s
			}
		}
	}

	for _, st := range settle {
		n := normSQL(st.SQL)
		src, ok := effected[n]
		if !ok {
			t.Errorf("settlement statement %s (%s:%d) does not match repo.go's actual SQL — both sides must change in sync (normalized-whitespace compare failed)",
				st.ID, settlementRel, st.Line)
			continue
		}

		var cross []string
		for _, tb := range tablesInSQL(st.SQL) {
			if dom, known := owner[tb]; known && dom != "orchestration" {
				cross = append(cross, tb)
			}
		}
		sort.Strings(cross)
		want := append([]string(nil), st.Foreign...)
		sort.Strings(want)
		if strings.Join(cross, ",") != strings.Join(want, ",") {
			t.Errorf("settlement statement %s's @foreign annotation %v does not match the foreign tables actually touched %v (func %s / code %s)",
				st.ID, want, cross, st.Func, src.Func)
		}
		t.Logf("  [settlement sync] %s ← %s (%s)", st.ID, src.Func, src.File)
	}
}

func TestTableOwnersCoverMigrations(t *testing.T) {
	_, owner := loadAllowlist(t)
	root := findRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations/*.up.sql scan failed: %v", err)
	}
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?\s*\(`)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			tb := strings.ToLower(m[1])
			if _, ok := owner[tb]; !ok {
				t.Errorf("migration %s creates table %s with no registered owner — add to arch/sql_allowlist.yml table_owners (internal archive 03 §2)",
					filepath.Base(f), tb)
			}
		}
	}
}
