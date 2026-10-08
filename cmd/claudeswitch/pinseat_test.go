package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// The seat a login actually produced, as vault.Store hands it back.
func gotSeat(uuid, org string) *vault.Entry {
	return &vault.Entry{AccountUUID: uuid, OrgID: org}
}

func loadOrFail(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// 2026-10-07: adding an account meant writing an [[account]] block by hand
// first, and a hand-written block cannot carry the seat, which is only knowable
// after signing in. A login to a name the config has never heard of now writes
// the block itself, pinned to what the login verified.
func TestRecordSeatAppendsAPinnedBlockForANewAccount(t *testing.T) {
	path := writeConfig(t, baseConfig)
	cfg := loadOrFail(t, path)

	msg, err := recordSeat(cfg, "work-b", gotSeat("person-3", "org-3"), "")
	if err != nil {
		t.Fatal(err)
	}
	after := loadOrFail(t, path)
	if got := after.SeatOf("work-b"); got != "person-3@org-3" {
		t.Errorf("the new block must be pinned to the seat the login produced, got %q", got)
	}
	ord := after.Priority
	if len(ord) == 0 || ord[len(ord)-1] != "work-b" {
		t.Errorf("the new account must be named last in priority, got %v", ord)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "scope") {
		t.Errorf("a new block has no scope line (lane 16):\n%s", raw)
	}
	if !strings.Contains(msg, "work-b") || !strings.Contains(msg, "pinned") {
		t.Errorf("the message must say what was written: %q", msg)
	}
}

const unpinnedConfig = `# hand-written; keep this comment
switch_at = 85

priority = ["work-a", "fresh"]

[[account]]
id           = "work-a"
scope        = "work"
account_uuid = "person-1"
org_id       = "org-1"

[[account]]
id    = "fresh"   # added by hand, before its seat was known
scope = "work"

[project."~/code"]
eligible = ["work"]
`

// The daemon refuses to sync an unpinned account ("no account_uuid in the
// config"), so leaving the block as written after a verified login leaves the
// account half-added. The login knows the seat; it writes it in place.
func TestRecordSeatPinsAKnownUnpinnedAccountInPlace(t *testing.T) {
	path := writeConfig(t, unpinnedConfig)
	if _, err := recordSeat(loadOrFail(t, path), "fresh", gotSeat("person-5", "org-5"), ""); err != nil {
		t.Fatal(err)
	}
	after := loadOrFail(t, path)
	if got := after.SeatOf("fresh"); got != "person-5@org-5" {
		t.Errorf("seat = %q, want person-5@org-5", got)
	}
	n := 0
	for _, a := range after.Accounts {
		if a.ID == "fresh" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("pinning must edit the existing block, not add a second one: %d blocks", n)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{"# hand-written; keep this comment",
		"# added by hand, before its seat was known", `[project."~/code"]`,
		`priority = ["work-a", "fresh"]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("lost %q:\n%s", want, raw)
		}
	}
	if after.Legacy().Projects != 1 {
		t.Errorf("the legacy project table after the block must survive the edit, got %+v", after.Legacy())
	}
}

func TestRecordSeatLeavesAPinnedAccountAlone(t *testing.T) {
	path := writeConfig(t, baseConfig)
	msg, err := recordSeat(loadOrFail(t, path), "work-a", gotSeat("person-1", "org-1"), "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != baseConfig {
		t.Errorf("an already-pinned account must not change the file:\n%s", raw)
	}
	if msg != "" {
		t.Errorf("nothing was written, so nothing to say; got %q", msg)
	}
}

// An organization pinned by hand that disagrees with what came back is not
// something to overwrite silently: one of the two is wrong, and only the person
// knows which.
func TestRecordSeatRefusesToOverwriteADisagreeingHalfPin(t *testing.T) {
	body := strings.Replace(unpinnedConfig, `scope = "work"`+"\n\n[project",
		`scope = "work"`+"\norg_id = \"org-OTHER\"\n\n[project", 1)
	path := writeConfig(t, body)
	if _, err := recordSeat(loadOrFail(t, path), "fresh", gotSeat("person-5", "org-5"), ""); err == nil {
		t.Fatal("a hand-pinned organization that disagrees must be refused")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != body {
		t.Errorf("the config must be untouched:\n%s", raw)
	}
}

// The command itself used to refuse before signing in at all:
// `"x" is not in config.toml; add an [[account]] block for it first`.
func TestLoginAcceptsAnAccountTheConfigDoesNotKnow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeConfig(t, baseConfig)

	if err := cmdLogin([]string{"brand-new", "--config", path, "--direct"}); err != nil {
		t.Fatalf("login must accept a name the config does not have yet: %v", err)
	}
	_, p, err := oauth.LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	if p.AccountID != "brand-new" {
		t.Errorf("the pending login must remember the name for --code, got %+v", p)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "claudeswitch", "pending-login.json")); err != nil {
		t.Errorf("the attempt must be saved under the temporary HOME: %v", err)
	}
}
