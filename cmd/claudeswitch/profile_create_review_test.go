package main

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 10 security review, with the owner's decisions.

// 1. Dir aliasing: a new profile's dir may not be, or sit inside, the base
// ~/.claude, another profile's dir, or the implicit profile's
// CLAUDE_CONFIG_DIR, compared as real paths.
func TestProfileCreateRefusesAliasedDirs(t *testing.T) {
	home := createHome(t)
	other := filepath.Join(home, ".claude-other")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	body := createWithDefault + "\n[[profile]]\nname = \"other\"\ndir = \"~/.claude-other\"\npool = [\"a3\"]\n"
	path := writeConfig(t, body)
	toOther := filepath.Join(home, "to-other")
	toBase := filepath.Join(home, "to-base")
	os.Symlink(other, toOther)
	os.Symlink(filepath.Join(home, ".claude"), toBase)
	for _, dir := range []string{
		toOther,                                  // another profile's dir, by a link
		filepath.Join(other, "inner"),            // inside another profile's dir
		filepath.Join(home, ".claude", "w"),      // inside the base
		filepath.Join(home, ".claude", "skills"), // the base's skills: a link loop
		filepath.Join(toBase, "w"),               // inside the base, by a link
		filepath.Join(home, ".claude", "..", ".claude"),
	} {
		err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "w", dir: dir})
		if err == nil {
			t.Errorf("%s: want a refusal", dir)
		}
	}
	if got := readFile(t, path); got != body {
		t.Errorf("a refused create edited the config:\n%s", got)
	}
}

// The implicit profile's dir is this shell's CLAUDE_CONFIG_DIR, and is
// protected like any other profile's.
func TestProfileCreateRefusesTheEnvDir(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createAccounts)
	env := filepath.Join(home, "cc-env")
	os.MkdirAll(env, 0o700)
	setCCDir(t, env)
	for _, dir := range []string{env, filepath.Join(env, "inner")} {
		if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "w", dir: dir}); err == nil {
			t.Errorf("%s: want a refusal", dir)
		}
	}
}

// 3. With no [[profile]] blocks, the declared default keeps the profile the
// implicit one was: this shell's CLAUDE_CONFIG_DIR when set, as written.
func TestProfileCreateDefaultKeepsTheEnvDir(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createAccounts)
	env := filepath.Join(home, "cc-env")
	setCCDir(t, env)
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}}); err != nil {
		t.Fatal(err)
	}
	def, ok := loadOrFail(t, path).ProfileNamed("default")
	if !ok || def.Dir != env {
		t.Fatalf("default: %+v %v, want dir %q", def, ok, env)
	}
}

// 5. ~user is not expanded by anything here, so it is refused rather than
// read as a relative path.
func TestProfileCreateRefusesTildeUser(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "w", dir: "~someone/cc"})
	if err == nil || !strings.Contains(err.Error(), "~someone") {
		t.Fatalf("want a refusal naming ~someone, got %v", err)
	}
}

// 5. An existing dir that others can read, or that a git work tree holds,
// is warned about: the MCP copy can carry secrets.
func TestProfileCreateWarnsAboutAnExposedDir(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	open := filepath.Join(home, "open")
	os.MkdirAll(open, 0o755)
	os.Chmod(open, 0o755)
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "w1", dir: open}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0755") {
		t.Errorf("no warning about the dir's mode:\n%s", out.String())
	}

	repo := filepath.Join(home, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	inRepo := filepath.Join(repo, "cc")
	out.Reset()
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "w2", dir: inRepo}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "git") || !strings.Contains(out.String(), repo) {
		t.Errorf("no warning about the git work tree:\n%s", out.String())
	}
}

// 5. A dir with a space is quoted wherever it is printed as part of a
// command.
func TestProfileCreateQuotesTheDirInCommands(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	dir := filepath.Join(home, "with space")
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "w", dir: dir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CLAUDE_CONFIG_DIR='"+dir+"' claude") {
		t.Errorf("the launch command does not quote the dir:\n%s", out.String())
	}
}

// --- 2. seeding waits for a running daemon ---

func daemonSeams(t *testing.T, running bool, wait time.Duration) {
	t.Helper()
	or, ow, op := daemonRunning, seedWait, seedPoll
	t.Cleanup(func() { daemonRunning, seedWait, seedPoll = or, ow, op })
	daemonRunning = func() bool { return running }
	seedWait, seedPoll = wait, 10*time.Millisecond
}

// daemonLoads plays a daemon that, after a moment, records the profiles it
// has loaded, and optionally changes state the way a swap would.
func daemonLoads(t *testing.T, cfgPath, name, dir string, also func(*state.State)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			time.Sleep(20 * time.Millisecond)
			cfgLoaded := false
			if st, err := state.Load(""); err == nil {
				// Only once the config names the profile, as a reload would.
				if b, err := os.ReadFile(cfgPath); err == nil && strings.Contains(string(b), "name = \""+name+"\"") {
					cfgLoaded = true
					st.DaemonProfiles = map[string]string{"default": "", name: dir}
					st.DaemonConfigHash = config.ContentHash(b)
					if also != nil {
						also(st)
					}
					_ = st.SaveAs(state.OwnerDaemon)
				}
			}
			if cfgLoaded {
				return
			}
		}
	}()
}

func TestSeedWaitsForTheDaemonToLoadTheProfile(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, true, 5*time.Second)
	dir := filepath.Join(home, ".claude-work")
	daemonLoads(t, path, "work", dir, nil)
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"}); err != nil {
		t.Fatal(err)
	}
	if !rec.created {
		t.Fatal("not seeded once the daemon had the profile")
	}
}

// The §3 check after the wait reads state afresh: the daemon may have
// swapped the account in elsewhere meanwhile.
func TestSeedRechecksWithFreshStateAfterTheWait(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, true, 5*time.Second)
	dir := filepath.Join(home, ".claude-work")
	daemonLoads(t, path, "work", dir, func(st *state.State) {
		st.Get("a2")
		st.Profile("default").SetActive("a2")
	})
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("want a refusal naming default, got %v", err)
	}
	if rec.created {
		t.Fatal("seeded an account the daemon had just made live in default")
	}
}

func TestSeedGivesUpWhenTheDaemonNeverLoadsTheProfile(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, true, 100*time.Millisecond)
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
	if err == nil || !strings.Contains(err.Error(), "claudeswitch profile seed work a2") ||
		!strings.Contains(err.Error(), "no credential was written") {
		t.Fatalf("want a refusal saying how to retry, got %v", err)
	}
	if rec.created {
		t.Fatal("seeded without the daemon")
	}
	if _, ok := loadOrFail(t, path).ProfileNamed("work"); !ok {
		t.Error("the profile itself should still be made")
	}
}

// With no daemon there is nothing to wait for.
func TestSeedWithNoDaemonDoesNotWait(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, false, time.Hour)
	start := time.Now()
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"}); err != nil {
		t.Fatal(err)
	}
	if !rec.created || time.Since(start) > 5*time.Second {
		t.Fatalf("created %v after %s", rec.created, time.Since(start))
	}
}

// The retry: `profile seed <name> <account>` on a profile already made.
func TestProfileSeedSignsInAnExistingProfile(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, false, time.Second)
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}}); err != nil {
		t.Fatal(err)
	}
	if err := cmdProfile([]string{"seed", "work", "a2", "--config", path}); err != nil {
		t.Fatal(err)
	}
	if !rec.created || rec.service == "" || rec.file != filepath.Join(home, ".claude-work", ".credentials.json") {
		t.Fatalf("seed: %+v", rec)
	}
	if err := cmdProfile([]string{"seed", "work", "a1", "--config", path}); err == nil {
		t.Error("an account outside the profile's pool must be refused")
	}
}

// The restart window (lane 10 re-review): state.json still holds the map the
// previous daemon wrote, which may name a profile on a dir this daemon does
// not run. A starting daemon replaces it and saves before any network call,
// so a seed waiting meanwhile never reads a stale "loaded".
func TestDaemonStartReplacesTheStaleMapBeforeAnyNetworkCall(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, reloadTwoProfiles))
	r := newRig(t, cfg, false)
	r.d.st.DaemonProfiles = map[string]string{"gone": "/old", "work": "/previous/dir"}
	var saved []map[string]string
	r.d.save = func() error {
		saved = append(saved, maps.Clone(r.d.st.DaemonProfiles))
		return nil
	}
	savesBeforePoll := -1
	r.p.onPoll = func() {
		if savesBeforePoll < 0 {
			savesBeforePoll = len(saved)
		}
	}
	r.d.start(context.Background())
	if savesBeforePoll < 1 {
		t.Fatalf("the daemon made a network call before saving its profiles (saves before: %d)", savesBeforePoll)
	}
	want := map[string]string{}
	for _, in := range cfg.EffectiveProfiles() {
		want[in.Name] = in.Dir
	}
	if !maps.Equal(saved[0], want) {
		t.Fatalf("first save: %v, want %v", saved[0], want)
	}
}

// The daemon records the profiles it has loaded, at start and on reload:
// the marker `profile create --seed` waits for.
func TestDaemonRecordsTheProfilesItLoaded(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	r.d.start(context.Background())
	if got := r.d.st.DaemonProfiles; len(got) != 2 {
		t.Fatalf("after start: %v", got)
	}
	rewrite(t, path, reloadTwoProfiles+thirdProfile)
	r.d.reload(context.Background())
	if got, ok := r.d.st.DaemonProfiles["third"]; !ok || got != "~/.claude-third" {
		t.Fatalf("after reload: %v", r.d.st.DaemonProfiles)
	}
}
