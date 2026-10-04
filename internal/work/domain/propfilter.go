package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	PropOpEq       = "eq"
	PropOpNe       = "ne"
	PropOpGt       = "gt"
	PropOpLt       = "lt"
	PropOpContains = "contains"
	PropOpEmpty    = "empty"
)

var propFilterOps = map[string]bool{
	PropOpEq: true, PropOpNe: true, PropOpGt: true,
	PropOpLt: true, PropOpContains: true, PropOpEmpty: true,
}

type PropFilter struct {
	Key   string
	Op    string
	Value string

	Numeric bool
}

func ParsePropFilterExpr(key, expr string) (PropFilter, error) {
	op, value, _ := strings.Cut(expr, ":")
	if !propFilterOps[op] {
		return PropFilter{}, fmt.Errorf("unknown operator %q (want eq|ne|gt|lt|contains|empty)", op)
	}
	return PropFilter{Key: key, Op: op, Value: value}, nil
}

func ResolvePropFilter(defs []PropertyDef, f PropFilter) (PropFilter, error) {
	var def PropertyDef
	found := false
	for _, d := range defs {
		if d.Key == f.Key {
			def, found = d, true
			break
		}
	}
	if !found {
		return PropFilter{}, fmt.Errorf("unknown property %q", f.Key)
	}
	if f.Op == PropOpContains || f.Op == PropOpEmpty {
		if f.Op == PropOpEmpty {
			f.Value = ""
		}
		return f, nil
	}
	switch def.Type {
	case PropCheckbox, PropMultiSelect, PropMultiActor:
		return PropFilter{}, fmt.Errorf("operator %q not applicable to %s property %q", f.Op, def.Type, f.Key)
	case PropNumber:
		if _, err := strconv.ParseFloat(strings.TrimSpace(f.Value), 64); err != nil {
			return PropFilter{}, fmt.Errorf("number property %q requires a numeric value, got %q", f.Key, f.Value)
		}
		f.Numeric = true
	}
	return f, nil
}

func SortedPropKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
