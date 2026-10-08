package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// Textual config edits for the app commands. Every edit changes only the
// lines it is about, so comments and layout survive, and is then parsed
// back and checked before it replaces the file (writeConfigFile: mode and
// symlink kept). A layout this misreads is discarded, never installed.

// tomlEntry is one logical line of a config: a table header, a key = value
// (an array value may span lines), or anything else (blank, comment).
type tomlEntry struct {
	start, end int    // byte offsets; end is the '\n' that ends it, or len(text)
	header     string // "account", "profile" or "other" on a table header
	key        string // on a key = value line
	valStart   int    // the value's first byte
	valEnd     int    // just past the value (past ']' for an array, before a comment)
	section    int    // index of the header entry this is under; -1 at top level
}

var keyValueLine = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*`)

// scanTOML splits text into entries.
func scanTOML(text string) []tomlEntry {
	var out []tomlEntry
	sec := -1
	for pos := 0; pos <= len(text); {
		if pos == len(text) {
			break
		}
		eol := strings.IndexByte(text[pos:], '\n')
		if eol < 0 {
			eol = len(text)
		} else {
			eol += pos
		}
		line := text[pos:eol]
		e := tomlEntry{start: pos, end: eol, section: sec}
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "["):
			e.header = "other"
			switch {
			case accountHdr.MatchString(line):
				e.header = "account"
			case profileHdr.MatchString(line):
				e.header = "profile"
			}
			sec = len(out)
			e.section = sec
		default:
			if m := keyValueLine.FindStringSubmatchIndex(line); m != nil {
				e.key = line[m[2]:m[3]]
				e.valStart = pos + m[1]
				if e.valStart < len(text) && text[e.valStart] == '[' {
					if cl, err := arraySpan(text, e.valStart); err == nil {
						e.valEnd = cl + 1
						if nl := strings.IndexByte(text[cl:], '\n'); nl >= 0 {
							e.end = cl + nl
						} else {
							e.end = len(text)
						}
					} else {
						e.valEnd = eol
					}
				} else {
					e.valEnd = scalarEnd(text, e.valStart, eol)
				}
			}
		}
		out = append(out, e)
		pos = e.end + 1
	}
	return out
}

// scalarEnd is where a scalar value starting at from ends: before a
// comment or trailing space, at the latest at eol.
func scalarEnd(text string, from, eol int) int {
	end := eol
	for i := from; i < eol; i++ {
		c := text[i]
		if c == '"' || c == '\'' {
			j := i + 1
			for j < eol && text[j] != c {
				if c == '"' && text[j] == '\\' {
					j++
				}
				j++
			}
			i = j
			continue
		}
		if c == '#' {
			end = i
			break
		}
	}
	for end > from && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return end
}

// arraySpan finds the ']' closing the array opened at text[open], past
// strings and comments.
func arraySpan(text string, open int) (int, error) {
	depth := 0
	for i := open; i < len(text); i++ {
		switch c := text[i]; c {
		case '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case '"', '\'':
			j := i + 1
			for j < len(text) && text[j] != c && text[j] != '\n' {
				if c == '"' && text[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("an array is not closed")
}

// arrayItems reads the string literals of an array value.
func arrayItems(v string) []string {
	var out []string
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case '#':
			for i < len(v) && v[i] != '\n' {
				i++
			}
		case '"', '\'':
			j := i + 1
			for j < len(v) && v[j] != c {
				if c == '"' && v[j] == '\\' {
					j++
				}
				j++
			}
			if j > len(v) {
				j = len(v)
			}
			lit := v[i:min(j+1, len(v))]
			if c == '"' {
				if s, err := strconv.Unquote(lit); err == nil {
					out = append(out, s)
				}
			} else if len(lit) >= 2 {
				out = append(out, lit[1:len(lit)-1])
			}
			i = j
		}
	}
	return out
}

// blockRef names where a key lives: the top level (kind ""), or the
// [[account]] with that id, or the [[profile]] with that name.
type blockRef struct{ kind, name string }

func (b blockRef) String() string {
	switch b.kind {
	case "":
		return "the top level"
	case "account":
		return fmt.Sprintf("the [[account]] block %q", b.name)
	}
	return fmt.Sprintf("the [[profile]] block %q", b.name)
}

// literal is a scalar string value's contents.
func literal(text string, e tomlEntry) string {
	v := text[e.valStart:e.valEnd]
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// findSection is the header index of ref (-1 for the top level), and the
// entry naming it (its id or name line; -1 at top level).
func findSection(text string, es []tomlEntry, ref blockRef) (sec, nameAt int, ok bool) {
	if ref.kind == "" {
		return -1, -1, true
	}
	nameKey := "name"
	if ref.kind == "account" {
		nameKey = "id"
	}
	for i, e := range es {
		if e.header != ref.kind {
			continue
		}
		for j := i + 1; j < len(es) && es[j].section == i; j++ {
			if es[j].key == nameKey && literal(text, es[j]) == ref.name {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// keyIn is the entry index of key in section sec, or -1.
func keyIn(es []tomlEntry, sec int, key string) int {
	for i, e := range es {
		if e.section == sec && e.header == "" && e.key == key {
			return i
		}
	}
	return -1
}

// setKey sets key = value (value already TOML) in ref, replacing the value
// where the key is written, else adding the line: at the top level after
// its last key, in a block under its id or name line.
func setKey(text string, ref blockRef, key, value string) (string, error) {
	es := scanTOML(text)
	sec, nameAt, ok := findSection(text, es, ref)
	if !ok {
		return "", fmt.Errorf("no %s in the config", ref)
	}
	if k := keyIn(es, sec, key); k >= 0 {
		e := es[k]
		return text[:e.valStart] + value + text[e.valEnd:], nil
	}
	line := key + " = " + value + "\n"
	at := 0
	if sec < 0 {
		for _, e := range es {
			if e.section == -1 && e.key != "" {
				at = min(e.end+1, len(text))
			}
		}
		if at == 0 && len(es) > 0 && es[0].header == "" {
			// No top-level key at all: above the first table.
			for _, e := range es {
				if e.header != "" {
					at = e.start
					break
				}
				at = min(e.end+1, len(text))
			}
		}
	} else {
		at = min(es[nameAt].end+1, len(text))
	}
	if at == len(text) && at > 0 && text[at-1] != '\n' {
		line = "\n" + line
	}
	return text[:at] + line + text[at:], nil
}

// deleteKey removes key's line from ref; absent is no change.
func deleteKey(text string, ref blockRef, key string) (string, error) {
	es := scanTOML(text)
	sec, _, ok := findSection(text, es, ref)
	if !ok {
		return "", fmt.Errorf("no %s in the config", ref)
	}
	k := keyIn(es, sec, key)
	if k < 0 {
		return text, nil
	}
	e := es[k]
	return text[:e.start] + text[min(e.end+1, len(text)):], nil
}

// mapArray rewrites the array value of key in ref through fn, written back
// on one line. found is false when ref has no such key.
func mapArray(text string, ref blockRef, key string, fn func([]string) []string) (out string, found bool, err error) {
	es := scanTOML(text)
	sec, _, ok := findSection(text, es, ref)
	if !ok {
		return "", false, fmt.Errorf("no %s in the config", ref)
	}
	k := keyIn(es, sec, key)
	if k < 0 {
		return text, false, nil
	}
	e := es[k]
	v := text[e.valStart:e.valEnd]
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return "", true, fmt.Errorf("%s in %s is not a list", key, ref)
	}
	items := fn(arrayItems(v))
	return text[:e.valStart] + tomlList(items) + text[e.valEnd:], true, nil
}

// tomlList renders a list of strings on one line.
func tomlList(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// deleteBlock removes the [[account]] or [[profile]] block ref, with any
// comment lines directly above its header; comments directly above the
// next header stay with it.
func deleteBlock(text string, ref blockRef) (string, error) {
	es := scanTOML(text)
	sec, _, ok := findSection(text, es, ref)
	if !ok || sec < 0 {
		return "", fmt.Errorf("no %s in the config", ref)
	}
	last := sec
	for j := sec + 1; j < len(es) && es[j].section == sec; j++ {
		last = j
	}
	// Trailing comments and blanks belong to whatever follows.
	cutAt := last
	for cutAt > sec && isTrivia(text, es[cutAt]) {
		cutAt--
	}
	end := min(es[cutAt].end+1, len(text))
	start := es[sec].start
	for k := sec - 1; k >= 0; k-- {
		t := strings.TrimSpace(text[es[k].start:es[k].end])
		if !strings.HasPrefix(t, "#") {
			break
		}
		start = es[k].start
	}
	return text[:start] + text[end:], nil
}

func isTrivia(text string, e tomlEntry) bool {
	if e.header != "" || e.key != "" {
		return false
	}
	t := strings.TrimSpace(text[e.start:e.end])
	return t == "" || strings.HasPrefix(t, "#")
}

// loadForEdit loads the config a command is about to edit: a missing file
// is codeNoConfig, one that does not load codeConfigInvalid.
func loadForEdit(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	switch {
	case err == nil:
		return cfg, nil
	case cfg != nil && errors.Is(err, os.ErrNotExist):
		return nil, appErr(codeNoConfig, "run `claudeswitch setup` first", "no config at %s", cfg.Path)
	}
	return nil, wrapErr(codeConfigInvalid, "", err)
}

// editConfigText applies edit to the config file's text and installs the
// result once it loads and check passes. A missing file is codeNoConfig;
// an edit that would not load is codeConfigInvalid.
func editConfigText(path string, edit func(string) (string, error), check func(*config.Config) error) error {
	if path == "" {
		path = config.DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return appErr(codeNoConfig, "run `claudeswitch setup` first", "no config at %s", path)
	}
	target, err := configTarget(path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	out, err := edit(string(raw))
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if out == string(raw) {
		return nil
	}
	if err := writeConfigFile(target, []byte(out), check); err != nil {
		return wrapErr(codeConfigInvalid, "nothing was written", err)
	}
	return nil
}
