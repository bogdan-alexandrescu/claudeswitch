package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
)

// Folder is the folder a profile dir names on disk, for telling whether two
// dirs are one (lane 10 security review, owner decision). Real is for
// showing; key is what is compared.
type Folder struct {
	Real string
	key  string
}

// RealFolder resolves a dir as the filesystem sees it: ~ expanded, made
// absolute, symlinks resolved as far as the path exists (a dir not made yet
// still resolves through a linked parent), and case-folded on macOS, whose
// default filesystem treats ~/CC and ~/cc as one folder. "" is ~/.claude,
// where the profile with CLAUDE_CONFIG_DIR unset keeps its files.
func RealFolder(dir string) Folder {
	if dir == "" {
		dir = "~/.claude"
	}
	if exp, err := ccdir.ExpandHome(dir); err == nil {
		dir = exp
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dir = filepath.Clean(dir)
	cur, rest := dir, []string(nil)
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			dir = filepath.Join(append([]string{r}, rest...)...)
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
	key := dir
	if runtime.GOOS == "darwin" {
		key = strings.ToLower(key)
	}
	return Folder{Real: dir, key: key}
}

// Overlap says how two folders relate: "same", "inside" (a is inside b),
// "contains" (b is inside a), or "".
func (a Folder) Overlap(b Folder) string {
	sep := string(filepath.Separator)
	switch {
	case a.key == b.key:
		return "same"
	case strings.HasPrefix(a.key, strings.TrimSuffix(b.key, sep)+sep):
		return "inside"
	case strings.HasPrefix(b.key, strings.TrimSuffix(a.key, sep)+sep):
		return "contains"
	}
	return ""
}

// relativeOK reports whether a profile dir names one folder whoever reads
// it: absolute, or from the home directory (~ or ~/…).
func relativeOK(dir string) bool {
	return filepath.IsAbs(dir) || dir == "~" || strings.HasPrefix(dir, "~/")
}

// shownDir is a profile's dir as the config says it, for errors.
func shownDir(in Profile) string {
	if in.Dir == "" {
		return "~/.claude (no dir)"
	}
	return in.Dir
}

// validateFolders refuses two profiles on one folder, or one inside
// another's. They would share transcripts (the daemon's activity and
// refusals) and, on Linux, the credential file; a link between them makes
// one profile's files the other's. D12 allowed the lexical case with a
// warning; every such config is now one of these errors.
func (c *Config) validateFolders() error {
	type pf struct {
		in Profile
		f  Folder
	}
	var seen []pf
	for _, in := range c.Profiles {
		f := RealFolder(in.Dir)
		for _, o := range seen {
			switch o.f.Overlap(f) {
			case "same":
				return fmt.Errorf("profiles %q and %q are the same folder (%s → %s); give each its own dir",
					o.in.Name, in.Name, shownDir(in), f.Real)
			case "inside":
				return fmt.Errorf("profile %q's dir %s is inside profile %q's %s (%s); profile dirs must not nest",
					o.in.Name, shownDir(o.in), in.Name, shownDir(in), o.f.Real)
			case "contains":
				return fmt.Errorf("profile %q's dir %s is inside profile %q's %s (%s); profile dirs must not nest",
					in.Name, shownDir(in), o.in.Name, shownDir(o.in), f.Real)
			}
		}
		seen = append(seen, pf{in, f})
	}
	return nil
}
