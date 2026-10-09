package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile paths (IMPROVEMENTS F4): the folders a profile is picked for.
//
//	[[profile]]
//	name  = "work"
//	dir   = "~/.claude-work"
//	paths = ["~/src/work/**", "~/clients/*/acme"]
//
// A pattern is absolute or from the home (~/…), split on "/". Within one
// folder name `*` is any run of characters and `?` one character; a whole
// `**` is any number of folders, none included. A directory belongs to a
// pattern when the pattern matches it or one of its parents, so "~/src/work"
// and "~/src/work/**" pick the same folders. Character classes and escapes
// are refused, so two patterns can be compared exactly: when some directory
// would belong to two profiles, the config does not load (like overlapping
// pools). One profile's own patterns may overlap.

// expandPattern is a pattern with ~ expanded and cleaned, as components.
func expandPattern(p string) []string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// dirComponents is a directory as components, made absolute and cleaned.
func dirComponents(dir string) []string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return expandPattern(filepath.ToSlash(filepath.Clean(dir)))
}

// validPathPattern says what is wrong with a pattern, "" when nothing.
func validPathPattern(p string) string {
	switch {
	case p == "":
		return "an empty pattern"
	case !relativeOK(p):
		return fmt.Sprintf("%q is relative; write it absolute (/…) or from your home (~/…)", p)
	case strings.ContainsAny(p, "[]\\"):
		return fmt.Sprintf("%q uses [ ] or \\; only *, ? and ** are supported", p)
	}
	for _, c := range strings.Split(p, "/") {
		switch {
		case c == "." || c == "..":
			return fmt.Sprintf("%q has a %q folder; write the path it names", p, c)
		case c != "**" && strings.Contains(c, "**"):
			return fmt.Sprintf("%q: ** must be a whole folder name (…/**/…)", p)
		}
	}
	return ""
}

// matchPath reports whether dir belongs to pattern: the pattern matches it
// or one of its parents.
func matchPath(pattern, dir string) bool {
	return matchComponents(append(expandPattern(pattern), "**"), dirComponents(dir))
}

func matchComponents(pat, dir []string) bool {
	if len(pat) == 0 {
		return len(dir) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(dir); i++ {
			if matchComponents(pat[1:], dir[i:]) {
				return true
			}
		}
		return false
	}
	if len(dir) == 0 {
		return false
	}
	if ok, _ := filepath.Match(pat[0], dir[0]); !ok {
		return false
	}
	return matchComponents(pat[1:], dir[1:])
}

// pathsOverlap reports whether some directory belongs to both patterns. It
// is exact for the supported syntax: a search over pairs of positions in the
// two patterns, folder by folder, and within a folder name character by
// character.
func pathsOverlap(a, b string) bool {
	return seqIntersect(append(expandPattern(a), "**"), append(expandPattern(b), "**"),
		func(x string) bool { return x == "**" }, componentsIntersect)
}

// componentsIntersect reports whether some folder name matches both.
func componentsIntersect(a, b string) bool {
	return seqIntersect([]rune(a), []rune(b), func(x rune) bool { return x == '*' },
		func(x, y rune) bool { return x == '?' || y == '?' || x == y })
}

// seqIntersect decides whether two patterns over sequences share a word. A
// star element (isStar) matches any run of elements; any other pair of
// elements consumes one element each when one could match both (meet).
func seqIntersect[T comparable](a, b []T, isStar func(T) bool, meet func(x, y T) bool) bool {
	star := func(s []T, i int) bool {
		return i < len(s) && isStar(s[i])
	}
	type pos struct{ i, j int }
	seen := map[pos]bool{}
	var walk func(i, j int) bool
	walk = func(i, j int) bool {
		if seen[pos{i, j}] {
			return false
		}
		seen[pos{i, j}] = true
		if i == len(a) && j == len(b) {
			return true
		}
		sa, sb := star(a, i), star(b, j)
		switch {
		case sa && walk(i+1, j): // a's star matches nothing more
			return true
		case sb && walk(i, j+1):
			return true
		case sa && j < len(b) && !sb && walk(i, j+1): // a's star takes b's element
			return true
		case sb && i < len(a) && !sa && walk(i+1, j):
			return true
		case !sa && !sb && i < len(a) && j < len(b) && meet(a[i], b[j]) && walk(i+1, j+1):
			return true
		}
		return false
	}
	return walk(0, 0)
}

// validatePaths checks every profile's patterns and refuses two profiles
// whose patterns overlap.
func (c *Config) validatePaths() error {
	for i, in := range c.Profiles {
		for _, p := range in.Paths {
			if why := validPathPattern(p); why != "" {
				return fmt.Errorf("profile %q: paths: %s", in.Name, why)
			}
		}
		for _, other := range c.Profiles[:i] {
			for _, p := range in.Paths {
				for _, q := range other.Paths {
					if pathsOverlap(p, q) {
						return fmt.Errorf("profiles %q and %q have paths that overlap (%q and %q): "+
							"a folder must pick one profile", other.Name, in.Name, q, p)
					}
				}
			}
		}
	}
	return nil
}

// resolvedPattern is a pattern with the folders before its first wildcard
// resolved through links, "" when that changes nothing or they do not exist.
func resolvedPattern(p string) string {
	comps := expandPattern(p)
	i := 0
	for i < len(comps) && !strings.ContainsAny(comps[i], "*?") {
		i++
	}
	if i == 0 {
		return ""
	}
	prefix := "/" + strings.Join(comps[:i], "/")
	real, err := filepath.EvalSymlinks(prefix)
	if err != nil || real == prefix {
		return ""
	}
	return strings.Join(append([]string{filepath.ToSlash(real)}, comps[i:]...), "/")
}

// ProfileForDir names the profile whose paths a directory belongs to, and
// the pattern that matched; ok is false when none does. Overlaps are refused
// at load, so at most one profile matches. The directory is tried as given,
// then with its links resolved, against each pattern as written and with the
// folders before its first wildcard resolved: the working directory may come
// back either way (macOS: /var is /private/var).
func (c *Config) ProfileForDir(dir string) (name, pattern string, ok bool) {
	try := func(d string) bool {
		for _, in := range c.Profiles {
			for _, p := range in.Paths {
				if matchPath(p, d) {
					name, pattern = in.Name, p
					return true
				}
			}
		}
		for _, in := range c.Profiles {
			for _, p := range in.Paths {
				if r := resolvedPattern(p); r != "" && matchPath(r, d) {
					name, pattern = in.Name, p
					return true
				}
			}
		}
		return false
	}
	if try(dir) {
		return name, pattern, true
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir && try(real) {
		return name, pattern, true
	}
	return "", "", false
}
