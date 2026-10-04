package domain

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	CatUnstarted = "unstarted"
	CatStarted   = "started"
	CatDone      = "done"
	CatClosed    = "closed"
)

type StatusDef struct {
	Key       string
	Name      string
	Category  string
	IsBuiltin bool
	Position  int
}

const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusBlocked    = "blocked"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

var BuiltinStatuses = []StatusDef{
	{StatusBacklog, "Backlog", CatUnstarted, true, 0},
	{StatusTodo, "Todo", CatUnstarted, true, 1},
	{StatusInProgress, "In Progress", CatStarted, true, 2},
	{StatusInReview, "In Review", CatStarted, true, 3},
	{StatusBlocked, "Blocked", CatStarted, true, 4},
	{StatusDone, "Done", CatDone, true, 5},
	{StatusCancelled, "Cancelled", CatClosed, true, 6},
}

func IsTerminal(category string) bool { return category == CatDone || category == CatClosed }

func IsCategory(v string) bool {
	switch v {
	case CatUnstarted, CatStarted, CatDone, CatClosed:
		return true
	}
	return false
}

func wireFold(category string) (string, bool) {
	switch category {
	case CatUnstarted:
		return "todo", true
	case CatStarted:
		return "in_progress", true
	case CatDone:
		return "done", true
	case CatClosed:
		return "cancelled", true
	}
	return "", false
}

func WireCategory(defs []StatusDef, key string) string {
	if IsBuiltinKey(key) {
		return key
	}
	for _, d := range defs {
		if d.Key == key {
			if fold, ok := wireFold(d.Category); ok {
				return fold
			}
			return key
		}
	}
	return key
}

func ExpandCategories(defs []StatusDef, values []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(ks ...string) {
		for _, k := range ks {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	for _, v := range values {
		if IsCategory(v) {
			for _, d := range defs {
				if d.Category == v {
					add(d.Key)
				}
			}
			continue
		}
		add(v)
	}
	return out
}

func WritableCategory(defs []StatusDef, key string) (string, bool) {
	for _, d := range defs {
		if d.Key == key {
			return d.Category, true
		}
	}
	return "", false
}

func ActiveStatusKeys(defs []StatusDef) []string {
	var out []string
	for _, d := range defs {
		if !IsTerminal(d.Category) {
			out = append(out, d.Key)
		}
	}
	return out
}

func DeriveStatusKey(name, category string, taken map[string]bool, n int) string {
	slug := slugify(name)
	if slug != "" && !taken[slug] && !IsBuiltinKey(slug) {
		return slug
	}
	base := categorySlug(category)
	for i := n; ; i++ {
		k := fmt.Sprintf("%s_%d", base, i)
		if !taken[k] && !IsBuiltinKey(k) {
			return k
		}
	}
}

func IsBuiltinKey(k string) bool {
	for _, b := range BuiltinStatuses {
		if b.Key == k {
			return true
		}
	}
	return false
}

func categorySlug(c string) string {
	switch c {
	case CatUnstarted:
		return "unstarted"
	case CatStarted:
		return "started"
	case CatDone:
		return "done"
	default:
		return "closed"
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

func NextPositionTop(min int64) int64 { return min - 1 }

func PositionBetween(before, after int64) int64 {
	if after <= before {
		return before - 1
	}
	return before + (after-before)/2
}

var wsRe = regexp.MustCompile(`\s+`)

func NormalizeTitle(t string) string {
	return strings.ToLower(wsRe.ReplaceAllString(strings.TrimSpace(t), " "))
}

var mentionRe = regexp.MustCompile(`\[@([^\]]+)\]\(mention://(user|agent|squad|work_item|all)/([0-9a-fA-F-]{0,36})\)`)

type Mention struct {
	Label string
	Type  string
	ID    string
}

func ParseMentions(content string) []Mention {
	var out []Mention
	for _, m := range mentionRe.FindAllStringSubmatch(content, -1) {
		out = append(out, Mention{Label: m[1], Type: m[2], ID: strings.ToLower(m[3])})
	}
	return out
}

type ThreadRow struct {
	ID         string
	ParentID   string
	AuthorID   string
	AuthorType string
	Resolved   bool
	Deleted    bool
}

func FoldThreads(rows []ThreadRow) []ThreadRow {
	byID := map[string]ThreadRow{}
	children := map[string][]string{}
	for _, r := range rows {
		byID[r.ID] = r
		if r.ParentID != "" {
			children[r.ParentID] = append(children[r.ParentID], r.ID)
		}
	}
	rootLast := map[string]int{}
	for i, r := range rows {
		cur := byID[r.ID]
		for cur.ParentID != "" {
			p, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			cur = p
		}
		rootLast[cur.ID] = i
	}
	var rootIDs []string
	for _, r := range rows {
		if r.ParentID == "" && !r.Deleted {
			rootIDs = append(rootIDs, r.ID)
		}
	}
	for i := 1; i < len(rootIDs); i++ {
		for j := i; j > 0 && rootLast[rootIDs[j]] > rootLast[rootIDs[j-1]]; j-- {
			rootIDs[j], rootIDs[j-1] = rootIDs[j-1], rootIDs[j]
		}
	}
	out := make([]ThreadRow, 0, len(rows))
	var walk func(pid string)
	walk = func(pid string) {
		for _, cid := range children[pid] {
			if c := byID[cid]; !c.Deleted {
				out = append(out, c)
			}
			walk(cid)
		}
	}
	for _, rid := range rootIDs {
		out = append(out, byID[rid])
		walk(rid)
	}
	return out
}
