package detector

import (
	"os"
	"path/filepath"
	"testing"
)

// Transcripts live in <config dir>/projects. A detector watching ~/.claude
// while Claude Code writes elsewhere sees no activity and no rejections.
func TestProjectsRootFollowsClaudeConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", d)
	if got, want := ProjectsRoot(), filepath.Join(d, "projects"); got != want {
		t.Fatalf("ProjectsRoot() = %s, want %s", got, want)
	}
}

func TestProjectsRootDefaultsToDotClaude(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	if got, want := ProjectsRoot(), filepath.Join(h, ".claude", "projects"); got != want {
		t.Fatalf("ProjectsRoot() = %s, want %s", got, want)
	}
}
