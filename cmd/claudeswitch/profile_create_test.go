package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// I7: `cs profile create` makes a profile in one command, `cs run` launches
// Claude Code in it, `cs profile list` shows them. Everything here runs in a
// temporary HOME with the keychain and Claude Code replaced.

const createAccounts = `
[[account]]
id = "a1"

[[account]]
id = "a2"

[[account]]
id = "a3"
`

// default lists a1 explicitly; a2 and a3 join it by D6.
const createWithDefault = createAccounts + `
[[profile]]
name = "default"
pool = ["a1"]
`

// createHome is a HOME with a base Claude Code setup in ~/.claude and user
// MCP servers in ~/.claude.json.
func createHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	setCCDir(t, "")
	base := filepath.Join(home, ".claude")
	files := map[string]string{
		"settings.json":          `{"theme":"dark"}`,
		"CLAUDE.md":              "# notes\n",
		"skills/one/SKILL.md":    "skill\n",
		"agents/helper.md":       "agent\n",
		"projects/p/t.jsonl":     "{}\n",
		"history.jsonl":          "{}\n",
		".credentials.json":      `{"claudeAiOauth":{"accessToken":"base-token"}}`,
		"plugins/installed.json": "{}\n",
	}
	for p, body := range files {
		full := filepath.Join(base, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	global := `{"oauthAccount":{"emailAddress":"someone@example.invalid"},"userID":"user-1",` +
		`"mcpServers":{"docs":{"type":"http","url":"https://mcp.example.invalid/docs"}},` +
		`"projects":{"/somewhere":{"mcpServers":{"local":{"command":"x"}}}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProfileCreateMakesTheDirSharesTheBaseAndWritesTheBlock(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "work", pool: []string{"a2", "a3"}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-work")
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir: %v %v", fi, err)
	}

	// The base setup is shared by link.
	for _, name := range []string{"settings.json", "CLAUDE.md", "skills", "agents"} {
		target, err := os.Readlink(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s not linked: %v", name, err)
			continue
		}
		if target != filepath.Join(home, ".claude", name) {
			t.Errorf("%s -> %s", name, target)
		}
		if !strings.Contains(out.String(), name) {
			t.Errorf("output does not report linking %s:\n%s", name, out.String())
		}
	}
	// commands/ is absent in the base, so nothing is made for it.
	if _, err := os.Lstat(filepath.Join(dir, "commands")); !os.IsNotExist(err) {
		t.Errorf("commands made from nothing: %v", err)
	}
	// Transcripts, history, plugins and the credential are never shared.
	for _, name := range []string{"projects", "history.jsonl", ".credentials.json", "plugins"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s must not be shared: %v", name, err)
		}
	}

	// User MCP servers are copied, and nothing else from ~/.claude.json.
	gpath := filepath.Join(dir, ".claude.json")
	gfi, err := os.Lstat(gpath)
	if err != nil || !gfi.Mode().IsRegular() || gfi.Mode().Perm() != 0o600 {
		t.Fatalf(".claude.json: %v %v", gfi, err)
	}
	var g map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, gpath)), &g); err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || !strings.Contains(string(g["mcpServers"]), "mcp.example.invalid/docs") {
		t.Fatalf("want only mcpServers, got %s", readFile(t, gpath))
	}
	if !strings.Contains(out.String(), "MCP") || !strings.Contains(out.String(), "sign in") {
		t.Errorf("output must say MCP logins may need doing again:\n%s", out.String())
	}

	// The block, with the dir absolute, so the keychain item Claude Code
	// hashes from CLAUDE_CONFIG_DIR is the one `cs run` names.
	cfg := loadOrFail(t, path)
	in, ok := cfg.ProfileNamed("work")
	if !ok || in.Dir != dir || strings.Join(in.Pool, ",") != "a2,a3" {
		t.Fatalf("work: %+v %v", in, ok)
	}
	if def, _ := cfg.ProfileNamed("default"); strings.Join(def.Pool, ",") != "a1" {
		t.Errorf("default's pool: %v", def.Pool)
	}
	if !strings.Contains(out.String(), "cs run work") {
		t.Errorf("output should say how to start it:\n%s", out.String())
	}
}

// An existing file in the new dir is the person's own, and is left alone.
func TestProfileCreateNeverOverwrites(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	dir := filepath.Join(home, "elsewhere", "w")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("mine"), 0o600)
	os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"mine":true}`), 0o600)
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "w", dir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "settings.json")); got != "mine" {
		t.Errorf("settings.json overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, ".claude.json")); got != `{"mine":true}` {
		t.Errorf(".claude.json overwritten: %q", got)
	}
	if _, err := os.Readlink(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("the rest is still linked: %v", err)
	}
	if !strings.Contains(out.String(), "kept") {
		t.Errorf("output should say what was kept:\n%s", out.String())
	}
}

func TestProfileCreateExpandsATildeDir(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "w", dir: "~/cc/w"}); err != nil {
		t.Fatal(err)
	}
	in, _ := loadOrFail(t, path).ProfileNamed("w")
	if in.Dir != filepath.Join(home, "cc", "w") {
		t.Errorf("dir written as %q", in.Dir)
	}
}

// D1: an account another profile lists is refused, naming that profile, and
// nothing is made.
func TestProfileCreateRefusesAnotherProfilesAccount(t *testing.T) {
	home := createHome(t)
	body := createWithDefault + "\n[[profile]]\nname = \"other\"\ndir = \"~/.claude-other\"\npool = [\"a3\"]\n"
	path := writeConfig(t, body)
	for _, id := range []string{"a1", "a3"} {
		err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2", id}})
		owner := map[string]string{"a1": `"default"`, "a3": `"other"`}[id]
		if err == nil || !strings.Contains(err.Error(), owner) {
			t.Errorf("%s: want a refusal naming %s, got %v", id, owner, err)
		}
	}
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"nobody"}}); err == nil {
		t.Error("an account the config does not have must be refused")
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude-work")); !os.IsNotExist(err) {
		t.Errorf("a refused create made the dir: %v", err)
	}
	if got := readFile(t, path); got != body {
		t.Errorf("a refused create edited the config:\n%s", got)
	}
}

func TestProfileCreateRefusesBadNamesAndDirs(t *testing.T) {
	home := createHome(t)
	body := createWithDefault + "\n[[profile]]\nname = \"other\"\ndir = \"~/.claude-other\"\npool = [\"a3\"]\n"
	path := writeConfig(t, body)
	cases := []createOptions{
		{name: "default"},                   // taken
		{name: "other"},                     // taken
		{name: "../x"},                      // not a plain name
		{name: "w", dir: "~/.claude-other"}, // another profile's dir
		{name: "w", dir: home + "/.claude"}, // the base itself
		{name: "w", dir: "~/.claude/"},      // the base, spelled otherwise
		{name: "w", pool: []string{"a2", "a2"}},
	}
	for _, c := range cases {
		c.cfgPath = path
		if err := profileCreate(&bytes.Buffer{}, c); err == nil {
			t.Errorf("%+v: want a refusal", c)
		}
	}
	if got := readFile(t, path); got != body {
		t.Errorf("a refused create edited the config:\n%s", got)
	}
}

// With no [[profile]] blocks the implicit default is declared too, or the
// accounts left out of the new pool would join nothing and the config would
// not load (D6).
func TestProfileCreateDeclaresDefaultWhenThereAreNoProfiles(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createAccounts)
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}}); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	def, ok := cfg.ProfileNamed("default")
	if !ok || def.Dir != "" || def.FromEnv || strings.Join(def.Pool, ",") != "a1,a3" {
		t.Fatalf("default: %+v %v", def, ok)
	}
	if !strings.Contains(out.String(), "default") {
		t.Errorf("output should say default was declared:\n%s", out.String())
	}
}

// The config edit keeps a symlinked config a symlink, and its mode.
func TestProfileCreateKeepsTheConfigsLinkAndMode(t *testing.T) {
	createHome(t)
	real := writeConfig(t, createWithDefault)
	if err := os.Chmod(real, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: link, name: "work"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", fi, err)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if _, ok := loadOrFail(t, real).ProfileNamed("work"); !ok {
		t.Error("work not written")
	}
}

func TestProfileCommandParsesCreate(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	if err := cmdProfile([]string{"create", "work", "--config", path, "--pool", "a2, a3"}); err != nil {
		t.Fatal(err)
	}
	in, _ := loadOrFail(t, path).ProfileNamed("work")
	if strings.Join(in.Pool, ",") != "a2,a3" {
		t.Errorf("pool %v", in.Pool)
	}
}

// --- seeding ---

type seedFake struct {
	*fakeVault
	creds map[string]*keychain.OAuth
}

func (s seedFake) Load(id string) (*keychain.OAuth, error) {
	if c, ok := s.creds[id]; ok {
		return c, nil
	}
	return nil, errors.New("not in the vault")
}

type seedRec struct {
	exists    bool
	existsErr error
	created   bool
	locked    bool
	service   string
	file      string
	blob      *keychain.Blob
	resolved  string // what resolution finds afterwards; "" means the created name
}

func installSeed(t *testing.T, v *fakeVault) *seedRec {
	t.Helper()
	rec := &seedRec{}
	oe, oc, or, ov, ol := liveExistsFor, createLiveItem, resolveLiveService, newSeedVault, cliLiveFor
	t.Cleanup(func() {
		liveExistsFor, createLiveItem, resolveLiveService, newSeedVault, cliLiveFor = oe, oc, or, ov, ol
	})
	liveExistsFor = func(string) (bool, error) { return rec.exists, rec.existsErr }
	createLiveItem = func(svc, file string, b *keychain.Blob) error {
		_, err := os.Stat(filepath.Join(filepath.Dir(file), ".oauth_refresh.lock"))
		rec.locked = err == nil
		rec.created, rec.service, rec.file, rec.blob = true, svc, file, b
		return nil
	}
	resolveLiveService = func(string) (string, error) {
		if !rec.created {
			return "", keychain.ErrNotFound
		}
		if rec.resolved != "" {
			return rec.resolved, nil
		}
		return rec.service, nil
	}
	newSeedVault = func() seedVault {
		return seedFake{fakeVault: v, creds: map[string]*keychain.OAuth{
			"a2": {AccessToken: "tok-a2", RefreshToken: "ref-a2"},
			"a3": {AccessToken: "tok-a3", RefreshToken: "ref-a3"},
		}}
	}
	cliLiveFor = rigLiveFor
	return rec
}

func TestProfileCreateSeedsTheNewProfilesItem(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	var out bytes.Buffer
	if err := profileCreate(&out, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-work")
	if !rec.created || rec.service != keychain.LiveServiceName(dir) || rec.file != filepath.Join(dir, ".credentials.json") {
		t.Fatalf("created %v %q %q", rec.created, rec.service, rec.file)
	}
	if !rec.locked {
		t.Error("the item must be written holding Claude Code's credential lock for the dir")
	}
	if _, err := os.Stat(filepath.Join(dir, ".oauth_refresh.lock")); !os.IsNotExist(err) {
		t.Errorf("the lock was not released: %v", err)
	}
	b := rec.blob
	if b == nil || b.ClaudeAIOAuth == nil || b.ClaudeAIOAuth.AccessToken != "tok-a2" || b.Meta != nil || b.MCPOAuth != nil {
		t.Fatalf("blob: %+v", b)
	}
	st, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if a := st.Profile("work").Active; a != "a2" {
		t.Errorf("state records %q live in work, want a2", a)
	}
	if !strings.Contains(out.String(), "a2") || !strings.Contains(out.String(), "signed in") {
		t.Errorf("output:\n%s", out.String())
	}
}

// The created item must be the one resolution finds, or Claude Code would
// not read it.
func TestProfileCreateSeedReportsAnItemResolutionDoesNotFind(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	rec.resolved = "Claude Code-credentials-00000000"
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
	if err == nil || !strings.Contains(err.Error(), "resol") {
		t.Fatalf("want a verification failure, got %v", err)
	}
}

// §3: an account live, or possibly live, anywhere else is never seeded, and a
// refused seed changes nothing at all.
func TestProfileCreateRefusesToSeedAnAccountLiveElsewhere(t *testing.T) {
	cases := map[string]func(st *state.State, v *fakeVault){
		"in default's item":        func(_ *state.State, v *fakeVault) { v.holds = map[string]string{"item-default/a2": "yes"} },
		"unknown in default (D18)": func(_ *state.State, v *fakeVault) { v.holds = map[string]string{"item-default/a2": "unknown"} },
		"recorded live in default": func(st *state.State, _ *fakeVault) {
			st.Get("a2")
			st.Profile("default").SetActive("a2")
		},
		"in a ghost": func(st *state.State, _ *fakeVault) {
			st.AddGhost(&state.Ghost{Profile: "gone", Why: state.GhostRemoved, Service: "item-gone", Account: "a2"})
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			home := createHome(t)
			path := writeConfig(t, createWithDefault)
			v := &fakeVault{}
			rec := installSeed(t, v)
			st, _ := state.Load("", "default")
			arrange(st, v)
			if err := st.Save(); err != nil {
				t.Fatal(err)
			}
			err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
			if err == nil || !strings.Contains(err.Error(), "a2") {
				t.Fatalf("want a refusal, got %v", err)
			}
			if rec.created {
				t.Fatal("seeded anyway")
			}
			if _, err := os.Lstat(filepath.Join(home, ".claude-work")); !os.IsNotExist(err) {
				t.Errorf("a refused seed made the dir: %v", err)
			}
			if got := readFile(t, path); got != createWithDefault {
				t.Errorf("a refused seed edited the config:\n%s", got)
			}
		})
	}
}

func TestProfileCreateSeedRefusals(t *testing.T) {
	cases := map[string]struct {
		opts    createOptions
		arrange func(*seedRec)
	}{
		"not in the pool":   {opts: createOptions{pool: []string{"a2"}, seed: "a3"}},
		"not vaulted":       {opts: createOptions{pool: []string{"a1"}, seed: "a1"}},
		"item exists":       {opts: createOptions{pool: []string{"a2"}, seed: "a2"}, arrange: func(r *seedRec) { r.exists = true }},
		"item lookup fails": {opts: createOptions{pool: []string{"a2"}, seed: "a2"}, arrange: func(r *seedRec) { r.existsErr = errors.New("no answer") }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			createHome(t)
			body := createAccounts + "\n[[profile]]\nname = \"default\"\n"
			path := writeConfig(t, body)
			rec := installSeed(t, &fakeVault{})
			if c.arrange != nil {
				c.arrange(rec)
			}
			c.opts.cfgPath, c.opts.name = path, "work"
			if err := profileCreate(&bytes.Buffer{}, c.opts); err == nil {
				t.Fatal("want a refusal")
			}
			if rec.created {
				t.Fatal("seeded anyway")
			}
			if got := readFile(t, path); got != body {
				t.Errorf("config edited:\n%s", got)
			}
		})
	}
}

// --- run ---

// fakeClaude puts a claude on PATH that records its environment and
// arguments, and runs it in place of exec.
func fakeClaude(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "captured")
	script := "#!/bin/sh\n{\n echo \"dir=${CLAUDE_CONFIG_DIR-<unset>}\"\n" +
		" echo \"secure=${CLAUDE_SECURESTORAGE_CONFIG_DIR-<unset>}\"\n" +
		" for a in \"$@\"; do echo \"arg=$a\"; done\n} > '" + capture + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := execClaude
	t.Cleanup(func() { execClaude = old })
	execClaude = func(path string, argv, env []string) error {
		c := exec.Command(path, argv[1:]...)
		c.Env = env
		return c.Run()
	}
	return capture
}

const runConfig = createAccounts + `
[[profile]]
name = "default"
pool = ["a1"]

[[profile]]
name = "work"
dir  = "/somewhere/.claude-work"
pool = ["a2"]
`

func TestRunStartsClaudeInTheProfile(t *testing.T) {
	createHome(t)
	path := writeConfig(t, runConfig)
	capture := fakeClaude(t)
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = rigLiveFor
	t.Setenv("CLAUDE_CONFIG_DIR", "/from/this/shell")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/from/this/shell")

	if err := cmdRun([]string{"--config", path, "work", "--", "--resume", "two words"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, capture)
	want := "dir=/somewhere/.claude-work\nsecure=<unset>\narg=--resume\narg=two words\n"
	if got != want {
		t.Errorf("claude saw:\n%s\nwant:\n%s", got, want)
	}

	if err := cmdRun([]string{"default", "--config", path}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, capture); got != "dir=<unset>\nsecure=<unset>\n" {
		t.Errorf("the no-dir profile runs with CLAUDE_CONFIG_DIR unset (D11); claude saw:\n%s", got)
	}
}

// D9: a profile whose credential cannot be found is not started, and the
// error says how to sign it in.
// Owner decision (lane 10 security review): a profile not signed in yet is
// started anyway, with one note, so /login inside it is the way to sign in.
func TestRunStartsAProfileThatIsNotSignedInWithANote(t *testing.T) {
	createHome(t)
	path := writeConfig(t, runConfig)
	capture := fakeClaude(t)
	old, oldNotes := cliLiveFor, runNotes
	t.Cleanup(func() { cliLiveFor, runNotes = old, oldNotes })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) {
		return nil, keychain.ErrNotFound
	}
	var notes bytes.Buffer
	runNotes = &notes
	if err := cmdRun([]string{"--config", path, "work"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, capture); !strings.HasPrefix(got, "dir=/somewhere/.claude-work\n") {
		t.Errorf("claude saw:\n%s", got)
	}
	want := "profile \"work\" is not signed in yet — use /login, then claudeswitch takes over\n"
	if notes.String() != want {
		t.Errorf("note = %q, want %q", notes.String(), want)
	}
	if err := cmdRun([]string{"--config", path, "nope"}); err == nil {
		t.Error("an unknown profile must be refused")
	}
}

// A signed-in profile starts with no note.
func TestRunSaysNothingForASignedInProfile(t *testing.T) {
	createHome(t)
	path := writeConfig(t, runConfig)
	fakeClaude(t)
	old, oldNotes := cliLiveFor, runNotes
	t.Cleanup(func() { cliLiveFor, runNotes = old, oldNotes })
	cliLiveFor = rigLiveFor
	var notes bytes.Buffer
	runNotes = &notes
	if err := cmdRun([]string{"--config", path, "work"}); err != nil {
		t.Fatal(err)
	}
	if notes.Len() != 0 {
		t.Errorf("note for a signed-in profile: %q", notes.String())
	}
}

// --- list ---

func TestProfileListShowsEachProfile(t *testing.T) {
	createHome(t)
	path := writeConfig(t, runConfig)
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = rigLiveFor
	st, _ := state.Load("", "default", "work")
	st.Profile("work").SetActive("a2")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := profileList(&out, path); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"default", "work", "/somewhere/.claude-work", "a1, a3", "a2", "CLAUDE_CONFIG_DIR unset"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}
