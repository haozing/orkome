package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePrefix = "orkome/internal/"

func domainOf(rel string) string {
	rest := strings.TrimPrefix(rel, "internal/")
	if rest == rel {
		return ""
	}
	return strings.SplitN(rest, "/", 2)[0]
}

type violation struct {
	file string
	rule string
	imp  string
}

func TestDependencyIronLaws(t *testing.T) {
	root := findRoot(t)
	var violations []violation

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "e2e/") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			return nil
		}

		isDomain := strings.Contains("/"+rel, "/domain/")
		isApp := strings.Contains("/"+rel, "/app/")
		isAdapters := strings.Contains("/"+rel, "/adapters/")
		isPlatform := strings.HasPrefix(rel, "internal/platform/")
		dom := domainOf(rel)
		isTestkit := strings.HasPrefix(rel, "internal/testkit")

		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			internal := strings.HasPrefix(p, modulePrefix)
			std := !internal && !strings.Contains(strings.SplitN(p, "/", 2)[0], ".")

			if strings.HasPrefix(p, "github.com/cloudwego/eino-ext") &&
				!strings.HasPrefix(rel, "internal/orchestration/adapters/engine/") {
				violations = append(violations, violation{rel, "eino-ext importable only by adapters/engine (internal archive 09 §1.1)", p})
			}

			switch {
			case isTestkit:

				if internal && !strings.HasPrefix(p, "orkome/internal/contract") &&
					!strings.HasPrefix(p, "orkome/internal/platform") {
					violations = append(violations, violation{rel, "testkit purity", p})
				}
			case isDomain:

				if !std && !strings.HasPrefix(p, "orkome/internal/contract") {
					violations = append(violations, violation{rel, "rule 1 domain purity", p})
				}
			case isPlatform:

				if internal && !strings.HasPrefix(p, "orkome/internal/platform") &&
					!strings.HasPrefix(p, "orkome/internal/contract") {
					violations = append(violations, violation{rel, "platform purity (zero business knowledge)", p})
				}
			case isApp:

				if std || strings.HasPrefix(p, "orkome/internal/contract") ||
					strings.HasPrefix(p, "orkome/internal/platform") {
					continue
				}
				if strings.HasPrefix(p, "orkome/internal/"+dom+"/domain") ||
					strings.HasPrefix(p, "orkome/internal/"+dom+"/wire") {
					continue
				}
				violations = append(violations, violation{rel, "rule 2/3 app dependency face", p})
			case isAdapters:

				if internal && dom != "" {
					other := strings.TrimPrefix(p, modulePrefix)
					otherDom := strings.SplitN(other, "/", 2)[0]
					if otherDom != dom && otherDom != "contract" && otherDom != "platform" && otherDom != "testkit" {
						violations = append(violations, violation{rel, "rule 3 no cross-domain imports", p})
					}
				}
			default:

				if internal && strings.Contains(p, "/adapters") {
					violations = append(violations, violation{rel, "rule 4 adapters imported only by cmd", p})
				}

				if strings.HasPrefix(rel, "internal/contract/") && internal &&
					!strings.HasPrefix(p, "orkome/internal/contract") {
					violations = append(violations, violation{rel, "contract zero dependencies", p})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(violations) > 0 {
		sort.Slice(violations, func(i, j int) bool { return violations[i].file < violations[j].file })
		for _, v := range violations {
			t.Errorf("violates %s: %s import %q", v.rule, v.file, v.imp)
		}
	}
}

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found (project root)")
		}
		dir = parent
	}
}
