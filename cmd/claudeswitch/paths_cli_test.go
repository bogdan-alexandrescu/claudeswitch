package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// IMPROVEMENTS F4: [[profile]] paths pick a profile by folder for `cs run`
// with no name, `cs profile which` and the optional shell hook.

const pathsCLIConfig = createAccounts + `
[[profile]]
name = "default"
pool = ["a1"]

[[profile]]
name  = "work"
dir   = "/somewhere/.claude-work"
pool  = ["a2"]
paths = ["~/src/work/**"]
`

func whichJSON(t *testing.T, cfgPath, dir string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := profileWhich(&buf, cfgPath, dir, true, false); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return m
}

func TestProfileWhich(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, pathsCLIConfig)
	inWork := filepath.Join(home, "src", "work", "api")

	m := whichJSON(t, path, inWork)
	for k, want := range map[string]any{"dir": inWork, "profile": "work", "pattern": "~/src/work/**",
		"rule": "paths", "config_dir": "/somewhere/.claude-work"} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v (%v)", k, m[k], want, m)
		}
	}

	m = whichJSON(t, path, filepath.Join(home, "src", "personal"))
	for k, want := range map[string]any{"profile": "default", "pattern": nil, "rule": "default",
		"config_dir": nil} {
		if m[k] != want {
			t.Errorf("unmatched: %s = %v, want %v", k, m[k], want)
		}
	}

	var buf bytes.Buffer
	if err := profileWhich(&buf, path, inWork, false, false); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "work") || !strings.Contains(got, "~/src/work/**") {
		t.Errorf("text: %q", got)
	}
}

// With no default profile and no match, nothing is picked.
func TestProfileWhichNoDefault(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createAccounts+`
[[profile]]
name  = "work"
dir   = "/somewhere/.claude-work"
pool  = ["a1", "a2", "a3"]
paths = ["~/src/work"]
`)
	m := whichJSON(t, path, filepath.Join(home, "elsewhere"))
	if m["profile"] != nil || m["rule"] != "none" {
		t.Errorf("no default: %v", m)
	}
	var buf bytes.Buffer
	if err := profileWhich(&buf, path, filepath.Join(home, "elsewhere"), false, true); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "keep\n" {
		t.Errorf("--hook with nothing picked = %q, want keep", buf.String())
	}
}

// --hook is the one line the shell hook reads.
func TestProfileWhichHookLine(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, pathsCLIConfig)
	for dir, want := range map[string]string{
		filepath.Join(home, "src", "work"): "set /somewhere/.claude-work\n",
		filepath.Join(home, "src"):         "unset\n", // default omits dir
	} {
		var buf bytes.Buffer
		if err := profileWhich(&buf, path, dir, false, true); err != nil {
			t.Fatal(err)
		}
		if buf.String() != want {
			t.Errorf("--hook in %s = %q, want %q", dir, buf.String(), want)
		}
	}
	// No [[profile]] blocks: the hook leaves the environment alone.
	plain := writeConfig(t, createAccounts)
	var buf bytes.Buffer
	if err := profileWhich(&buf, plain, home, false, true); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "keep\n" {
		t.Errorf("implicit profile --hook = %q", buf.String())
	}
}

func TestRunPicksTheProfileByFolder(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, pathsCLIConfig)
	capture := fakeClaude(t)
	old, oldNotes := cliLiveFor, runNotes
	t.Cleanup(func() { cliLiveFor, runNotes = old, oldNotes })
	cliLiveFor = rigLiveFor
	var notes bytes.Buffer
	runNotes = &notes
	work := filepath.Join(home, "src", "work", "api")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	chdirT(t, work)
	if err := cmdRun([]string{"--config", path, "--", "--resume"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, capture); got != "dir=/somewhere/.claude-work\nsecure=<unset>\narg=--resume\n" {
		t.Errorf("claude saw:\n%s", got)
	}
	if !strings.Contains(notes.String(), `profile "work"`) {
		t.Errorf("no note naming the picked profile: %q", notes.String())
	}

	chdirT(t, home)
	if err := cmdRun([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, capture); got != "dir=<unset>\nsecure=<unset>\n" {
		t.Errorf("outside every path, default runs; claude saw:\n%s", got)
	}
}

func TestRunWithNoNameAndNoDefaultRefuses(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createAccounts+`
[[profile]]
name  = "work"
dir   = "/somewhere/.claude-work"
pool  = ["a1", "a2", "a3"]
paths = ["~/src/work"]
`)
	fakeClaude(t)
	chdirT(t, home)
	err := cmdRun([]string{"--config", path})
	if err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("no profile for this folder and no default: %v", err)
	}
}

func TestProfileListJSONHasPaths(t *testing.T) {
	createHome(t)
	path := writeConfig(t, pathsCLIConfig)
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = rigLiveFor
	var buf bytes.Buffer
	if err := profileListJSON(&buf, path); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profiles []struct {
			Name  string    `json:"name"`
			Paths *[]string `json:"paths"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Profiles {
		if p.Paths == nil {
			t.Fatalf("%s: no paths key: %s", p.Name, buf.String())
		}
		want := ""
		if p.Name == "work" {
			want = "~/src/work/**"
		}
		if strings.Join(*p.Paths, ",") != want {
			t.Errorf("%s paths = %q", p.Name, *p.Paths)
		}
	}
}

func TestProfileSetPaths(t *testing.T) {
	createHome(t)
	path := writeConfig(t, pathsCLIConfig)
	var buf bytes.Buffer
	if err := profileSet(&buf, path, "default", "paths", "~/src/personal/**, /srv/home", true); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["key"] != "paths" || len(m["override"].([]any)) != 2 || len(m["effective"].([]any)) != 2 {
		t.Errorf("set paths = %v", m)
	}
	if !strings.Contains(readFile(t, path), `paths = ["~/src/personal/**", "/srv/home"]`) {
		t.Errorf("config:\n%s", readFile(t, path))
	}

	// An overlap with work's paths is refused, and nothing is written.
	before := readFile(t, path)
	err := profileSet(&buf, path, "default", "paths", "~/src/**", true)
	if got := errCode(t, err); got != codeInvalidValue {
		t.Errorf("overlap: code %q (%v)", got, err)
	}
	if readFile(t, path) != before {
		t.Error("a refused paths edit changed the config")
	}

	buf.Reset()
	if err := profileSet(&buf, path, "default", "paths", "inherit", true); err != nil {
		t.Fatal(err)
	}
	m = nil
	_ = json.Unmarshal(buf.Bytes(), &m)
	if m["override"] != nil || len(m["effective"].([]any)) != 0 {
		t.Errorf("inherit = %v", m)
	}
	if strings.Contains(readFile(t, path), "~/src/personal") {
		t.Errorf("paths not removed:\n%s", readFile(t, path))
	}
}

func TestProfileHookScripts(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		var buf bytes.Buffer
		if err := profileHook(&buf, shell); err != nil {
			t.Fatal(err)
		}
		s := buf.String()
		for _, want := range []string{"claudeswitch profile which --hook", "CLAUDE_CONFIG_DIR"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s hook lacks %q:\n%s", shell, want, s)
			}
		}
		// It only sets or unsets CLAUDE_CONFIG_DIR: never eval, never source
		// what the binary prints, never export anything else. (The comment
		// lines say how to load the hook itself.)
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			for _, bad := range []string{"eval", "source", ". <("} {
				if strings.Contains(line, bad) {
					t.Errorf("%s hook contains %q: %q", shell, bad, line)
				}
			}
			if strings.Contains(line, "export ") && !strings.Contains(line, "export CLAUDE_CONFIG_DIR=") {
				t.Errorf("%s hook exports something else: %q", shell, line)
			}
			if strings.Contains(line, "set -gx ") && !strings.Contains(line, "set -gx CLAUDE_CONFIG_DIR ") {
				t.Errorf("%s hook exports something else: %q", shell, line)
			}
		}
	}
	if err := profileHook(&bytes.Buffer{}, "tcsh"); errCode(t, err) != codeUsage {
		t.Errorf("unknown shell: %v", err)
	}
}

// The bash hook, run under bash: a fake claudeswitch answers --hook by
// folder, and CLAUDE_CONFIG_DIR follows each cd.
func TestProfileHookRunsUnderBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	var hook bytes.Buffer
	if err := profileHook(&hook, "bash"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	for _, d := range []string{bin, filepath.Join(root, "zz-alpha", "api"), filepath.Join(root, "zz-beta")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fake := "#!/bin/sh\n[ \"$1 $2 $3\" = 'profile which --hook' ] || exit 9\n" +
		"case \"$PWD\" in\n  */zz-alpha*) echo 'set /x/.claude work';;\n  */zz-beta*) echo unset;;\n  *) echo keep;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "claudeswitch"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	hookFile := filepath.Join(root, "hook.bash")
	if err := os.WriteFile(hookFile, hook.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `. "$HOOK"
show() { eval "$PROMPT_COMMAND"; echo "${CLAUDE_CONFIG_DIR-<unset>}"; }
cd "$ROOT/zz-alpha/api"; show
cd "$ROOT/zz-beta"; show
export CLAUDE_CONFIG_DIR=/mine
cd "$ROOT"; show
show
`
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", script)
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOOK=" + hookFile, "ROOT=" + root,
		"CLAUDE_CONFIG_DIR=/start"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Sourcing runs the hook once for the starting folder (keep).
	want := "/x/.claude work\n<unset>\n/mine\n/mine\n"
	if string(out) != want {
		t.Errorf("bash hook:\n%s\nwant:\n%s", out, want)
	}
}

// chdirT is t.Chdir for Go 1.23.
func chdirT(t *testing.T, dir string) {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(was) })
}
