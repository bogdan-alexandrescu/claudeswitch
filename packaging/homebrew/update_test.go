// Package homebrew holds the Homebrew formula and cask templates and the
// script that fills them in for a release (IMPROVEMENTS F8). This test runs
// the script; there is no Go code here.
package homebrew

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const zeros = "0000000000000000000000000000000000000000000000000000000000000000"

func sha(c byte) string { return strings.Repeat(string(c), 64) }

// checksums is a release's checksums.txt with the app zip's line appended,
// as the release workflow assembles it.
func checksums(version string, withZip bool) string {
	s := sha('a') + "  claudeswitch_v" + version + "_darwin_amd64.tar.gz\n" +
		sha('b') + "  claudeswitch_v" + version + "_darwin_arm64.tar.gz\n" +
		sha('c') + "  claudeswitch_v" + version + "_linux_amd64.tar.gz\n" +
		sha('d') + "  claudeswitch_v" + version + "_linux_arm64.tar.gz\n"
	if withZip {
		s += sha('e') + "  ClaudeSwitch-" + version + "-macos.zip\n"
	}
	return s
}

// copyTemplates puts the templates in a scratch dir, for the script to edit.
func copyTemplates(t *testing.T) (formula, cask string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"claudeswitch.rb", "claudeswitch-app.rb"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "claudeswitch.rb"), filepath.Join(dir, "claudeswitch-app.rb")
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", append([]string{"update.sh"}, args...)...).CombinedOutput()
	return string(out), err
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The templates carry placeholders, one sha256 per target, the cs link and
// the unsigned app's caveat.
func TestTemplates(t *testing.T) {
	f := read(t, "claudeswitch.rb")
	for _, want := range []string{`version "0.0.0"`, "class Claudeswitch < Formula", `license "MIT"`,
		"on_macos", "on_linux", "on_arm", "on_intel",
		`claudeswitch_v#{version}_darwin_arm64.tar.gz`, `claudeswitch_v#{version}_linux_amd64.tar.gz`,
		`bin.install "claudeswitch"`, `bin.install_symlink "claudeswitch" => "cs"`} {
		if !strings.Contains(f, want) {
			t.Errorf("formula lacks %q", want)
		}
	}
	for _, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		if !strings.Contains(f, `sha256 "`+zeros+`" # `+target) {
			t.Errorf("formula lacks the %s sha256 placeholder", target)
		}
	}
	c := read(t, "claudeswitch-app.rb")
	for _, want := range []string{`cask "claudeswitch-app"`, `version "0.0.0"`, `sha256 "` + zeros + `"`,
		`ClaudeSwitch-#{version}-macos.zip`, `app "ClaudeSwitch.app"`, "caveats", "Open Anyway",
		"com.apple.quarantine"} {
		if !strings.Contains(c, want) {
			t.Errorf("cask lacks %q", want)
		}
	}
}

func TestUpdateFillsVersionAndSums(t *testing.T) {
	formula, cask := copyTemplates(t)
	sums := write(t, checksums("0.5.6", true))
	if out, err := run(t, "v0.5.6", sums, formula, cask); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	f := read(t, formula)
	for target, s := range map[string]string{"darwin_amd64": sha('a'), "darwin_arm64": sha('b'),
		"linux_amd64": sha('c'), "linux_arm64": sha('d')} {
		if !strings.Contains(f, `sha256 "`+s+`" # `+target) {
			t.Errorf("formula %s sha256 not filled:\n%s", target, f)
		}
	}
	if !strings.Contains(f, `version "0.5.6"`) || strings.Contains(f, zeros) {
		t.Errorf("formula:\n%s", f)
	}
	c := read(t, cask)
	if !strings.Contains(c, `version "0.5.6"`) || !strings.Contains(c, `sha256 "`+sha('e')+`"`) {
		t.Errorf("cask:\n%s", c)
	}

	// Run again on the filled files for the next release: every value moves.
	sums = write(t, strings.ReplaceAll(strings.ReplaceAll(checksums("0.5.7", true), sha('a'), sha('f')), sha('e'), sha('9')))
	if out, err := run(t, "0.5.7", sums, formula, cask); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f := read(t, formula); !strings.Contains(f, `version "0.5.7"`) || !strings.Contains(f, `sha256 "`+sha('f')+`" # darwin_amd64`) {
		t.Errorf("second release, formula:\n%s", f)
	}
	if c := read(t, cask); !strings.Contains(c, `version "0.5.7"`) || !strings.Contains(c, `sha256 "`+sha('9')+`"`) {
		t.Errorf("second release, cask:\n%s", c)
	}
}

// A release without the app zip's sum updates the formula and leaves the
// cask as it was, saying so.
func TestUpdateWithoutTheApp(t *testing.T) {
	formula, cask := copyTemplates(t)
	before := read(t, cask)
	out, err := run(t, "0.5.6", write(t, checksums("0.5.6", false)), formula, cask)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, cask) != before || !strings.Contains(out, "cask") {
		t.Errorf("cask changed, or no word about it: %s", out)
	}
	if !strings.Contains(read(t, formula), `version "0.5.6"`) {
		t.Error("formula not updated")
	}
}

// Anything missing or malformed refuses, changing nothing.
func TestUpdateRefuses(t *testing.T) {
	formula, cask := copyTemplates(t)
	f0, c0 := read(t, formula), read(t, cask)
	partial := strings.Join(strings.Split(checksums("0.5.6", true), "\n")[1:], "\n") // no darwin_amd64
	for name, args := range map[string][]string{
		"no args":        {},
		"bad version":    {"0.5", write(t, checksums("0.5", true)), formula, cask},
		"version x":      {"0.5.6;rm", write(t, checksums("0.5.6", true)), formula, cask},
		"missing target": {"0.5.6", write(t, partial), formula, cask},
		"other version":  {"0.5.6", write(t, checksums("0.5.5", true)), formula, cask},
		"bad sha":        {"0.5.6", write(t, strings.Replace(checksums("0.5.6", true), sha('a'), "xyz", 1)), formula, cask},
		"no checksums":   {"0.5.6", filepath.Join(t.TempDir(), "none"), formula, cask},
	} {
		if out, err := run(t, args...); err == nil {
			t.Errorf("%s: accepted:\n%s", name, out)
		}
		if read(t, formula) != f0 || read(t, cask) != c0 {
			t.Fatalf("%s: a refused update changed a file", name)
		}
	}
}

// The script stays portable: POSIX sh, no GNU-only sed -i.
func TestUpdateIsPortable(t *testing.T) {
	s := read(t, "update.sh")
	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Error("not a /bin/sh script")
	}
	if regexp.MustCompile(`sed[^|]*-i`).MatchString(s) {
		t.Error("sed -i differs between BSD and GNU sed")
	}
}
