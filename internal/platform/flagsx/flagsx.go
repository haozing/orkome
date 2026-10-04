package flagsx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Provider interface {
	Lookup(key, user string) (value, hit bool)
}

type Service struct{ providers []Provider }

func NewService(providers ...Provider) *Service { return &Service{providers: providers} }

func (s *Service) Enabled(key, user string) bool {
	if s == nil {
		return false
	}
	for _, p := range s.providers {
		if v, hit := p.Lookup(key, user); hit {
			return v
		}
	}
	return false
}

type EnvProvider struct {
	Getenv func(string) string
}

func (e EnvProvider) Lookup(key, _ string) (bool, bool) {
	getenv := e.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := getenv("FF_" + envKey(key))
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	}
	return false, false
}

func envKey(key string) string {
	return strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
}

type Rule struct {
	Default     bool
	Percent     int
	DeniedUsers []string
	AllowUsers  []string
}

type StaticProvider map[string]Rule

func (m StaticProvider) Lookup(key, user string) (bool, bool) {
	rule, ok := m[key]
	if !ok {
		return false, false
	}
	for _, u := range rule.DeniedUsers {
		if u == user {
			return false, true
		}
	}
	for _, u := range rule.AllowUsers {
		if u == user {
			return true, true
		}
	}
	if rule.Percent > 0 && bucket(key, user) < rule.Percent {
		return true, true
	}
	return rule.Default, true
}

const (
	fnvOffset32 uint32 = 2166136261
	fnvPrime32  uint32 = 16777619
)

func bucket(key, user string) int {
	h := fnvOffset32
	for _, b := range []byte(key + "\x00" + user) {
		h ^= uint32(b)
		h *= fnvPrime32
	}
	return int(h % 100)
}

func LoadFile(path string) (StaticProvider, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("flagsx: read %s: %w", path, err)
	}
	return Parse(raw)
}

func Parse(raw []byte) (StaticProvider, error) {
	rules := StaticProvider{}
	var curKey string
	var curList string

	for i, line := range strings.Split(string(raw), "\n") {
		ln := strings.TrimSpace(stripComment(line))
		if ln == "" {
			continue
		}
		switch {
		case !isIndented(line) && strings.HasSuffix(ln, ":"):
			curKey = strings.TrimSuffix(ln, ":")
			if curKey == "" {
				return nil, fmt.Errorf("flagsx: line %d: empty key", i+1)
			}
			if _, dup := rules[curKey]; dup {
				return nil, fmt.Errorf("flagsx: line %d: duplicate key %q", i+1, curKey)
			}
			rules[curKey] = Rule{}
			curList = ""
		case strings.HasPrefix(ln, "- "):
			if curList == "" {
				return nil, fmt.Errorf("flagsx: line %d: list item outside allow/deny", i+1)
			}
			item := strings.TrimSpace(strings.TrimPrefix(ln, "-"))
			r := rules[curKey]
			if curList == "allow" {
				r.AllowUsers = append(r.AllowUsers, item)
			} else {
				r.DeniedUsers = append(r.DeniedUsers, item)
			}
			rules[curKey] = r
		case isIndented(line):
			if curKey == "" {
				return nil, fmt.Errorf("flagsx: line %d: field before any key", i+1)
			}
			name, val, ok := strings.Cut(ln, ":")
			if !ok {
				return nil, fmt.Errorf("flagsx: line %d: want 'field: value'", i+1)
			}
			field, value := strings.TrimSpace(name), strings.TrimSpace(val)
			rule := rules[curKey]
			switch field {
			case "default":
				b, err := strconv.ParseBool(value)
				if err != nil {
					return nil, fmt.Errorf("flagsx: line %d: default want bool: %w", i+1, err)
				}
				rule.Default = b
			case "percent":
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 || n > 100 {
					return nil, fmt.Errorf("flagsx: line %d: percent want int 0-100, got %q", i+1, value)
				}
				rule.Percent = n
			case "allow", "deny":
				list, err := parseList(value)
				if err != nil {
					return nil, fmt.Errorf("flagsx: line %d: %s: %w", i+1, field, err)
				}
				if field == "allow" {
					rule.AllowUsers = list
				} else {
					rule.DeniedUsers = list
				}
			default:
				return nil, fmt.Errorf("flagsx: line %d: unknown field %q (default|percent|allow|deny)", i+1, field)
			}
			rules[curKey] = rule

			if field == "allow" || field == "deny" {
				curList = field
			} else {
				curList = ""
			}
		default:
			return nil, fmt.Errorf("flagsx: line %d: unexpected %q (want 'key:' or indented field)", i+1, ln)
		}
	}
	return rules, nil
}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func isIndented(line string) bool { return len(line) > 0 && (line[0] == ' ' || line[0] == '\t') }

func parseList(v string) ([]string, error) {
	if v == "" {
		return nil, nil
	}
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, fmt.Errorf("want [a, b] list, got %q", v)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, item := range strings.Split(inner, ",") {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
