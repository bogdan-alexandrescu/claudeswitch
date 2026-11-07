package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccsetup"
)

// Owner picks OC1 and CC1-CC3: `profile recent`, `claude show`, `profile
// share|own`, `claude write` and `claude plugin`. Everything runs in a
// temporary HOME: ~/.claude and ~/.claude.json for the default profile,
// ~/.claude-work for a second one sharing some of it.

const setupProfiles = `
[[account]]
id = "a1"

[[account]]
id = "w1"

[[profile]]
name = "default"
pool = ["a1"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

// Values that must never reach any output: MCP arguments, env and headers,
// URL paths and queries, settings.json env values, and the account.
var setupSecrets = []string{"SECRET-ARG", "SECRET-ENV", "SECRET-HEADER", "secret-path", "SECRET-QUERY",
	"SECRET-SETTING", "someone@example.invalid", "aaaaaaaa-aaaa-4aaa-8aaa-000000000001"}

// setupWorld is a HOME with a base setup and a work profile that shares
// CLAUDE.md, skills and commands, keeps its own settings.json and has no
// agents. It returns HOME and the config path.
func setupWorld(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	setCCDir(t, "")
	base := filepath.Join(home, ".claude")
	write := func(root string, files map[string]string) {
		for p, body := range files {
			full := filepath.Join(root, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(base, map[string]string{
		"CLAUDE.md": "# notes\nbe brief\nuse tests\n",
		"settings.json": `{"model": "opus",
			"permissions": {"allow": ["Read", "Edit"], "deny": ["Bash(rm:*)"]},
			"hooks": {"Stop": [], "PreToolUse": []}, "statusLine": {"type": "command", "command": "cs statusline"},
			"env": {"HTTP_PROXY": "SECRET-SETTING", "DISABLE_TELEMETRY": "SECRET-SETTING"},
			"enabledPlugins": {"cs@claudeswitch": true, "slack@claude-plugins-official": false}}`,
		"skills/release-notes/SKILL.md": "---\nname: release-notes\ndescription: Drafts release notes from merged pull requests\n---\n",
		"skills/plain/SKILL.md":         "no front matter\n",
		"agents/code-reviewer.md":       "---\nname: code-reviewer\ndescription: Reviews a diff for bugs before it is pushed\n---\n",
		"commands/standup.md":           "---\ndescription: \"Yesterday's commits as a stand-up note\"\n---\n",
		"commands/git/sync.md":          "---\ndescription: Pull and rebase\n---\n",
		"plugins/installed_plugins.json": `{"version": 2, "plugins": {
			"cs@claudeswitch": [{"scope": "user", "version": "0.8.19"}],
			"slack@claude-plugins-official": [{"scope": "user", "version": "1.0.0"}]}}`,
		".credentials.json": `{"claudeAiOauth":{"accessToken":"SECRET-ARG"}}`,
	})
	global := map[string]any{
		"oauthAccount": map[string]any{"emailAddress": "someone@example.invalid",
			"accountUuid": "aaaaaaaa-aaaa-4aaa-8aaa-000000000001"},
		"projects": map[string]any{filepath.Join(home, "code", "cs"): map[string]any{},
			filepath.Join(home, "code", "web.site"): map[string]any{}, filepath.Join(home, "code", "old"): map[string]any{},
			filepath.Join(home, "code", "gone"): map[string]any{}},
		"mcpServers": map[string]any{
			"linear": map[string]any{"type": "http", "url": "https://mcp.linear.example/secret-path?token=SECRET-QUERY",
				"headers": map[string]any{"Authorization": "Bearer SECRET-HEADER"}},
			"files": map[string]any{"command": "/usr/local/bin/npx", "args": []string{"-y", "SECRET-ARG"},
				"env": map[string]any{"API_KEY": "SECRET-ENV"}},
		},
	}
	raw, _ := json.Marshal(global)
	write(home, map[string]string{".claude.json": string(raw)})
	for _, d := range []string{"cs", "web.site", "old"} {
		if err := os.MkdirAll(filepath.Join(home, "code", d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	work := filepath.Join(home, ".claude-work")
	write(work, map[string]string{
		"settings.json": `{"model": "sonnet", "permissions": {"allow": ["Read", "Edit", "Bash(ls)"]},
			"enabledPlugins": {"cs@claudeswitch": true}}`,
		"plugins/installed_plugins.json": `{"plugins": {"cs@claudeswitch": [{"version": "0.8.19"}]}}`,
		".claude.json": `{"mcpServers": {"a": {"command": "x"}, "b": {"command": "y"},
			"c": {"type": "sse", "url": "https://c.example/secret-path"}}, "projects": {"` +
			filepath.Join(home, "code", "cs") + `": {}}}`,
	})
	for _, item := range []string{"CLAUDE.md", "skills", "commands"} {
		if err := os.Symlink(filepath.Join(base, item), filepath.Join(work, item)); err != nil {
			t.Fatal(err)
		}
	}
	return home, writeConfig(t, setupProfiles)
}

// transcript makes a transcript for folder in a profile's projects/, last
// written at.
func transcript(t *testing.T, ccDir, folder string, at time.Time) {
	t.Helper()
	d := filepath.Join(ccDir, "projects", ccsetup.ProjectDir(folder))
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "s.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func noSecrets(t *testing.T, what, out string) {
	t.Helper()
	for _, s := range setupSecrets {
		if strings.Contains(out, s) {
			t.Errorf("%s shows %q:\n%s", what, s, out)
		}
	}
}

func jsonOf(t *testing.T, f func(w *bytes.Buffer) error) map[string]any {
	t.Helper()
	var b bytes.Buffer
	if err := f(&b); err != nil {
		t.Fatal(err)
	}
	return decodeJSON(t, b.Bytes())
}

// OC1: each profile's folders come from its own .claude.json (the default
// profile's is ~/.claude.json), newest first, never-used last, and only the
// folders that still exist.
func TestProfileRecentReadsEachProfilesOwnFile(t *testing.T) {
	home, cfg := setupWorld(t)
	at := time.Date(2026, 10, 28, 10, 2, 11, 0, time.UTC)
	transcript(t, filepath.Join(home, ".claude"), filepath.Join(home, "code", "web.site"), at)
	transcript(t, filepath.Join(home, ".claude"), filepath.Join(home, "code", "cs"), at.Add(-time.Hour))
	transcript(t, filepath.Join(home, ".claude-work"), filepath.Join(home, "code", "cs"), at.Add(-48*time.Hour))

	m := jsonOf(t, func(w *bytes.Buffer) error { return profileRecent(w, cfg, "", true) })
	want := map[string]any{"profiles": []any{
		map[string]any{"profile": "default", "folders": []any{
			map[string]any{"path": filepath.Join(home, "code", "web.site"), "last_used": "2026-10-28T10:02:11Z"},
			map[string]any{"path": filepath.Join(home, "code", "cs"), "last_used": "2026-10-28T09:02:11Z"},
			map[string]any{"path": filepath.Join(home, "code", "old"), "last_used": nil},
		}},
		map[string]any{"profile": "work", "folders": []any{
			map[string]any{"path": filepath.Join(home, "code", "cs"), "last_used": "2026-10-26T10:02:11Z"},
		}},
	}}
	if !reflect.DeepEqual(m, want) {
		got, _ := json.MarshalIndent(m, "", "  ")
		t.Fatalf("got %s", got)
	}

	// One profile by name; an unknown one is invalid_value.
	m = jsonOf(t, func(w *bytes.Buffer) error { return profileRecent(w, cfg, "work", true) })
	if ps := m["profiles"].([]any); len(ps) != 1 || ps[0].(map[string]any)["profile"] != "work" {
		t.Errorf("work alone: %v", m)
	}
	if got := errCode(t, profileRecent(&bytes.Buffer{}, cfg, "nope", true)); got != codeInvalidValue {
		t.Errorf("unknown profile: %s", got)
	}

	// The human form: one line per folder, its age, the path from ~.
	var out bytes.Buffer
	if err := profileRecent(&out, cfg, "", false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  default\n", "  work\n", "~/code/web.site\n", "-     ~/code/old\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human form lacks %q:\n%s", want, out.String())
		}
	}
	if regexp.MustCompile(`\d+d +~/code/cs`).FindString(out.String()) == "" {
		t.Errorf("work's folder should show its age in days:\n%s", out.String())
	}
}

// A profile with no .claude.json, or one that does not parse, has no
// folders: not an error. At most twelve are listed.
func TestProfileRecentEmptyAndLimited(t *testing.T) {
	home, cfg := setupWorld(t)
	os.Remove(filepath.Join(home, ".claude-work", ".claude.json"))
	m := jsonOf(t, func(w *bytes.Buffer) error { return profileRecent(w, cfg, "work", true) })
	if f := m["profiles"].([]any)[0].(map[string]any)["folders"].([]any); len(f) != 0 {
		t.Errorf("no .claude.json: %v", f)
	}
	os.WriteFile(filepath.Join(home, ".claude-work", ".claude.json"), []byte("{not json"), 0o600)
	m = jsonOf(t, func(w *bytes.Buffer) error { return profileRecent(w, cfg, "work", true) })
	if f := m["profiles"].([]any)[0].(map[string]any)["folders"].([]any); len(f) != 0 {
		t.Errorf("an unreadable .claude.json: %v", f)
	}
	projects := map[string]any{}
	for i := 0; i < 20; i++ {
		d := filepath.Join(home, "many", strings.Repeat("x", i+1))
		os.MkdirAll(d, 0o700)
		projects[d] = map[string]any{}
	}
	raw, _ := json.Marshal(map[string]any{"projects": projects})
	os.WriteFile(filepath.Join(home, ".claude-work", ".claude.json"), raw, 0o600)
	m = jsonOf(t, func(w *bytes.Buffer) error { return profileRecent(w, cfg, "work", true) })
	if f := m["profiles"].([]any)[0].(map[string]any)["folders"].([]any); len(f) != recentLimit {
		t.Errorf("%d folders, want %d", len(f), recentLimit)
	}
}

// CC1: every field of ~/.claude, and what each profile shares with it.
func TestClaudeShowDescribesTheBaseAndEveryProfile(t *testing.T) {
	home, cfg := setupWorld(t)
	base := filepath.Join(home, ".claude")
	var b bytes.Buffer
	if err := claudeShow(&b, cfg, "", true); err != nil {
		t.Fatal(err)
	}
	noSecrets(t, "claude show --json", b.String())
	m := decodeJSON(t, b.Bytes())

	if m["dir"] != base {
		t.Errorf("dir %v", m["dir"])
	}
	in := m["instructions"].(map[string]any)
	if in["path"] != filepath.Join(base, "CLAUDE.md") || in["target"] != in["path"] || in["lines"] != 3.0 {
		t.Errorf("instructions %v", in)
	}
	if _, err := time.Parse(time.RFC3339, in["modified"].(string)); err != nil || !strings.HasSuffix(in["modified"].(string), "Z") {
		t.Errorf("modified %v", in["modified"])
	}
	wantSettings := map[string]any{"path": filepath.Join(base, "settings.json"), "target": filepath.Join(base, "settings.json"),
		"model": "opus", "allow": 2.0, "deny": 1.0, "hooks": []any{"PreToolUse", "Stop"}, "status_line": true,
		"env": []any{"DISABLE_TELEMETRY", "HTTP_PROXY"}, "error": nil}
	if !reflect.DeepEqual(m["settings"], wantSettings) {
		t.Errorf("settings %v", m["settings"])
	}
	entry := func(name, desc, path string) any {
		return map[string]any{"name": name, "description": desc, "path": path}
	}
	if want := []any{entry("plain", "", filepath.Join(base, "skills", "plain")),
		entry("release-notes", "Drafts release notes from merged pull requests", filepath.Join(base, "skills", "release-notes"))}; !reflect.DeepEqual(m["skills"], want) {
		t.Errorf("skills %v", m["skills"])
	}
	if want := []any{entry("code-reviewer", "Reviews a diff for bugs before it is pushed",
		filepath.Join(base, "agents", "code-reviewer.md"))}; !reflect.DeepEqual(m["agents"], want) {
		t.Errorf("agents %v", m["agents"])
	}
	if want := []any{entry("git/sync", "Pull and rebase", filepath.Join(base, "commands", "git", "sync.md")),
		entry("standup", "Yesterday's commits as a stand-up note", filepath.Join(base, "commands", "standup.md"))}; !reflect.DeepEqual(m["commands"], want) {
		t.Errorf("commands %v", m["commands"])
	}
	plugin := func(id, name, market, version string, on bool) any {
		return map[string]any{"id": id, "name": name, "marketplace": market, "version": version, "enabled": on}
	}
	if want := []any{plugin("cs@claudeswitch", "cs", "claudeswitch", "0.8.19", true),
		plugin("slack@claude-plugins-official", "slack", "claude-plugins-official", "1.0.0", false)}; !reflect.DeepEqual(m["plugins"], want) {
		t.Errorf("plugins %v", m["plugins"])
	}
	if want := []any{map[string]any{"name": "files", "type": "stdio", "target": "npx"},
		map[string]any{"name": "linear", "type": "http", "target": "mcp.linear.example"}}; !reflect.DeepEqual(m["servers"], want) {
		t.Errorf("servers %v", m["servers"])
	}

	profiles := m["profiles"].([]any)
	if want := map[string]any{"name": "default", "dir": base, "base": true}; !reflect.DeepEqual(profiles[0], want) {
		t.Errorf("default %v", profiles[0])
	}
	work := profiles[1].(map[string]any)
	item := func(name, state, summary string) any {
		return map[string]any{"item": name, "state": state, "summary": summary}
	}
	wantWork := map[string]any{"name": "work", "dir": filepath.Join(home, ".claude-work"), "base": false,
		"items": []any{item("CLAUDE.md", "shared", "3 lines"), item("settings.json", "own", "model sonnet · 3 allowed"),
			item("skills", "shared", "2 skills"), item("commands", "shared", "2 commands"), item("agents", "missing", "")},
		"plugins": []any{plugin("cs@claudeswitch", "cs", "claudeswitch", "0.8.19", true)},
		"servers": 3.0}
	if !reflect.DeepEqual(work, wantWork) {
		got, _ := json.MarshalIndent(work, "", "  ")
		t.Errorf("work %s", got)
	}

	// The human form shows the same, and no secret either.
	var out bytes.Buffer
	if err := claudeShow(&out, cfg, "", false); err != nil {
		t.Fatal(err)
	}
	noSecrets(t, "claude show", out.String())
	for _, want := range []string{"settings.json  model opus · 2 allowed · 1 denied", "release-notes",
		"git/sync", "cs@claudeswitch 0.8.19, on", "linear  http, mcp.linear.example", "settings.json  own",
		"agents         missing\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human form lacks %q:\n%s", want, out.String())
		}
	}
}

// Nothing there is null or empty, never a missing key or an error; a
// settings.json that is not JSON says why, every field empty.
func TestClaudeShowWithLittleThere(t *testing.T) {
	home, cfg := setupWorld(t)
	base := filepath.Join(home, ".claude")
	for _, p := range []string{"CLAUDE.md", "skills", "agents", "commands", "plugins"} {
		os.RemoveAll(filepath.Join(base, p))
	}
	os.Remove(filepath.Join(home, ".claude.json"))
	os.WriteFile(filepath.Join(base, "settings.json"), []byte("{\n  \"model\": \"opus\",\n  \"env\": {\"K\": \"SECRET-SETTING\"},\n}\n"), 0o600)
	var b bytes.Buffer
	if err := claudeShow(&b, cfg, "", true); err != nil {
		t.Fatal(err)
	}
	noSecrets(t, "claude show --json", b.String())
	m := decodeJSON(t, b.Bytes())
	if m["instructions"] != nil {
		t.Errorf("instructions %v", m["instructions"])
	}
	for _, k := range []string{"skills", "agents", "commands", "plugins", "servers"} {
		if l, ok := m[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s: %v, want []", k, m[k])
		}
	}
	s := m["settings"].(map[string]any)
	if s["model"] != nil || s["allow"] != 0.0 || s["deny"] != 0.0 || len(s["hooks"].([]any)) != 0 ||
		len(s["env"].([]any)) != 0 || s["status_line"] != false {
		t.Errorf("an invalid settings.json must have every field empty: %v", s)
	}
	if e, _ := s["error"].(string); !strings.Contains(e, "invalid character '}'") || !strings.Contains(e, "line 4, column 1") {
		t.Errorf("error %v", s["error"])
	}
	// The work profile reads the shared CLAUDE.md, now gone: missing.
	work := m["profiles"].([]any)[1].(map[string]any)
	if it := work["items"].([]any)[0].(map[string]any); it["state"] != "missing" || it["summary"] != "" {
		t.Errorf("a link to nothing: %v", it)
	}
	os.Remove(filepath.Join(base, "settings.json"))
	m = jsonOf(t, func(w *bytes.Buffer) error { return claudeShow(w, cfg, "", true) })
	if m["settings"] != nil {
		t.Errorf("no settings.json: %v", m["settings"])
	}
}

// A link to anywhere but ~/.claude is elsewhere; a dangling one missing.
func TestClaudeShowTellsElsewhereFromShared(t *testing.T) {
	home, cfg := setupWorld(t)
	work := filepath.Join(home, ".claude-work")
	dotfiles := filepath.Join(home, "dotfiles")
	os.MkdirAll(filepath.Join(dotfiles, "agents"), 0o700)
	os.WriteFile(filepath.Join(dotfiles, "agents", "a.md"), []byte("a"), 0o600)
	os.Remove(filepath.Join(work, "skills"))
	os.Symlink(filepath.Join(dotfiles, "agents"), filepath.Join(work, "agents"))
	os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(work, "skills"))
	m := jsonOf(t, func(w *bytes.Buffer) error { return claudeShow(w, cfg, "", true) })
	items := m["profiles"].([]any)[1].(map[string]any)["items"].([]any)
	got := map[string]string{}
	for _, it := range items {
		it := it.(map[string]any)
		got[it["item"].(string)] = it["state"].(string) + " " + it["summary"].(string)
	}
	if got["agents"] != "elsewhere 1 agent" || got["skills"] != "missing " {
		t.Errorf("states %v", got)
	}
}

// Amendment 1: --profile shows what that profile reads, through its links,
// with its shares and no profiles list.
func TestClaudeShowOneProfile(t *testing.T) {
	home, cfg := setupWorld(t)
	work := filepath.Join(home, ".claude-work")
	var b bytes.Buffer
	if err := claudeShow(&b, cfg, "work", true); err != nil {
		t.Fatal(err)
	}
	noSecrets(t, "claude show --profile work --json", b.String())
	m := decodeJSON(t, b.Bytes())
	if _, ok := m["profiles"]; ok {
		t.Error("--profile has no profiles key")
	}
	if m["profile"] != "work" || m["base"] != false || m["dir"] != work {
		t.Errorf("profile, base, dir: %v %v %v", m["profile"], m["base"], m["dir"])
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(home, ".claude", "CLAUDE.md"))
	in := m["instructions"].(map[string]any)
	if in["path"] != filepath.Join(work, "CLAUDE.md") || in["target"] != real {
		t.Errorf("instructions path %v target %v, want the link and %s", in["path"], in["target"], real)
	}
	s := m["settings"].(map[string]any)
	if s["model"] != "sonnet" || s["path"] != filepath.Join(work, "settings.json") || s["target"] != s["path"] {
		t.Errorf("settings %v", s)
	}
	if len(m["skills"].([]any)) != 2 || len(m["commands"].([]any)) != 2 || len(m["agents"].([]any)) != 0 {
		t.Errorf("a shared skills/ lists ~/.claude's: %v %v %v", m["skills"], m["commands"], m["agents"])
	}
	if p := m["plugins"].([]any); len(p) != 1 || p[0].(map[string]any)["enabled"] != true {
		t.Errorf("plugins %v", p)
	}
	if want := []any{map[string]any{"name": "a", "type": "stdio", "target": "x"},
		map[string]any{"name": "b", "type": "stdio", "target": "y"},
		map[string]any{"name": "c", "type": "sse", "target": "c.example"}}; !reflect.DeepEqual(m["servers"], want) {
		t.Errorf("servers %v", m["servers"])
	}
	share := func(item, state string) any { return map[string]any{"item": item, "state": state} }
	if want := []any{share("CLAUDE.md", "shared"), share("settings.json", "own"), share("skills", "shared"),
		share("commands", "shared"), share("agents", "missing")}; !reflect.DeepEqual(m["shares"], want) {
		t.Errorf("shares %v", m["shares"])
	}

	// The base profile shares nothing with itself, and reads ~/.claude.json.
	m = jsonOf(t, func(w *bytes.Buffer) error { return claudeShow(w, cfg, "default", true) })
	if m["base"] != true || len(m["shares"].([]any)) != 0 || len(m["servers"].([]any)) != 2 {
		t.Errorf("default: base %v shares %v servers %v", m["base"], m["shares"], m["servers"])
	}
	if got := errCode(t, claudeShow(&bytes.Buffer{}, cfg, "nope", true)); got != codeInvalidValue {
		t.Errorf("unknown profile: %s", got)
	}
	var out bytes.Buffer
	if err := claudeShow(&out, cfg, "work", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "profile work: ~/.claude-work") ||
		!strings.Contains(out.String(), "(→ ~/.claude/CLAUDE.md)") || !strings.Contains(out.String(), "its own:") {
		t.Errorf("human form:\n%s", out.String())
	}
}

// CC3: share renames the profile's own item aside and links ~/.claude's.
func TestProfileShare(t *testing.T) {
	home, cfg := setupWorld(t)
	work := filepath.Join(home, ".claude-work")
	m := jsonOf(t, func(w *bytes.Buffer) error { return profileShare(w, cfg, "work", "settings.json", true) })
	backup, _ := m["backup"].(string)
	if m["profile"] != "work" || m["item"] != "settings.json" || m["state"] != "shared" ||
		!regexp.MustCompile(`^`+regexp.QuoteMeta(filepath.Join(work, "settings.json"))+`\.claudeswitch-\d{8}-\d{6}$`).MatchString(backup) {
		t.Fatalf("share: %v", m)
	}
	if b, _ := os.ReadFile(backup); !strings.Contains(string(b), "sonnet") {
		t.Errorf("the backup holds %q", b)
	}
	if got, _ := os.Readlink(filepath.Join(work, "settings.json")); got != filepath.Join(home, ".claude", "settings.json") {
		t.Errorf("linked to %q", got)
	}
	// Shared already: nothing changes, no backup.
	m = jsonOf(t, func(w *bytes.Buffer) error { return profileShare(w, cfg, "work", "settings.json", true) })
	if m["backup"] != nil {
		t.Errorf("again: %v", m)
	}
	// Missing in the profile: linked, nothing to back up.
	m = jsonOf(t, func(w *bytes.Buffer) error { return profileShare(w, cfg, "work", "agents", true) })
	if m["backup"] != nil || ccsetup.State(filepath.Join(home, ".claude"), work, "agents") != ccsetup.Shared {
		t.Errorf("agents: %v", m)
	}

	// Refusals.
	err := profileShare(&bytes.Buffer{}, cfg, "default", "skills", true)
	if errCode(t, err) != codeInvalidValue || !strings.Contains(err.Error(), "default is ~/.claude itself") {
		t.Errorf("the base: %v", err)
	}
	for _, args := range [][2]string{{"nope", "skills"}, {"work", "projects"}, {"work", ".credentials.json"}} {
		if got := errCode(t, profileShare(&bytes.Buffer{}, cfg, args[0], args[1], true)); got != codeInvalidValue {
			t.Errorf("%v: %s", args, got)
		}
	}
	os.RemoveAll(filepath.Join(home, ".claude", "commands"))
	os.Remove(filepath.Join(work, "commands"))
	err = profileShare(&bytes.Buffer{}, cfg, "work", "commands", true)
	var ae *appError
	if errCode(t, err) != codeNotFound || !errors.As(err, &ae) || ae.Hint != "create it in ~/.claude first" {
		t.Errorf("nothing in ~/.claude: %v", err)
	}
	var out bytes.Buffer
	if err := profileShare(&out, cfg, "work", "skills", false); err != nil ||
		!strings.Contains(out.String(), "work: skills is shared with ~/.claude already; nothing changed") {
		t.Errorf("human form: %v\n%s", err, out.String())
	}
}

// CC3: own replaces a link with a copy: a file, or a folder whole with the
// links inside kept as links.
func TestProfileOwn(t *testing.T) {
	home, cfg := setupWorld(t)
	base, work := filepath.Join(home, ".claude"), filepath.Join(home, ".claude-work")
	os.Symlink("release-notes", filepath.Join(base, "skills", "notes-alias"))
	os.Chmod(filepath.Join(base, "CLAUDE.md"), 0o640)
	for _, item := range []string{"CLAUDE.md", "skills"} {
		m := jsonOf(t, func(w *bytes.Buffer) error { return profileOwn(w, cfg, "work", item, true) })
		if want := map[string]any{"profile": "work", "item": item, "state": "own"}; !reflect.DeepEqual(m, want) {
			t.Errorf("%s: %v", item, m)
		}
		if ccsetup.State(base, work, item) != ccsetup.Own {
			t.Errorf("%s is not its own", item)
		}
	}
	if fi, err := os.Lstat(filepath.Join(work, "CLAUDE.md")); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o640 {
		t.Errorf("CLAUDE.md copy: %v %v", fi, err)
	}
	if got, err := os.Readlink(filepath.Join(work, "skills", "notes-alias")); err != nil || got != "release-notes" {
		t.Errorf("a link inside skills/: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(work, "skills", "release-notes", "SKILL.md")); err != nil {
		t.Errorf("skills copied: %v", err)
	}
	// Own already, missing, the base.
	m := jsonOf(t, func(w *bytes.Buffer) error { return profileOwn(w, cfg, "work", "settings.json", true) })
	if m["state"] != "own" {
		t.Errorf("own already: %v", m)
	}
	if got := errCode(t, profileOwn(&bytes.Buffer{}, cfg, "work", "agents", true)); got != codeNotFound {
		t.Errorf("missing: %s", got)
	}
	if got := errCode(t, profileOwn(&bytes.Buffer{}, cfg, "default", "skills", true)); got != codeInvalidValue {
		t.Errorf("the base: %s", got)
	}
	var out bytes.Buffer
	if err := profileOwn(&out, cfg, "work", "commands", false); err != nil ||
		!strings.Contains(out.String(), "work: commands is its own copy now, of ~/.claude/commands") {
		t.Errorf("human form: %v\n%s", err, out.String())
	}
}

// Amendment 1: claude write replaces what the profile reads, through its
// link, whole, keeping the mode; it checks what it is given first.
func TestClaudeWrite(t *testing.T) {
	home, cfg := setupWorld(t)
	base, work := filepath.Join(home, ".claude"), filepath.Join(home, ".claude-work")
	os.Chmod(filepath.Join(base, "CLAUDE.md"), 0o640)
	old, _ := os.ReadFile(filepath.Join(base, "CLAUDE.md"))
	body := "# shared notes\n"

	// Through work's link: ~/.claude's file changes, and default reads it too.
	m := jsonOf(t, func(w *bytes.Buffer) error {
		return claudeWrite(w, cfg, "CLAUDE.md", "work", ccsetup.Hash(old), strings.NewReader(body), true)
	})
	real, _ := filepath.EvalSymlinks(filepath.Join(base, "CLAUDE.md"))
	want := map[string]any{"path": real, "bytes": float64(len(body)), "sha256": ccsetup.Hash([]byte(body)),
		"shared_with": []any{"default"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %v\nwant %v", m, want)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "CLAUDE.md")); string(b) != body {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(base, "CLAUDE.md")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %o, want the file's own 0640", fi.Mode().Perm())
	}
	if fi, err := os.Lstat(filepath.Join(work, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link must stay a link")
	}

	// Opened before that write: refused, nothing written.
	err := claudeWrite(&bytes.Buffer{}, cfg, "CLAUDE.md", "", ccsetup.Hash(old), strings.NewReader("lost\n"), true)
	var ae *appError
	if errCode(t, err) != codeConfigChanged || !errors.As(err, &ae) || ae.Hint != "reopen it to see the change" ||
		!strings.Contains(ae.Message, "changed since it was opened") {
		t.Errorf("stale: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "CLAUDE.md")); string(b) != body {
		t.Errorf("a refused write changed the file: %q", b)
	}

	// settings.json must be a JSON object.
	before, _ := os.ReadFile(filepath.Join(work, "settings.json"))
	err = claudeWrite(&bytes.Buffer{}, cfg, "settings.json", "work", "", strings.NewReader("{\n  \"model\": \"x\",\n}"), true)
	if errCode(t, err) != codeInvalidValue ||
		!strings.Contains(err.Error(), "settings.json is not valid JSON: invalid character '}'") ||
		!strings.Contains(err.Error(), "line 3, column 1") {
		t.Errorf("invalid JSON: %v", err)
	}
	if got := errCode(t, claudeWrite(&bytes.Buffer{}, cfg, "settings.json", "work", "", strings.NewReader("[1]"), true)); got != codeInvalidValue {
		t.Errorf("an array: %s", got)
	}
	if after, _ := os.ReadFile(filepath.Join(work, "settings.json")); !bytes.Equal(before, after) {
		t.Error("a refused settings.json was written")
	}
	m = jsonOf(t, func(w *bytes.Buffer) error {
		return claudeWrite(w, cfg, "settings.json", "work", "", strings.NewReader(`{"model": "haiku"}`), true)
	})
	if m["path"] != filepath.Join(work, "settings.json") || len(m["shared_with"].([]any)) != 0 {
		t.Errorf("work's own settings.json: %v", m)
	}
	if fi, _ := os.Stat(filepath.Join(work, "settings.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("settings.json mode %o", fi.Mode().Perm())
	}

	// A new file: absent hashes as empty; 0644 for CLAUDE.md, 0600 for
	// settings.json. No temp file is left behind.
	fresh := filepath.Join(home, ".claude-fresh")
	cfg2 := writeConfig(t, setupProfiles+"\n[[profile]]\nname = \"fresh\"\ndir  = \""+fresh+"\"\npool = []\n")
	for item, mode := range map[string]os.FileMode{"CLAUDE.md": 0o644, "settings.json": 0o600} {
		if err := claudeWrite(&bytes.Buffer{}, cfg2, item, "fresh", ccsetup.Hash(nil), strings.NewReader("{}"), true); err != nil {
			t.Fatalf("%s: %v", item, err)
		}
		if fi, err := os.Stat(filepath.Join(fresh, item)); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("new %s: %v %v", item, fi, err)
		}
	}
	ents, _ := os.ReadDir(fresh)
	if len(ents) != 2 {
		t.Errorf("left in the dir: %v", ents)
	}

	// Refusals: too big, a bad hash, another file, an unknown profile.
	big := strings.Repeat("x", writeMax+1)
	if got := errCode(t, claudeWrite(&bytes.Buffer{}, cfg, "CLAUDE.md", "", "", strings.NewReader(big), true)); got != codeInvalidValue {
		t.Errorf("over 1 MiB: %s", got)
	}
	if err := claudeWrite(&bytes.Buffer{}, cfg, "CLAUDE.md", "", "", strings.NewReader(big[:writeMax]), true); err != nil {
		t.Errorf("exactly 1 MiB: %v", err)
	}
	for _, c := range []struct{ item, profile, hash string }{
		{"CLAUDE.md", "", "abc"}, {"skills", "", ""}, {"../x", "", ""}, {"CLAUDE.md", "nope", ""},
	} {
		if got := errCode(t, claudeWrite(&bytes.Buffer{}, cfg, c.item, c.profile, c.hash, strings.NewReader("x"), true)); got != codeInvalidValue {
			t.Errorf("%v: %s", c, got)
		}
	}
}

// fakePluginClaude puts a claude on PATH that records its arguments and
// CLAUDE_CONFIG_DIR, fails for missing@nowhere with colour codes on its
// last stderr line, and hangs for slow@nowhere. It returns the log.
func fakePluginClaude(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\n" +
		"printf '%s|%s\\n' \"$*\" \"${CLAUDE_CONFIG_DIR-unset}\" >> '" + log + "'\n" +
		"case \"$3\" in\n" +
		"missing@nowhere) printf 'Looking it up\\n\\033[31mPlugin \"missing@nowhere\" not found\\033[0m\\n\\n' >&2; exit 1;;\n" +
		"slow@nowhere) exec sleep 5;;\n" +
		"esac\n" +
		"echo done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// CC2: plugins are switched by Claude Code itself, in the profile:
// CLAUDE_CONFIG_DIR unset for ~/.claude whatever this shell says, set to
// the profile's dir otherwise.
func TestClaudePluginRunsClaudeInTheProfile(t *testing.T) {
	home, cfg := setupWorld(t)
	log := fakePluginClaude(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "/some/shell/dir")

	m := jsonOf(t, func(w *bytes.Buffer) error {
		return claudePlugin(w, cfg, "disable", "slack@claude-plugins-official", "", true)
	})
	if want := map[string]any{"plugin": "slack@claude-plugins-official", "profile": "default", "enabled": false}; !reflect.DeepEqual(m, want) {
		t.Errorf("disable: %v", m)
	}
	m = jsonOf(t, func(w *bytes.Buffer) error { return claudePlugin(w, cfg, "enable", "cs@claudeswitch", "work", true) })
	if want := map[string]any{"plugin": "cs@claudeswitch", "profile": "work", "enabled": true}; !reflect.DeepEqual(m, want) {
		t.Errorf("enable: %v", m)
	}
	calls := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
	want := []string{"plugin disable slack@claude-plugins-official|unset",
		"plugin enable cs@claudeswitch|" + filepath.Join(home, ".claude-work")}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("claude ran as %q, want %q", calls, want)
	}

	// Its failure is the message: the last line of its stderr, plain.
	err := claudePlugin(&bytes.Buffer{}, cfg, "enable", "missing@nowhere", "", true)
	var ae *appError
	if errCode(t, err) != codeFailed || !errors.As(err, &ae) || ae.Message != `Plugin "missing@nowhere" not found` {
		t.Errorf("failure: %#v", err)
	}
	old := claudePluginTimeout
	claudePluginTimeout = 200 * time.Millisecond
	t.Cleanup(func() { claudePluginTimeout = old })
	if err := claudePlugin(&bytes.Buffer{}, cfg, "enable", "slow@nowhere", "", true); errCode(t, err) != codeFailed ||
		!strings.Contains(err.Error(), "did not finish within") {
		t.Errorf("timeout: %v", err)
	}
	for _, c := range [][2]string{{"-x", ""}, {"", ""}, {"cs@claudeswitch", "nope"}} {
		if got := errCode(t, claudePlugin(&bytes.Buffer{}, cfg, "enable", c[0], c[1], true)); got != codeInvalidValue {
			t.Errorf("%v: %s", c, got)
		}
	}
	var out bytes.Buffer
	if err := claudePlugin(&out, cfg, "enable", "cs@claudeswitch", "work", false); err != nil ||
		out.String() != "  cs@claudeswitch is on in profile work\n" {
		t.Errorf("human form: %v %q", err, out.String())
	}

	// No claude at all.
	t.Setenv("PATH", t.TempDir())
	err = claudePlugin(&bytes.Buffer{}, cfg, "enable", "cs@claudeswitch", "", true)
	if errCode(t, err) != codeFailed || !errors.As(err, &ae) || ae.Hint != "install Claude Code, or put claude on PATH" {
		t.Errorf("not on PATH: %v", err)
	}
}

// Every new command answers help with its usage, exit 0, and refuses a
// stray argument or a flag it does not know.
func TestClaudeSetupCommandsHelpAndRefusals(t *testing.T) {
	_, cfg := setupWorld(t)
	for _, args := range [][]string{
		{"claude", "--help"}, {"claude", "help"}, {"help", "claude"}, {"claude", "show", "--help"},
		{"claude", "write", "-h"}, {"claude", "plugin", "--help"}, {"profile", "recent", "--help"},
		{"profile", "share", "--help"}, {"profile", "own", "-h"},
	} {
		out, errOut, code := runCLISplit(t, args...)
		if code != 0 || !strings.Contains(out, "usage: claudeswitch") || strings.Contains(out+errOut, "help requested") {
			t.Errorf("%v: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{
		{"claude"}, {"claude", "bogus"}, {"claude", "show", "extra"}, {"claude", "show", "--bogus"},
		{"claude", "plugin", "enable"}, {"claude", "plugin", "toggle", "x"}, {"claude", "write"},
		{"profile", "recent", "a", "b"}, {"profile", "share", "work"}, {"profile", "own", "work", "skills", "x"},
		{"profile", "recent", "--to", "x"},
	} {
		out, _, code := runCLISplit(t, append(args, "--json", "--config", cfg)...)
		var m map[string]map[string]any
		if code != 1 || json.Unmarshal([]byte(out), &m) != nil || m["error"]["code"] != codeUsage {
			t.Errorf("%v --json: exit %d, want the usage error object:\n%s", args, code, out)
		}
	}
	if _, errOut, code := runCLISplit(t, "claude", "show", "extra", "--config", cfg); code == 0 ||
		!strings.Contains(errOut, "show takes no arguments") {
		t.Errorf("claude show extra: exit %d\n%s", code, errOut)
	}
}

// The five items are exactly what profile create links from ~/.claude.
func TestSetupItemsAreTheSharedBase(t *testing.T) {
	a := append([]string(nil), sharedBase...)
	b := append([]string(nil), ccsetup.Items...)
	slices.Sort(a)
	slices.Sort(b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("sharedBase %v, ccsetup.Items %v", sharedBase, ccsetup.Items)
	}
}
