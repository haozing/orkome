package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	PropText        = "text"
	PropURL         = "url"
	PropNumber      = "number"
	PropCheckbox    = "checkbox"
	PropDate        = "date"
	PropSelect      = "select"
	PropMultiSelect = "multi_select"
	PropActor       = "actor"
	PropMultiActor  = "multi_actor"
)

type PropertyDef struct {
	Key     string
	Name    string
	Type    string
	Options []string
}

func ValidateWorkItemProperties(defs []PropertyDef, props map[string]any) (map[string]any, error) {
	defBy := map[string]PropertyDef{}
	for _, d := range defs {
		defBy[d.Key] = d
	}
	out := map[string]any{}
	for k, v := range props {
		d, ok := defBy[k]
		if !ok {
			return nil, fmt.Errorf("unknown property %q", k)
		}
		norm, err := validateOne(d, v)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", k, err)
		}
		if norm != nil {
			out[k] = norm
		}
	}
	return out, nil
}

func validateOne(d PropertyDef, v any) (any, error) {
	switch d.Type {
	case PropText:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want string")
		}
		if len([]rune(s)) > 2000 {
			return nil, fmt.Errorf("text exceeds 2000 runes")
		}
		if s == "" {
			return nil, nil
		}
		return s, nil
	case PropURL:
		s, ok := v.(string)
		if !ok || len(s) > 2048 {
			return nil, fmt.Errorf("want url string ≤2048")
		}
		if s == "" {
			return nil, nil
		}
		if !strings.Contains(s, "://") {
			return nil, fmt.Errorf("malformed url")
		}
		return s, nil
	case PropNumber:
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("want number")
		}
		return f, nil
	case PropCheckbox:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("want boolean")
		}
		return b, nil
	case PropDate:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want date string")
		}

		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, fmt.Errorf("want YYYY-MM-DD")
		}
		return s, nil
	case PropSelect:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want string")
		}
		if !contains(d.Options, s) {
			return nil, fmt.Errorf("option %q not in definition", s)
		}
		return s, nil
	case PropMultiSelect:
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("want array")
		}
		var out []string
		for _, e := range arr {
			s, _ := e.(string)
			if !contains(d.Options, s) {
				return nil, fmt.Errorf("option %q not in definition", s)
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, nil
		}

		rank := map[string]int{}
		for i, o := range d.Options {
			rank[o] = i
		}
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && rank[out[j]] < rank[out[j-1]]; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
		return out, nil
	case PropActor:

		ref, err := parseActorRef(v)
		if err != nil {
			return nil, err
		}
		return ref, nil
	case PropMultiActor:
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("want array")
		}
		if len(arr) > 20 {
			return nil, fmt.Errorf("multi_actor exceeds 20")
		}
		out := make([]string, 0, len(arr))
		seen := map[string]bool{}
		for _, e := range arr {
			ref, err := parseActorRef(e)
			if err != nil {
				return nil, err
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown type %q", d.Type)
}

func parseActorRef(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf(`want "member:<uuid>" string`)
	}
	kind, id, found := strings.Cut(s, ":")
	if !found || kind != "member" {
		return "", fmt.Errorf(`want "member:<uuid>"`)
	}
	uid, ok := ParseUUID(id)
	if !ok {
		return "", fmt.Errorf(`actor id in %q must be a canonical uuid`, s)
	}
	return "member:" + uid, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
