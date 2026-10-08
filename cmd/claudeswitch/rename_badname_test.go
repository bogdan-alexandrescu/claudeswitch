package main

import (
	"os"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// fakeVaultStore replaces the keychain functions with a map for one test.
func fakeVaultStore(t *testing.T) map[string]*keychain.Blob {
	t.Helper()
	store := map[string]*keychain.Blob{}
	r, w, d := keychain.Read, keychain.Write, keychain.Delete
	t.Cleanup(func() { keychain.Read, keychain.Write, keychain.Delete = r, w, d })
	keychain.Read = func(name string) (*keychain.Blob, error) {
		if b, ok := store[name]; ok {
			cp := *b
			return &cp, nil
		}
		return nil, keychain.ErrNotFound
	}
	keychain.Write = func(name string, b *keychain.Blob) error {
		cp := *b
		store[name] = &cp
		return nil
	}
	keychain.Delete = func(name string) error {
		delete(store, name)
		return nil
	}
	return store
}

const badNameConfig = `# mine
priority = ["me+work", "home"]

[[account]]
id           = "me+work"
account_uuid = "u1"
org_id       = "o1"

[[account]]
id = "home"

[[profile]]
name = "default"
pool = ["home"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = [
  "me+work", # the work seat
]
`

// Owner decision (lane 7 review): an id the new rule refuses is fixed with
// `cs rename`, which must work on the config that no longer loads: the id in
// its block, priority and pools, the state records, and the vault item.
func TestRenameFixesABadIDEverywhere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{
		ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-w", RefreshToken: "r-w"},
		Meta:          &keychain.Meta{AccountID: "me+work", AccountUUID: "u1", OrgID: "o1"},
	}
	path := writeConfig(t, badNameConfig)
	if _, err := config.Load(path); err == nil {
		t.Fatal("setup: the bad id loaded")
	}
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("me+work").OrgID = "o1"
	st.Profile("work").SetActive("me+work")
	st.AddVaulted("me+work")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	if err := cmdRename([]string{"--config", path, "me+work", "me-work"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the renamed config does not load: %v", err)
	}
	if cfg.SeatOf("me-work") != "u1@o1" {
		t.Errorf("me-work's pin: %q", cfg.SeatOf("me-work"))
	}
	if owner, _ := cfg.ProfileOf("me-work"); owner != "work" {
		t.Errorf("me-work is in %q's pool, want work's", owner)
	}
	if strings.Join(cfg.Priority, ",") != "me-work,home" {
		t.Errorf("priority %v", cfg.Priority)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "me+work") || !strings.Contains(string(raw), "# the work seat") {
		t.Errorf("config after rename:\n%s", raw)
	}

	if _, ok := store[keychain.VaultService("me+work")]; ok {
		t.Error("the old vault item is still there")
	}
	moved := store[keychain.VaultService("me-work")]
	if moved == nil || moved.ClaudeAIOAuth.AccessToken != "tok-w" || moved.Meta.AccountID != "me-work" ||
		moved.Meta.Seat() != "u1@o1" {
		t.Fatalf("vault item after rename: %+v", moved)
	}

	after, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	if after.Profile("work").Active != "me-work" {
		t.Errorf("work's active: %q", after.Profile("work").Active)
	}
	if _, ok := after.Accounts["me+work"]; ok {
		t.Error("state still has me+work")
	}
	if strings.Join(after.Vaulted, ",") != "me-work" {
		t.Errorf("vaulted %v", after.Vaulted)
	}
}

// The credential is never lost: an existing target is refused before
// anything moves.
func TestRenameRefusesATakenNameAndKeepsTheCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-w"}}
	store[keychain.VaultService("me-work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "other"}}
	path := writeConfig(t, badNameConfig)
	if err := cmdRename([]string{"--config", path, "me+work", "me-work"}); err == nil {
		t.Fatal("renamed onto an existing vault entry")
	}
	if store[keychain.VaultService("me+work")] == nil || store[keychain.VaultService("me-work")].ClaudeAIOAuth.AccessToken != "other" {
		t.Error("a refused rename changed the vault")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != badNameConfig {
		t.Error("a refused rename changed the config")
	}
}

// A suggested name is always one the config would load.
func TestSuggestNameOnlyProposesValidIDs(t *testing.T) {
	for _, c := range []struct{ email, org, want string }{
		{"you+work@x.io", "", "you-work"},
		{"Jane.Doe+cs@x.io", "", "jane.doe-cs"},
		{"a@x.io", "Ünïcode   Corp!", "n-code-corp"},
		{"+++@x.io", "", "account"},
	} {
		pr := &usage.Profile{}
		pr.Account.Email, pr.Organization.Name = c.email, c.org
		got := suggestName(pr, nil)
		if err := config.ValidName("account id", got); err != nil {
			t.Errorf("%s/%s: suggested %q: %v", c.email, c.org, got, err)
		}
		if got != c.want {
			t.Errorf("%s/%s: suggested %q, want %q", c.email, c.org, got, c.want)
		}
	}
	pr := &usage.Profile{}
	pr.Account.Email = "you+work@x.io"
	taken := map[string]string{"you-work": "you-work", "you-work-2": "you-work-2"}
	if got := suggestName(pr, taken); got != "you-work-3" {
		t.Errorf("dedupe: %q", got)
	}
}
