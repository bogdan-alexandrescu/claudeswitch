package ccsetup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// write makes the files of body under root, each path's folders included.
func write(t *testing.T, root string, body map[string]string) {
	t.Helper()
	for p, s := range body {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func names(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestFrontMatterReadsNameAndDescription(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("é", 250)
	cases := map[string]struct {
		body, name, desc string
	}{
		"plain":    {"---\nname: release-notes\ndescription: Drafts release notes\n---\nbody\n", "release-notes", "Drafts release notes"},
		"quoted":   {"---\nname: \"quoted name\"\ndescription: 'it''s quoted'\n---\n", "quoted name", "it's quoted"},
		"folded":   {"---\ndescription: >\n  First line of it\n  second line\nname: x\n---\n", "x", "First line of it"},
		"next":     {"---\ndescription:\n  On the next line\n---\n", "", "On the next line"},
		"empty":    {"---\ndescription:\nname: y\n---\n", "y", ""},
		"crlf":     {"---\r\nname: win\r\ndescription: Windows lines\r\n---\r\n", "win", "Windows lines"},
		"bom":      {"\xef\xbb\xbf---\nname: marked\n---\n", "marked", ""},
		"nested":   {"---\nmeta:\n  name: inner\n---\n", "", ""},
		"unclosed": {"---\nname: never-closed\n", "", ""},
		"none":     {"# no front matter\n---\nname: rule\n---\n", "", ""},
		"long":     {"---\ndescription: " + long + "\n---\n", "", strings.Repeat("é", 200)},
	}
	for label, c := range cases {
		p := filepath.Join(dir, label+".md")
		if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		name, desc := FrontMatter(p)
		if name != c.name || desc != c.desc {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", label, name, desc, c.name, c.desc)
		}
	}
}

// Skills are folders holding SKILL.md, links to folders included; agents
// are agents/*.md; commands are every .md below commands/, named by path.
func TestSkillsAgentsAndCommands(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	write(t, dir, map[string]string{
		"skills/zeta/SKILL.md":   "---\nname: alpha-by-name\ndescription: Named in its front matter\n---\n",
		"skills/plain/SKILL.md":  "no front matter\n",
		"skills/empty/notes.txt": "no SKILL.md here\n",
		"skills/file.md":         "a file, not a skill folder\n",
		"agents/reviewer.md":     "---\nname: code-reviewer\ndescription: Reviews diffs\n---\n",
		"agents/helper.md":       "helper\n",
		"agents/notes.txt":       "not an agent\n",
		"commands/standup.md":    "---\ndescription: The stand-up note\n---\n",
		"commands/git/sync.md":   "---\nname: ignored\ndescription: Pull and rebase\n---\n",
		"commands/git/deep/x.md": "x\n",
		"commands/.hidden/h.md":  "hidden\n",
		"commands/readme.txt":    "not a command\n",
	})
	write(t, elsewhere, map[string]string{"linked/SKILL.md": "---\nname: linked\n---\n"})
	link(t, filepath.Join(elsewhere, "linked"), filepath.Join(dir, "skills", "linked"))

	if got, want := names(Skills(dir)), []string{"alpha-by-name", "linked", "plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("skills %v, want %v", got, want)
	}
	if s := Skills(dir)[0]; s.Description != "Named in its front matter" || s.Path != filepath.Join(dir, "skills", "zeta") {
		t.Errorf("skill %+v", s)
	}
	if got, want := names(Agents(dir)), []string{"code-reviewer", "helper"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agents %v, want %v", got, want)
	}
	cmds := Commands(dir)
	if got, want := names(cmds), []string{"git/deep/x", "git/sync", "standup"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
	if cmds[1].Description != "Pull and rebase" || cmds[1].Path != filepath.Join(dir, "commands", "git", "sync.md") {
		t.Errorf("git/sync: %+v", cmds[1])
	}

	// A shared commands/ is read through its link, and shown at the link.
	other := t.TempDir()
	link(t, filepath.Join(dir, "commands"), filepath.Join(other, "commands"))
	if got := Commands(other); len(got) != 3 || got[2].Path != filepath.Join(other, "commands", "standup.md") {
		t.Errorf("commands through a link: %+v", got)
	}
	// Nothing there: empty lists, never nil (JSON [] rather than null).
	if Skills(t.TempDir()) == nil || Agents(t.TempDir()) == nil || Commands(t.TempDir()) == nil {
		t.Error("a dir with nothing must give empty lists")
	}
}

func TestReadSettings(t *testing.T) {
	dir := t.TempDir()
	if ReadSettings(dir) != nil {
		t.Fatal("no settings.json is nil")
	}
	write(t, dir, map[string]string{"settings.json": `{"model": "opus",
		"permissions": {"allow": ["Read", "Edit"], "deny": ["Bash(rm:*)"]},
		"hooks": {"Stop": [], "PreToolUse": []}, "statusLine": {"type": "command"},
		"env": {"HTTP_PROXY": "http://proxy.example:1", "A_TOKEN": "value-never-read"},
		"enabledPlugins": {"on@m": true, "off@m": false, "odd@m": "yes"}}`})
	s := ReadSettings(dir)
	want := &Settings{Path: filepath.Join(dir, "settings.json"), Target: filepath.Join(dir, "settings.json"),
		Model: "opus", Allow: 2, Deny: 1, Hooks: []string{"PreToolUse", "Stop"}, StatusLine: true,
		Env: []string{"A_TOKEN", "HTTP_PROXY"}, Keys: 6, Enabled: map[string]bool{"on@m": true}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("got %+v\nwant %+v", s, want)
	}
	if got := s.Summary(); got != "model opus · 2 allowed" {
		t.Errorf("summary %q", got)
	}

	write(t, dir, map[string]string{"settings.json": "{}"})
	if got := ReadSettings(dir).Summary(); got != "no settings" {
		t.Errorf("{} summary %q", got)
	}
	write(t, dir, map[string]string{"settings.json": `{"theme": "dark", "statusLine": null}`})
	if s := ReadSettings(dir); s.Summary() != "0 allowed" || s.StatusLine {
		t.Errorf("no model, no rules: %q, status line %v", s.Summary(), s.StatusLine)
	}

	// Not valid JSON: every field empty, the error with its line and column.
	write(t, dir, map[string]string{"settings.json": "{\n  \"model\": \"opus\",\n}\n"})
	s = ReadSettings(dir)
	if s.Model != "" || s.Allow != 0 || len(s.Hooks) != 0 || len(s.Env) != 0 || s.Keys != 0 {
		t.Errorf("an invalid file must leave every field empty: %+v", s)
	}
	if !strings.Contains(s.Err, "invalid character '}'") || !strings.Contains(s.Err, "line 3, column 1") {
		t.Errorf("error %q", s.Err)
	}
	if s.Summary() != "not valid JSON" {
		t.Errorf("summary %q", s.Summary())
	}
	write(t, dir, map[string]string{"settings.json": `["an", "array"]`})
	if s := ReadSettings(dir); s.Err != "it holds an array, not an object" {
		t.Errorf("an array: %q", s.Err)
	}
}

func TestReadInstructionsCountsLines(t *testing.T) {
	dir := t.TempDir()
	if ReadInstructions(dir) != nil {
		t.Fatal("no CLAUDE.md is nil")
	}
	for body, want := range map[string]int{"": 0, "one": 1, "one\n": 1, "a\nb\nc": 3, "a\n\n": 2} {
		write(t, dir, map[string]string{"CLAUDE.md": body})
		if got := ReadInstructions(dir).Lines; got != want {
			t.Errorf("%q: %d lines, want %d", body, got, want)
		}
	}
}

func TestPluginsFromTheInstallRecord(t *testing.T) {
	dir := t.TempDir()
	if got := Plugins(dir, nil); got == nil || len(got) != 0 {
		t.Fatalf("no file: %v, want []", got)
	}
	write(t, dir, map[string]string{"plugins/installed_plugins.json": `{"version": 2, "plugins": {
		"slack@claude-plugins-official": [{"version": "1.2.0"}, {"version": "0.9.0"}],
		"cs@claudeswitch": [{"scope": "user", "version": "0.8.19"}],
		"old@market": {"version": "0.1.0"},
		"bare": []}}`})
	got := Plugins(dir, map[string]bool{"cs@claudeswitch": true})
	want := []Plugin{
		{ID: "bare", Name: "bare"},
		{ID: "cs@claudeswitch", Name: "cs", Marketplace: "claudeswitch", Version: "0.8.19", Enabled: true},
		{ID: "old@market", Name: "old", Marketplace: "market", Version: "0.1.0"},
		{ID: "slack@claude-plugins-official", Name: "slack", Marketplace: "claude-plugins-official", Version: "1.2.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// Only a server's type and target leave the file: a URL's host, a
// command's program name. Never its arguments, env, headers, URL path or
// query, nor any other key of .claude.json.
func TestServersShowTypeAndTargetOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude.json")
	write(t, filepath.Dir(p), map[string]string{".claude.json": `{
		"oauthAccount": {"emailAddress": "someone@example.invalid"},
		"mcpServers": {
			"linear": {"type": "http", "url": "https://user:pw@mcp.linear.example:8443/tenant-path?token=q-value",
			           "headers": {"Authorization": "Bearer header-value"}},
			"files": {"command": "/opt/tools/bin/npx", "args": ["-y", "arg-value"], "env": {"KEY": "env-value"}},
			"shell": {"command": "API_KEY=inline-value node server.js --secret arg2-value"},
			"events": {"type": "sse", "url": "https://events.example/stream/tenant-path"},
			"odd": {}
		}}`})
	got := Servers(p)
	want := []Server{{"events", "sse", "events.example"}, {"files", "stdio", "npx"},
		{"linear", "http", "mcp.linear.example"}, {"odd", "", ""}, {"shell", "stdio", "node"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if n := ServerCount(p); n != 5 {
		t.Errorf("count %d", n)
	}
	if got := Servers(filepath.Join(t.TempDir(), "absent.json")); got == nil || len(got) != 0 {
		t.Errorf("no file: %v", got)
	}
}

// The five states, a dangling link among the missing.
func TestStateOfEachItem(t *testing.T) {
	base, dir, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, base, map[string]string{"CLAUDE.md": "x\n", "settings.json": "{}", "skills/a/SKILL.md": "a"})
	write(t, other, map[string]string{"settings.json": "{}"})
	write(t, dir, map[string]string{"commands/c.md": "c"})
	link(t, filepath.Join(base, "CLAUDE.md"), filepath.Join(dir, "CLAUDE.md"))
	link(t, filepath.Join(other, "settings.json"), filepath.Join(dir, "settings.json"))
	// skills through a relative link and a link to the link: still shared.
	link(t, filepath.Join(base, "skills"), filepath.Join(other, "skills-alias"))
	link(t, filepath.Join("..", filepath.Base(other), "skills-alias"), filepath.Join(dir, "skills"))
	link(t, filepath.Join(base, "agents"), filepath.Join(dir, "agents")) // base has none: dangling

	want := map[string]string{"CLAUDE.md": Shared, "settings.json": Elsewhere, "skills": Shared,
		"commands": Own, "agents": Missing}
	for item, w := range want {
		if got := State(base, dir, item); got != w {
			t.Errorf("%s: %s, want %s", item, got, w)
		}
	}
	if got := State(base, t.TempDir(), "CLAUDE.md"); got != Missing {
		t.Errorf("absent: %s", got)
	}
}

func TestShareItemKeepsTheProfilesOwnAsABackup(t *testing.T) {
	base, dir := t.TempDir(), t.TempDir()
	write(t, base, map[string]string{"settings.json": `{"model":"opus"}`, "skills/a/SKILL.md": "a"})
	write(t, dir, map[string]string{"settings.json": `{"model":"sonnet"}`})
	now := time.Date(2026, 10, 28, 10, 15, 0, 0, time.Local)

	backup, changed, err := ShareItem(base, dir, "settings.json", now)
	if err != nil || !changed {
		t.Fatalf("share: %v %v", changed, err)
	}
	if want := filepath.Join(dir, "settings.json.claudeswitch-20261028-101500"); backup != want {
		t.Errorf("backup %q, want %q", backup, want)
	}
	if b, _ := os.ReadFile(backup); string(b) != `{"model":"sonnet"}` {
		t.Errorf("backup holds %q", b)
	}
	if got, _ := os.Readlink(filepath.Join(dir, "settings.json")); got != filepath.Join(base, "settings.json") {
		t.Errorf("link to %q", got)
	}
	// Again: shared already, nothing moved.
	if backup, changed, err := ShareItem(base, dir, "settings.json", now); err != nil || changed || backup != "" {
		t.Errorf("again: %q %v %v", backup, changed, err)
	}
	// Own again within the same second, and share: the first backup stays.
	os.Remove(filepath.Join(dir, "settings.json"))
	write(t, dir, map[string]string{"settings.json": "{}"})
	if backup, _, _ := ShareItem(base, dir, "settings.json", now); !strings.HasSuffix(backup, "-101500-2") {
		t.Errorf("second backup %q", backup)
	}
	// Missing, or a link elsewhere: linked, nothing to back up.
	if backup, changed, err := ShareItem(base, dir, "skills", now); err != nil || !changed || backup != "" {
		t.Errorf("missing: %q %v %v", backup, changed, err)
	}
	if State(base, dir, "skills") != Shared {
		t.Error("skills not shared")
	}
	// Nothing in base to share.
	if _, _, err := ShareItem(base, dir, "agents", now); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("base has no agents: %v", err)
	}
}

func TestOwnItemCopiesWhatTheLinkLeadsTo(t *testing.T) {
	base, dir := t.TempDir(), t.TempDir()
	write(t, base, map[string]string{"CLAUDE.md": "notes\n", "skills/a/SKILL.md": "a", "skills/a/run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(base, "skills", "a", "run.sh"), 0o750); err != nil {
		t.Fatal(err)
	}
	link(t, "a", filepath.Join(base, "skills", "alias")) // a link inside stays a link
	link(t, filepath.Join(base, "CLAUDE.md"), filepath.Join(dir, "CLAUDE.md"))
	link(t, filepath.Join(base, "skills"), filepath.Join(dir, "skills"))

	for _, item := range []string{"CLAUDE.md", "skills"} {
		if changed, err := OwnItem(dir, item); err != nil || !changed {
			t.Fatalf("%s: %v %v", item, changed, err)
		}
		if State(base, dir, item) != Own {
			t.Errorf("%s is not own", item)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(b) != "notes\n" {
		t.Errorf("CLAUDE.md copy %q", b)
	}
	fi, err := os.Stat(filepath.Join(dir, "skills", "a", "run.sh"))
	if err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("run.sh: %v %v", fi, err)
	}
	if got, err := os.Readlink(filepath.Join(dir, "skills", "alias")); err != nil || got != "a" {
		t.Errorf("the link inside: %q %v", got, err)
	}
	// The copy is the profile's: changing it leaves ~/.claude alone.
	write(t, dir, map[string]string{"skills/a/SKILL.md": "changed"})
	if b, _ := os.ReadFile(filepath.Join(base, "skills", "a", "SKILL.md")); string(b) != "a" {
		t.Errorf("the base changed: %q", b)
	}
	// Own already: nothing changes. Missing: not found. No temp left.
	if changed, err := OwnItem(dir, "skills"); err != nil || changed {
		t.Errorf("again: %v %v", changed, err)
	}
	if _, err := OwnItem(dir, "agents"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), "claudeswitch") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func TestTargetFollowsLinks(t *testing.T) {
	base, dir := t.TempDir(), t.TempDir()
	write(t, base, map[string]string{"CLAUDE.md": "x"})
	link(t, filepath.Join(base, "CLAUDE.md"), filepath.Join(dir, "CLAUDE.md"))
	link(t, filepath.Join(base, "settings.json"), filepath.Join(dir, "settings.json")) // to nothing yet
	real, _ := filepath.EvalSymlinks(base)
	if got, _ := Target(filepath.Join(dir, "CLAUDE.md")); got != filepath.Join(real, "CLAUDE.md") {
		t.Errorf("a link: %q", got)
	}
	if got, _ := Target(filepath.Join(dir, "settings.json")); got != filepath.Join(real, "settings.json") {
		t.Errorf("a link to nothing yet: %q", got)
	}
	if got, _ := Target(filepath.Join(base, "CLAUDE.md")); got != filepath.Join(base, "CLAUDE.md") {
		t.Errorf("not a link: %q", got)
	}
	if got, _ := Target(filepath.Join(dir, "absent.md")); got != filepath.Join(dir, "absent.md") {
		t.Errorf("absent: %q", got)
	}
}

func TestWriteFileKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	if err := WriteFile(p, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("new file mode %o", fi.Mode().Perm())
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("kept mode %o", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != "again\n" {
		t.Errorf("content %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("temp files left: %v", ents)
	}
	if h, _ := CurrentHash(filepath.Join(dir, "absent")); h != Hash(nil) ||
		h != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("an absent file hashes as empty: %s", h)
	}
}

func TestProjectDirEncodesAsClaudeCodeDoes(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/x/code/a.b":       "-Users-x-code-a-b",
		"/Users/x/my project_1":   "-Users-x-my-project-1",
		"/Users/x/café":           "-Users-x-caf-",
		"/Users/x/\U0001F600-dir": "-Users-x----dir",
	} {
		if got := ProjectDir(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Newest first, never-used last, then by path; folders that are gone or are
// files left out; at most the limit.
func TestRecentOrdersByLastUse(t *testing.T) {
	home, projects := t.TempDir(), t.TempDir()
	now := time.Now()
	mk := func(name string, used time.Duration) string {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if used >= 0 {
			pd := filepath.Join(projects, ProjectDir(p))
			write(t, pd, map[string]string{"old.jsonl": "{}", "new.jsonl": "{}", "notes.txt": "x"})
			os.Chtimes(filepath.Join(pd, "old.jsonl"), now.Add(-used-time.Hour), now.Add(-used-time.Hour))
			os.Chtimes(filepath.Join(pd, "new.jsonl"), now.Add(-used), now.Add(-used))
			os.Chtimes(filepath.Join(pd, "notes.txt"), now, now) // not a transcript
		}
		return p
	}
	a := mk("a.site", 3*time.Hour)
	b := mk("b", time.Hour)
	c := mk("c", -1)
	d := mk("d", -1)
	file := filepath.Join(home, "file")
	write(t, home, map[string]string{"file": "x"})
	gone := filepath.Join(home, "gone")
	got := Recent([]string{d, a, gone, c, file, b, "relative/path"}, projects, 12)
	var order []string
	for _, f := range got {
		order = append(order, filepath.Base(f.Path))
	}
	if want := []string{"b", "a.site", "c", "d"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	if d := got[0].LastUsed.Sub(now.Add(-time.Hour)).Abs(); d > time.Second {
		t.Errorf("b last used %v, want the newest .jsonl's time", got[0].LastUsed)
	}
	if !got[2].LastUsed.IsZero() {
		t.Errorf("c was never used: %v", got[2].LastUsed)
	}

	var many []string
	for i := 0; i < 15; i++ {
		many = append(many, mk(strings.Repeat("m", i+1), time.Duration(i)*time.Minute))
	}
	if got := Recent(many, projects, 12); len(got) != 12 || filepath.Base(got[0].Path) != "m" {
		t.Errorf("limit: %d folders, first %v", len(got), got[0])
	}
}
