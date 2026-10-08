package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Lane 16 (S1): scope and [project] rules are gone. A config that still has
// them loads, and says once that they are ignored and how to delete them.

const legacyTOML = `[[account]]
id = "a1"
scope = "personal"

[[account]]
id = "w1"
scope = "work"

[[account]]
id = "w2"

[project."~/work/**"]
eligible = ["work"]

# personal projects
[project."~/home/**"]
prefer = ["personal"]
`

func TestLegacyScopeAndProjectsLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(legacyTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("a config with scope lines must still load: %v", err)
	}
	lg := c.Legacy()
	if !slices.Equal(lg.Scope, []string{"a1", "w1"}) || lg.Projects != 2 {
		t.Fatalf("legacy = %+v", lg)
	}
	w := c.LegacyWarning()
	for _, want := range []string{"a1", "w1", "2 [project] tables", "ignored", "claudeswitch config clean"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning lacks %q:\n%s", want, w)
		}
	}
	if strings.Count(w, "\n") > 1 {
		t.Errorf("one warning, not several:\n%s", w)
	}
}

func TestNoLegacyNoWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[account]]\nid = \"a1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if lg := c.Legacy(); len(lg.Scope) != 0 || lg.Projects != 0 || c.LegacyWarning() != "" {
		t.Fatalf("legacy %+v, warning %q", lg, c.LegacyWarning())
	}
}
