package app

import "testing"

func eqMap(t *testing.T, name string, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: unequal length got=%v want=%v", name, got, want)
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("%s: missing key %q got=%v", name, k, got)
		}
		if wn, ok := v.(map[string]any); ok {
			gn, ok := gv.(map[string]any)
			if !ok {
				t.Fatalf("%s: key %q should be a map got=%#v", name, k, gv)
			}
			eqMap(t, name+"."+k, gn, wn)
			continue
		}
		if gv != v {
			t.Fatalf("%s: key %q got=%#v want=%#v", name, k, gv, v)
		}
	}
}

func TestMergeRuntimeConfigMissingEnvInherits(t *testing.T) {
	existing := map[string]any{
		"provider": "openai", "model": "gpt-4o",
		"env": map[string]any{"GITHUB_TOKEN": "gh_real"},
	}
	incoming := map[string]any{"provider": "openai", "model": "gpt-4.1"}
	got := mergeRuntimeConfig(existing, incoming)
	eqMap(t, "env inheritance", got, map[string]any{
		"provider": "openai", "model": "gpt-4.1",
		"env": map[string]any{"GITHUB_TOKEN": "gh_real"},
	})
}

func TestMergeRuntimeConfigMaskedTopLevelUnchanged(t *testing.T) {
	existing := map[string]any{"provider": "openai", "api_key": "sk_real"}
	incoming := map[string]any{"provider": "openai", "api_key": MaskedValue}
	got := mergeRuntimeConfig(existing, incoming)
	eqMap(t, "top-level mask backfill", got, map[string]any{"provider": "openai", "api_key": "sk_real"})
}

func TestMergeRuntimeConfigMaskedNestedEnvAndParams(t *testing.T) {
	existing := map[string]any{
		"provider": "openai",
		"env":      map[string]any{"TOKEN": "real-token", "KEEP": "v1"},
		"params":   map[string]any{"api_key": "sk_real"},
	}

	incoming := map[string]any{
		"provider": "openai",
		"env":      map[string]any{"TOKEN": MaskedValue, "KEEP": MaskedValue},
		"params":   map[string]any{"api_key": MaskedValue},
	}
	got := mergeRuntimeConfig(existing, incoming)
	eqMap(t, "nested mask backfill", got, map[string]any{
		"provider": "openai",
		"env":      map[string]any{"TOKEN": "real-token", "KEEP": "v1"},
		"params":   map[string]any{"api_key": "sk_real"},
	})
}

func TestMergeRuntimeConfigNewValueWins(t *testing.T) {
	existing := map[string]any{
		"provider": "openai", "model": "gpt-4o",
		"env": map[string]any{"A": "1"},
	}
	incoming := map[string]any{
		"provider": "openai", "model": "gpt-4.1",
		"env": map[string]any{"A": "2", "B": "new"},
	}
	got := mergeRuntimeConfig(existing, incoming)
	eqMap(t, "explicit new values overwrite", got, map[string]any{
		"provider": "openai", "model": "gpt-4.1",
		"env": map[string]any{"A": "2", "B": "new"},
	})
}

func TestMergeRuntimeConfigNilIncomingInheritsWhole(t *testing.T) {
	existing := map[string]any{"provider": "openai", "model": "gpt-4o"}
	got := mergeRuntimeConfig(existing, nil)
	eqMap(t, "nil inherits wholesale", got, existing)
}

func TestMergeRuntimeConfigStringMapEnv(t *testing.T) {

	existing := map[string]any{"provider": "openai", "env": map[string]any{"K": "real"}}
	incoming := map[string]any{
		"provider": "openai",
		"env":      map[string]string{"K": MaskedValue},
	}
	got := mergeRuntimeConfig(existing, incoming)
	env, _ := got["env"].(map[string]any)
	if env == nil || env["K"] != "real" {
		t.Fatalf("map[string]string mask not backfilled: %#v", got["env"])
	}
}

func TestMaskSecretsDeepMasksEnvAndParams(t *testing.T) {
	rc := map[string]any{
		"provider": "openai", "model": "gpt-4o", "credential_id": "uuid-x",
		"env":      map[string]any{"GITHUB_TOKEN": "gh_real", "PLAIN": "ok"},
		"params":   map[string]any{"api_key": "sk_real", "temperature": float64(1)},
		"base_url": "https://gw.example",
	}
	out := MaskSecrets(rc)
	if out["credential_id"] != "uuid-x" {
		t.Fatal("reference keys should not be masked")
	}
	env := out["env"].(map[string]any)
	if env["GITHUB_TOKEN"] != MaskedValue {
		t.Fatal("env.GITHUB_TOKEN should be deeply masked")
	}
	if env["PLAIN"] != "ok" {
		t.Fatal("non-sensitive env keys should be kept")
	}
	params := out["params"].(map[string]any)
	if params["api_key"] != MaskedValue || params["temperature"] != float64(1) {
		t.Fatal("params masking/keep incorrect")
	}
	if rc["env"].(map[string]any)["GITHUB_TOKEN"] != "gh_real" {
		t.Fatal("Sanitized must return a copy, not rewrite the original map")
	}
}
