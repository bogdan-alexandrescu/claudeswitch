package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// Legacy is what a config still carries of account scope and [project]
// directory rules. Both were removed in lane 16 (IMPROVEMENTS S1): profiles
// do the separation now. A file that has them still loads; they are
// ignored, warned about once, and `claudeswitch config clean` deletes them.
type Legacy struct {
	// Scope lists the accounts, by id, whose block has a scope line.
	Scope []string
	// Projects counts the [project.…] tables (or [[project]] blocks).
	Projects int
}

// Empty reports whether there is nothing legacy.
func (l Legacy) Empty() bool { return len(l.Scope) == 0 && l.Projects == 0 }

// Legacy is what the loaded file still carries of scope and project rules.
func (c *Config) Legacy() Legacy { return c.legacy }

// LegacyWarning is the one warning about Legacy, "" when there is none.
func (c *Config) LegacyWarning() string {
	l := c.legacy
	if l.Empty() {
		return ""
	}
	var parts []string
	if len(l.Scope) > 0 {
		parts = append(parts, "scope lines (accounts "+strings.Join(l.Scope, ", ")+")")
	}
	switch l.Projects {
	case 0:
	case 1:
		parts = append(parts, "1 [project] table")
	default:
		parts = append(parts, fmt.Sprintf("%d [project] tables", l.Projects))
	}
	return fmt.Sprintf("%s has %s: account scope and project rules were removed, so they are ignored. "+
		"Delete them with: claudeswitch config clean", c.Path, strings.Join(parts, " and "))
}

// findLegacy reads the scope lines and project tables out of the raw file.
// A file that does not parse yields nothing: the real decode reports it.
func findLegacy(raw string) Legacy {
	var doc struct {
		Account []map[string]any `toml:"account"`
		Project any              `toml:"project"`
	}
	if _, err := toml.Decode(raw, &doc); err != nil {
		return Legacy{}
	}
	var l Legacy
	for _, a := range doc.Account {
		if _, ok := a["scope"]; ok {
			id, _ := a["id"].(string)
			l.Scope = append(l.Scope, id)
		}
	}
	switch p := doc.Project.(type) {
	case map[string]any:
		l.Projects = len(p)
	case []map[string]any:
		l.Projects = len(p)
	case []any:
		l.Projects = len(p)
	}
	return l
}
