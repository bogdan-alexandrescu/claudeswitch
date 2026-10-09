package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F4: [[profile]] paths are globs over directories. A directory belongs to a
// pattern when the pattern matches it or one of its parents; `**` is any
// number of folders, zero included.
func TestPathMatches(t *testing.T) {
	for _, c := range []struct {
		pattern, dir string
		want         bool
	}{
		{"/src/work/**", "/src/work", true},
		{"/src/work/**", "/src/work/api/internal", true},
		{"/src/work", "/src/work", true},
		{"/src/work", "/src/work/api", true}, // a parent matches
		{"/src/work", "/src/workshop", false},
		{"/src/work", "/src", false},
		{"/src/*/api", "/src/team-a/api/cmd", true},
		{"/src/*/api", "/src/team-a/web", false},
		{"/src/team-?", "/src/team-b/x", true},
		{"/src/team-?", "/src/team-bc", false},
		{"/src/**/client-*", "/src/a/b/client-x/y", true},
		{"/src/**/client-*", "/src/client-x", true},
		{"/src/**/client-*", "/src/a/b/server", false},
		{"/src/work/", "/src/work/x", true}, // trailing slash ignored
	} {
		if got := matchPath(c.pattern, c.dir); got != c.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", c.pattern, c.dir, got, c.want)
		}
	}
}

// Two patterns overlap when some directory could belong to both.
func TestPathsOverlap(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"/src/work/**", "/src/personal/**", false},
		{"/src/work/**", "/src/work/api", true}, // nested
		{"/src/work", "/src/work/api", true},    // a parent covers its children
		{"/src/*", "/src/work", true},
		{"/src/*/api", "/src/*/web", false},
		{"/src/*/api", "/src/x/**", true},
		{"/src/**/api", "/srv/x/web", false},
		{"/src/**/api", "/src/x/web", true}, // /src/x/web/api is under both
		{"/src/**/api", "/src/x/**", true},
		{"/src/team-*", "/src/*-b", true},
		{"/src/team-*", "/src/club-*", false},
		{"/src/a?c", "/src/abd", false},
		{"/src/a?c", "/src/a*", true},
		{"/src/**", "/home/**", false},
		{"/**", "/home/x", true},
	} {
		if got := pathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := pathsOverlap(c.b, c.a); got != c.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v (reversed)", c.b, c.a, got, c.want)
		}
	}
}

const pathsConfig = profileAccounts + `
[[profile]]
name  = "default"
pool  = ["personal", "a4"]

[[profile]]
name  = "work"
dir   = "~/.claude-work"
pool  = ["work-1", "work-2"]
paths = ["~/src/work/**", "/srv/work-*"]
`

func TestProfilePathsLoad(t *testing.T) {
	c := mustLoad(t, pathsConfig)
	in, ok := c.ProfileNamed("work")
	if !ok || strings.Join(in.Paths, "|") != "~/src/work/**|/srv/work-*" {
		t.Fatalf("work paths = %q", in.Paths)
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{filepath.Join(home, "src/work"), filepath.Join(home, "src/work/api"),
		"/srv/work-x/y"} {
		name, pattern, ok := c.ProfileForDir(dir)
		if !ok || name != "work" || pattern == "" {
			t.Errorf("ProfileForDir(%q) = %q %q %v, want work", dir, name, pattern, ok)
		}
	}
	if name, _, ok := c.ProfileForDir(filepath.Join(home, "src/personal")); ok {
		t.Errorf("an unmatched dir picked %q", name)
	}
}

func TestProfilePathsOverlapRefused(t *testing.T) {
	_, err := loadString(t, profileAccounts+`
[[profile]]
name  = "default"
pool  = ["personal", "a4"]
paths = ["~/src/**"]

[[profile]]
name  = "work"
dir   = "~/.claude-work"
pool  = ["work-1", "work-2"]
paths = ["~/src/work/**"]
`)
	if err == nil || !strings.Contains(err.Error(), `"default"`) || !strings.Contains(err.Error(), `"work"`) ||
		!strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlapping paths loaded: %v", err)
	}
	// One profile's own patterns may overlap each other.
	if _, err := loadString(t, profileAccounts+`
[[profile]]
name  = "default"
pool  = ["personal", "a4", "work-1", "work-2"]
paths = ["~/src/**", "~/src/work"]
`); err != nil {
		t.Fatalf("a profile's own overlapping patterns refused: %v", err)
	}
}

func TestProfilePathsInvalid(t *testing.T) {
	for _, p := range []string{`"src/**"`, `""`, `"/src/[ab]"`, `"/src/a\\b"`, `"/src/../etc"`, `"/src/a**"`} {
		_, err := loadString(t, profileAccounts+`
[[profile]]
name  = "default"
pool  = ["personal", "a4", "work-1", "work-2"]
paths = [`+p+`]
`)
		if err == nil || !strings.Contains(err.Error(), "paths") {
			t.Errorf("paths = [%s] loaded: %v", p, err)
		}
	}
}

// Write keeps paths, so a config rewritten by setup does not lose them.
func TestProfilePathsWritten(t *testing.T) {
	c := mustLoad(t, pathsConfig)
	var b strings.Builder
	c.writeProfiles(&b)
	if !strings.Contains(b.String(), `paths            = ["~/src/work/**", "/srv/work-*"]`) {
		t.Fatalf("paths not written:\n%s", b.String())
	}
}

// A pattern written through a link matches the folder however it is
// spelled: the working directory often comes back with links resolved
// (macOS: /var is /private/var).
func TestProfileForDirThroughLinks(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name  = "default"
pool  = ["personal", "a4", "work-1", "work-2"]
paths = ["~/link/**"]
`)
	home, _ := os.UserHomeDir()
	real := filepath.Join(home, "real")
	if err := os.MkdirAll(filepath.Join(real, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "link")); err != nil {
		t.Skip(err)
	}
	for _, dir := range []string{filepath.Join(home, "link", "proj"), filepath.Join(real, "proj")} {
		if name, _, ok := c.ProfileForDir(dir); !ok || name != "default" {
			t.Errorf("ProfileForDir(%q) = %q %v", dir, name, ok)
		}
	}
}
