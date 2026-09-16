package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseConfig = `# claudeswitch — hand-edited, with comments worth keeping.
switch_at        = 85
switch_at_weekly = 98
hard_floor       = 99

# Rotation order. Earlier accounts are spent first.
priority = ["work-a", "personal"]

[[account]]
id           = "work-a"
scope        = "work"
account_uuid = "person-1"
org_id       = "org-1"

[[account]]
id           = "personal"
scope        = "personal"
account_uuid = "person-2"
org_id       = "org-2"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const newBlock = "\n[[account]]\nid           = \"work-b\"\nscope        = \"work\"\n" +
	"account_uuid = \"person-3\"\norg_id       = \"org-3\"\n"

func TestAppendAccountAddsTheBlockAndNamesItLastInPriority(t *testing.T) {
	path := writeConfig(t, baseConfig)
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	if !strings.Contains(out, `priority = ["work-a", "personal", "work-b"]`) {
		t.Errorf("the new account must go last in the priority list:\n%s", out)
	}
	if !strings.Contains(out, `account_uuid = "person-3"`) {
		t.Errorf("the block must be written:\n%s", out)
	}
}

// A regenerated config would lose these. The edit is textual precisely so a
// hand-maintained file survives it.
func TestAppendAccountKeepsCommentsAndEverythingElse(t *testing.T) {
	path := writeConfig(t, baseConfig)
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	for _, want := range []string{
		"# claudeswitch — hand-edited, with comments worth keeping.",
		"# Rotation order. Earlier accounts are spent first.",
		"switch_at_weekly = 98",
		"hard_floor       = 99",
		`id           = "work-a"`,
		`id           = "personal"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lost %q from the file:\n%s", want, got)
		}
	}
}

func TestAppendAccountHandlesAnEmptyPriorityList(t *testing.T) {
	path := writeConfig(t, strings.Replace(baseConfig,
		`priority = ["work-a", "personal"]`, `priority = []`, 1))
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `priority = ["work-b"]`) {
		t.Errorf("an empty list must not gain a stray separator:\n%s", got)
	}
}

// No priority line at all is legal: Ordered() falls back to config order. The
// block still has to land.
func TestAppendAccountWorksWithNoPriorityLine(t *testing.T) {
	path := writeConfig(t, strings.Replace(baseConfig,
		`priority = ["work-a", "personal"]`, "", 1))
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `id           = "work-b"`) {
		t.Errorf("the block must still be written:\n%s", got)
	}
}

// The file on disk must never be one this could not produce cleanly, so the
// edit is parsed back before it is moved into place.
func TestAppendAccountLeavesTheOriginalAloneWhenTheResultWillNotLoad(t *testing.T) {
	path := writeConfig(t, baseConfig)
	err := appendAccount(path, "work-b", "\n[[account]\nid = broken\n")
	if err == nil {
		t.Fatal("a malformed block must be refused")
	}
	got, _ := os.ReadFile(path)
	if string(got) != baseConfig {
		t.Errorf("the original config must be untouched:\n%s", got)
	}
	if _, err := os.Stat(path + ".claudeswitch-new"); !os.IsNotExist(err) {
		t.Error("the temporary file must be cleaned up")
	}
}

// Liveness is decided by comparing access tokens, so two entries holding one
// credential are both "live" at once. `remove` refused a live entry, and no
// account could be switched to that released it — the other copy held the
// identical token — so the duplicate state `doctor` calls corrupted could not
// be repaired by the command that repairs it.
func TestFindTwinSpotsTwoNamesForOneCredential(t *testing.T) {
	tokens := map[string]string{
		"work-a": "shared-token", "work-b": "shared-token", "work-c": "its-own-token",
	}
	ids := []string{"work-a", "work-b", "work-c", "never-vaulted"}
	tokenOf := func(id string) string { return tokens[id] }

	if got := findTwin("work-a", ids, tokenOf); got != "work-b" {
		t.Errorf("work-a shares its credential with work-b, got %q", got)
	}
	if got := findTwin("work-b", ids, tokenOf); got != "work-a" {
		t.Errorf("the relationship is symmetric, got %q", got)
	}
	if got := findTwin("work-c", ids, tokenOf); got != "" {
		t.Errorf("an account holding its own credential has no twin, got %q", got)
	}
}

// An unvaulted account has no token, and an empty string must not match every
// other unvaulted one — that would report a twin for accounts holding nothing
// and let a live entry be deleted on the strength of it.
func TestFindTwinTreatsAnUnvaultedAccountAsNoMatch(t *testing.T) {
	tokens := map[string]string{"work-a": "a-token"}
	ids := []string{"work-a", "never-vaulted", "also-never-vaulted"}
	tokenOf := func(id string) string { return tokens[id] }

	if got := findTwin("never-vaulted", ids, tokenOf); got != "" {
		t.Errorf("two accounts with no credential are not twins, got %q", got)
	}
	if got := findTwin("work-a", ids, tokenOf); got != "" {
		t.Errorf("a sole credential has no twin, got %q", got)
	}
}
