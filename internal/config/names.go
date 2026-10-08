package config

import (
	"errors"
	"fmt"
	"strings"
)

// BadName is one account id or profile name the name rule refuses.
type BadName struct {
	Kind string // "account id" or "profile name"
	Name string
	Err  error
}

// NameError is a config whose only names-related fault is names that break
// the rule. It lists every one, each with the command or edit that fixes it,
// so a config written before the rule can be repaired from the message alone
// (owner decision, lane 7: strict, with a working rename).
type NameError struct{ Bad []BadName }

func (e *NameError) Error() string {
	var b strings.Builder
	for i, n := range e.Bad {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(n.Err.Error())
		if n.Kind == "account id" {
			fmt.Fprintf(&b, "\n  fix: cs rename %s <new-id>", shellWord(n.Name))
		} else {
			b.WriteString("\n  fix: change that [[profile]]'s name in the config")
		}
	}
	return b.String()
}

// shellWord quotes s for a shell when it needs it.
func shellWord(s string) string {
	safe := s != ""
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("._-+@%=:,/", c)) {
			safe = false
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nameError checks every account id and profile name, nil when all pass.
func (c *Config) nameError() *NameError {
	var bad []BadName
	for _, a := range c.Accounts {
		if a.ID == "" {
			continue // "needs an id" is validateRest's
		}
		if err := ValidName("account id", a.ID); err != nil {
			bad = append(bad, BadName{"account id", a.ID, err})
		}
	}
	for _, in := range c.Profiles {
		if in.Name == "" {
			continue
		}
		if err := ValidName("profile name", in.Name); err != nil {
			bad = append(bad, BadName{"profile name", in.Name, err})
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return &NameError{Bad: bad}
}

// LoadAllowingBadNames loads a config whose only fault is names that break
// the rule, returning it together with the *NameError, so `rename` can repair
// it. Any other fault refuses as Load does.
func LoadAllowingBadNames(path string) (*Config, error) {
	c, err := decode(path)
	if err != nil {
		return c, err
	}
	ne := c.nameError()
	if rest := c.validateRest(); rest != nil {
		if ne != nil {
			return nil, fmt.Errorf("%s: %w", path, errors.Join(ne, rest))
		}
		return nil, fmt.Errorf("%s: %w", path, rest)
	}
	if ne != nil {
		return c, fmt.Errorf("%s: %w", path, ne)
	}
	return c, nil
}

// MaxNameLen bounds an account id or profile name.
const MaxNameLen = 64

// ValidName checks an account id or a profile name: ASCII letters, digits,
// '.', '_' and '-', not starting with '-' or '.', no "..", 1 to 64 bytes.
// kind says which it is, for the error ("account id", "profile name").
//
// Both become parts of other names: keychain items (written through
// `security -i`, where a newline ends one command and starts another, and
// escapeForSecurity only quotes), recovery item names and, on Linux, recovery
// file names. A plain name is safe in all of them, so nothing else loads.
func ValidName(kind, s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%s is empty", kind)
	case len(s) > MaxNameLen:
		return fmt.Errorf("%s %q is longer than %d characters", kind, s, MaxNameLen)
	case s[0] == '-' || s[0] == '.':
		return fmt.Errorf("%s %q starts with %q; names start with a letter, a digit or _", kind, s, s[0])
	case strings.Contains(s, ".."):
		return fmt.Errorf("%s %q contains \"..\"", kind, s)
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("%s %q contains %q; use only letters, digits, '.', '_' and '-'", kind, s, c)
	}
	return nil
}
