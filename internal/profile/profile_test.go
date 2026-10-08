package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// fakeServices replaces both keychain resolutions, recording what was asked,
// so no test reaches the keychain.
type fakeServices struct {
	forDir map[string]string
	env    string
	err    error
	asked  []string
}

func (f *fakeServices) install(t *testing.T) {
	t.Helper()
	oldFor, oldEnv := serviceForDir, serviceFromEnv
	t.Cleanup(func() { serviceForDir, serviceFromEnv = oldFor, oldEnv })
	serviceForDir = func(dir string) (string, error) {
		f.asked = append(f.asked, "dir:"+dir)
		if f.err != nil {
			return "", f.err
		}
		return f.forDir[dir], nil
	}
	serviceFromEnv = func() (string, error) {
		f.asked = append(f.asked, "env")
		if f.err != nil {
			return "", f.err
		}
		return f.env, nil
	}
}

func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	return h
}

func TestResolveDeclaredDir(t *testing.T) {
	h := home(t)
	f := &fakeServices{forDir: map[string]string{"~/.claude-work": "Claude Code-credentials-abcd1234"}}
	f.install(t)
	r, err := Resolve(config.Profile{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1"}})
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(h, ".claude-work")
	want := Resolved{
		Name:           "work",
		Dir:            work,
		Projects:       filepath.Join(work, "projects"),
		Identity:       filepath.Join(work, ".claude.json"),
		CredentialFile: filepath.Join(work, ".credentials.json"),
		Service:        "Claude Code-credentials-abcd1234",
		Pool:           []string{"w1"},
	}
	if r.Name != want.Name || r.Dir != want.Dir || r.Projects != want.Projects ||
		r.Identity != want.Identity || r.CredentialFile != want.CredentialFile ||
		r.Service != want.Service || len(r.Pool) != 1 {
		t.Fatalf("Resolve = %+v\nwant      %+v", r, want)
	}
	// D8: the keychain name is hashed from the string as written, ~ and all.
	if len(f.asked) != 1 || f.asked[0] != "dir:~/.claude-work" {
		t.Fatalf("asked %v, want the dir as written", f.asked)
	}
}

// Omitting dir is the CLAUDE_CONFIG_DIR-unset profile: ~/.claude for files,
// ~/.claude.json for identity, the bare keychain item.
func TestResolveOmittedDirIsTheUnsetProfile(t *testing.T) {
	h := home(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere") // must not leak in
	f := &fakeServices{forDir: map[string]string{"": "Claude Code-credentials"}}
	f.install(t)
	r, err := Resolve(config.Profile{Name: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != filepath.Join(h, ".claude") || r.Identity != filepath.Join(h, ".claude.json") {
		t.Fatalf("paths = %+v", r)
	}
	if r.Service != "Claude Code-credentials" {
		t.Fatalf("Service = %s, want the bare item", r.Service)
	}
	if len(f.asked) != 1 || f.asked[0] != "dir:" {
		t.Fatalf("asked %v", f.asked)
	}
}

// The implicit profile (no [[profile]] blocks) resolves from the
// environment exactly as claudeswitch does today.
func TestResolveImplicitUsesTheEnvironment(t *testing.T) {
	h := home(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/.claude-work")
	os.Unsetenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	f := &fakeServices{env: "Claude Code-credentials-env00000"}
	f.install(t)
	r, err := Resolve(config.Profile{Name: "default", FromEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != filepath.Join(h, ".claude-work") || r.Service != "Claude Code-credentials-env00000" {
		t.Fatalf("Resolve = %+v", r)
	}
	if len(f.asked) != 1 || f.asked[0] != "env" {
		t.Fatalf("asked %v, want the env resolution", f.asked)
	}
}

// A failed keychain resolution still returns the paths, so doctor can report
// the directory and the credential separately.
func TestResolveKeepsPathsWhenTheServiceFails(t *testing.T) {
	h := home(t)
	f := &fakeServices{err: errors.New("not logged in")}
	f.install(t)
	r, err := Resolve(config.Profile{Name: "work", Dir: "~/.claude-work"})
	if err == nil {
		t.Fatal("want the service error")
	}
	if r.Dir != filepath.Join(h, ".claude-work") || r.Service != "" {
		t.Fatalf("Resolve = %+v", r)
	}
}
