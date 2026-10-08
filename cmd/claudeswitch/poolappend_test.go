package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

const poolConfig = `# profiles, hand-written
switch_at = 85
priority = ["a", "w1"]

[[account]]
id = "a"

[[account]]
id = "w1"

[[profile]]
name = "default"
pool = ["a"]

[[profile]]
name = "work"   # the work laptop profile
dir  = "~/.claude-work"
pool = ["w1"]
switch_at = 75
`

// D20: a new account signed in through profile P joins P's pool. The edit
// writes the account block and the pool entry together, and says so.
func TestRecordSeatPutsANewAccountInTheProfilesPool(t *testing.T) {
	path := writeConfig(t, poolConfig)
	msg, err := recordSeat(loadOrFail(t, path), "w2", "work", gotSeat("p2", "o2"), "work")
	if err != nil {
		t.Fatal(err)
	}
	after := loadOrFail(t, path)
	if owner, _ := after.ProfileOf("w2"); owner != "work" {
		t.Errorf("w2 is in %q's pool, want work's", owner)
	}
	if got := after.SeatOf("w2"); got != "p2@o2" {
		t.Errorf("w2 pinned to %q", got)
	}
	in, _ := after.ProfileNamed("work")
	if strings.Join(in.Pool, ",") != "w1,w2" || in.SwitchAt != 75 {
		t.Errorf("work after the edit: pool %v switch_at %v", in.Pool, in.SwitchAt)
	}
	if !strings.Contains(msg, `"work"`) || !strings.Contains(msg, "pool") {
		t.Errorf("the message must say which pool it joined: %q", msg)
	}
	raw, _ := os.ReadFile(path)
	for _, keep := range []string{"# profiles, hand-written", "# the work laptop profile"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("comment %q lost:\n%s", keep, raw)
		}
	}
}

// With no profile named "default", an account in no pool is a config error
// (D6), so the block alone would not load: the pool entry is what makes the
// edit valid, and both land in one write.
func TestRecordSeatWithNoDefaultProfileStillAddsTheAccount(t *testing.T) {
	body := strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)
	path := writeConfig(t, body)
	if _, err := recordSeat(loadOrFail(t, path), "w2", "work", gotSeat("p2", "o2"), "work"); err != nil {
		t.Fatalf("adding an account to work's pool failed: %v", err)
	}
	if owner, _ := loadOrFail(t, path).ProfileOf("w2"); owner != "work" {
		t.Errorf("w2 in %q's pool", owner)
	}
}

// A pool written across lines, with comments, and a profile with no pool
// line at all.
func TestRecordSeatEditsPoolsHoweverTheyAreWritten(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"multi-line": {strings.Replace(poolConfig, `pool = ["w1"]`, "pool = [\n  \"w1\", # first\n]", 1), "w1,w2"},
		"multi-line no trailing comma": {strings.Replace(poolConfig, `pool = ["w1"]`,
			"pool = [\n  \"w1\"  # first\n  ]", 1), "w1,w2"},
		"empty": {strings.Replace(strings.Replace(strings.Replace(poolConfig, `pool = ["w1"]`, `pool = []`, 1),
			"[[account]]\nid = \"w1\"\n", "", 1), `, "w1"`, "", 1), "w2"},
		"none": {strings.Replace(strings.Replace(strings.Replace(poolConfig, "pool = [\"w1\"]\n", "", 1),
			"[[account]]\nid = \"w1\"\n", "", 1), `, "w1"`, "", 1), "w2"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, c.body)
			if _, err := recordSeat(loadOrFail(t, path), "w2", "work", gotSeat("p2", "o2"), "work"); err != nil {
				t.Fatal(err)
			}
			in, _ := loadOrFail(t, path).ProfileNamed("work")
			if got := strings.Join(in.Pool, ","); got != c.want {
				raw, _ := os.ReadFile(path)
				t.Errorf("pool %q, want %q:\n%s", got, c.want, raw)
			}
		})
	}
}

// Without [[profile]] blocks there is no pool to edit: the account joins the
// implicit profile as before, and no profile block appears.
func TestRecordSeatWithoutProfilesWritesNoPool(t *testing.T) {
	path := writeConfig(t, baseConfig)
	if _, err := recordSeat(loadOrFail(t, path), "work-b", "work", gotSeat("p3", "o3"), ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "pool") || strings.Contains(string(raw), "[[profile]]") {
		t.Errorf("a pool was written into a config without profiles:\n%s", raw)
	}
}

// The edit keeps the file's mode and writes through a symlink, like every
// other config edit.
func TestRecordSeatPoolEditKeepsModeAndSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles-config.toml")
	if err := os.WriteFile(real, []byte(poolConfig), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := recordSeat(loadOrFail(t, link), "w2", "work", gotSeat("p2", "o2"), "work"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", fi.Mode().Perm())
	}
	if owner, _ := loadOrFail(t, link).ProfileOf("w2"); owner != "work" {
		t.Errorf("w2 in %q's pool", owner)
	}
}

// The notice before a login no longer sends the person to edit the pool by
// hand: it says where the account will go.
func TestNewAccountNoticeNamesThePoolItJoins(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, poolConfig))
	work, _ := cfg.ProfileNamed("work")
	got := newAccountNotice(cfg, work, "w2", "work")
	if !strings.Contains(got, `"work"`) || !strings.Contains(got, "pool") {
		t.Errorf("notice does not name work's pool:\n%s", got)
	}
	if strings.Contains(got, "add it to that profile's pool") || strings.Contains(got, `joins the "default"`) {
		t.Errorf("notice still sends the person to edit the pool:\n%s", got)
	}
	// No profiles: nothing about pools.
	plain := loadOrFail(t, writeConfig(t, baseConfig))
	if got := newAccountNotice(plain, config.Profile{Name: "default", FromEnv: true}, "x", "work"); strings.Contains(got, "pool") {
		t.Errorf("a config without profiles mentions pools:\n%s", got)
	}
}
