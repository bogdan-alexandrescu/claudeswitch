package main

import (
	"bytes"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

func TestUseJSONRefusesAcrossPoolsWithACode(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	err := cmdUse([]string{"--config", path, "w1", "--json"})
	if got := errCode(t, err); got != codeOutsidePool {
		t.Errorf("code %q (%v)", got, err)
	}
}

func TestProfileListJSON(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) {
		if in.Name == "work" {
			return nil, keychain.ErrNotFound
		}
		return fakeLive{"def-item"}, nil
	}
	var buf bytes.Buffer
	if err := profileListJSON(&buf, path); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	list := m["profiles"].([]any)
	if len(list) != 2 {
		t.Fatalf("profiles %v", list)
	}
	work := list[1].(map[string]any)
	if work["name"] != "work" || work["signed_in"] != "no" || work["dir"] != "~/.claude-work" ||
		work["overrides"].(map[string]any)["switch_at"] != "75" {
		t.Errorf("work: %v", work)
	}
	if def := list[0].(map[string]any); def["signed_in"] != "yes" || def["dir"] != nil {
		t.Errorf("default: %v", def)
	}
}

func TestEditsWithNoConfigSaySo(t *testing.T) {
	appWorld(t, "")
	missing := t.TempDir() + "/none.toml"
	var buf bytes.Buffer
	if got := errCode(t, setPriority(&buf, missing, []string{"a1"}, true)); got != codeNoConfig {
		t.Errorf("priority: %q", got)
	}
	if got := errCode(t, profileSet(&buf, missing, "work", "switch_at", "70", true)); got != codeNoConfig {
		t.Errorf("profile set: %q", got)
	}
}

func TestVaultedAccountJSON(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	acctSeams.describe = func(string) string { return "someone@example.com" }
	acctSeams.plan = func(string) string { return "Max" }
	m := vaultedAccountJSON(path, "w2", &vault.Entry{AccountUUID: "person-2", OrgID: "org-2"}, false)
	if m["seat"] != "person-2@org-2" || m["profile"] != "work" || m["email"] != "someone@example.com" ||
		m["plan"] != "Max" || m["configured"] != true || m["renewable"] != true {
		t.Errorf("got %v", m)
	}
	if pool, _ := m["pool"].([]string); len(pool) != 2 {
		t.Errorf("pool %v", m["pool"])
	}
}
