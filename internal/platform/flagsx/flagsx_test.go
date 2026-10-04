package flagsx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlagsx_BucketStable(t *testing.T) {
	cases := []struct {
		key, user string
		want      int
	}{
		{"skills.sh_import", "u-alice", 94},
		{"skills.sh_import", "u-bob", 1},
		{"chat.quickactions", "u-alice", 83},
		{"chat.quickactions", "u-bob", 72},
		{"chat.quickactions", "u-carol", 2},
		{"ab", "c", 67},
		{"a", "bc", 11},
	}
	for _, c := range cases {
		got := bucket(c.key, c.user)
		assert.Equal(t, c.want, got, "bucket(%q,%q)", c.key, c.user)
		assert.Equal(t, got, bucket(c.key, c.user), "repeated calls must return the same value")
	}
}

func TestFlagsx_EvalOrderDenyAllowPercentDefault(t *testing.T) {
	static := StaticProvider{
		"k": {
			DeniedUsers: []string{"denied"},
			AllowUsers:  []string{"allowed"},
			Percent:     100,
			Default:     false,
		},
	}
	cases := []struct {
		user string
		want bool
		note string
	}{
		{"denied", false, "Deny takes priority over Percent=100 and everything"},
		{"allowed", true, "Allow hit means on"},
		{"other", true, "Percent=100 full coverage (no Deny/Allow hit)"},
	}
	for _, c := range cases {
		v, hit := static.Lookup("k", c.user)
		assert.True(t, hit)
		assert.Equal(t, c.want, v, c.note)
	}

	off := StaticProvider{"k": {Percent: 0, Default: true}}
	v, hit := off.Lookup("k", "anyone")
	require.True(t, hit)
	assert.True(t, v, "Percent=0 should fall to Default=true")
}

func TestFlagsx_PercentBoundaries(t *testing.T) {
	full := StaticProvider{"k": {Percent: 100}}
	for _, u := range []string{"u1", "u2", "u3", "u4", "u5"} {
		v, hit := full.Lookup("k", u)
		require.True(t, hit)
		assert.True(t, v, "percent=100 user=%s", u)
	}

	half := StaticProvider{"chat.quickactions": {Percent: 50, Default: false}}
	assert.True(t, mustLookup(t, half, "chat.quickactions", "u-carol"))
	assert.False(t, mustLookup(t, half, "chat.quickactions", "u-alice"))
	assert.False(t, mustLookup(t, half, "chat.quickactions", "u-bob"))
}

func mustLookup(t *testing.T, p Provider, key, user string) bool {
	t.Helper()
	v, hit := p.Lookup(key, user)
	require.True(t, hit)
	return v
}

func TestFlagsx_EnvOverridesYAML(t *testing.T) {
	env := map[string]string{"FF_CHAT_QUICKACTIONS": "off"}
	static := StaticProvider{"chat.quickactions": {Default: true}}

	svc := NewService(EnvProvider{Getenv: func(k string) string { return env[k] }}, static)
	assert.False(t, svc.Enabled("chat.quickactions", "u1"), "env off should override YAML default on")

	env["FF_CHAT_QUICKACTIONS"] = "on"
	assert.True(t, svc.Enabled("chat.quickactions", "u1"))

	delete(env, "FF_CHAT_QUICKACTIONS")
	assert.True(t, svc.Enabled("chat.quickactions", "u1"), "env miss falls back to YAML")

	env["FF_CHAT_QUICKACTIONS"] = "yes!"
	assert.True(t, svc.Enabled("chat.quickactions", "u1"), "invalid env values fall back to YAML")
}

func TestFlagsx_DenyBeatsPercent(t *testing.T) {
	svc := NewService(StaticProvider{
		"k": {Percent: 100, DeniedUsers: []string{"vip-blocked"}},
	})
	assert.False(t, svc.Enabled("k", "vip-blocked"))
	assert.True(t, svc.Enabled("k", "normal-user"))
}

func TestFlagsx_NilServiceSafe(t *testing.T) {
	var svc *Service
	assert.NotPanics(t, func() {
		assert.False(t, svc.Enabled("anything", "anyone"))
	})
}

func TestFlagsx_UnknownKeyDefaultsFalse(t *testing.T) {
	svc := NewService(StaticProvider{"other": {Default: true}})
	assert.False(t, svc.Enabled("nope", "u"))
}

func TestFlagsx_ParseSubset(t *testing.T) {
	raw := []byte(`
# static rules (ORKOME_FEATURE_FLAGS_FILE minimal YAML subset)
chat.quickactions:
  default: false
  percent: 20
  allow: [u-alice, u-bob]
  deny: [u-carol]

skills.sh_import:
  default: true
  allow:
    - u-dave
    - u-eve
`)
	rules, err := Parse(raw)
	require.NoError(t, err)
	require.Len(t, rules, 2)

	qa := rules["chat.quickactions"]
	assert.False(t, qa.Default)
	assert.Equal(t, 20, qa.Percent)
	assert.Equal(t, []string{"u-alice", "u-bob"}, qa.AllowUsers)
	assert.Equal(t, []string{"u-carol"}, qa.DeniedUsers)

	si := rules["skills.sh_import"]
	assert.True(t, si.Default)
	assert.Equal(t, []string{"u-dave", "u-eve"}, si.AllowUsers, "  - item list row")
}

func TestFlagsx_ParseErrors(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"unknown field", "k:\n  bogus: 1\n"},
		{"percent out of range", "k:\n  percent: 101\n"},
		{"percent not a number", "k:\n  percent: abc\n"},
		{"default not a boolean", "k:\n  default: maybe\n"},
		{"top-level fields have no key", "default: true\n"},
		{"bare top-level row", "k: v\n"},
		{"list items drift outside the field", "k:\n- stray\n"},
		{"duplicate key", "k:\n  default: true\nk:\n  default: false\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.body))
			assert.Error(t, err)
		})
	}
}

func TestFlagsx_LoadFile(t *testing.T) {
	t.Run("empty path = not configured", func(t *testing.T) {
		rules, err := LoadFile("")
		require.NoError(t, err)
		assert.Nil(t, rules)
	})

	t.Run("loaded normally", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.yaml")
		require.NoError(t, os.WriteFile(path, []byte("k:\n  default: true\n"), 0o600))
		rules, err := LoadFile(path)
		require.NoError(t, err)
		v, hit := rules.Lookup("k", "u")
		require.True(t, hit)
		assert.True(t, v)
	})

	t.Run("file does not exist, fail-fast", func(t *testing.T) {
		_, err := LoadFile(filepath.Join(t.TempDir(), "nope.yaml"))
		assert.Error(t, err)
	})
}
