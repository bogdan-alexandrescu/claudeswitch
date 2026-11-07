// Package ccsetup reads the setup Claude Code takes from one config dir: its
// CLAUDE.md, settings.json, skills, agents, commands, plugins and MCP
// servers, and says whether a profile's copy of each item it can share with
// ~/.claude is shared, its own, or missing (owner picks CC1-CC3,
// IMPROVEMENTS, 2026-10-28). It also makes an item shared or a profile's
// own, and writes the CLAUDE.md or settings.json a profile reads.
//
// What it returns is shown to people and to the app, so it carries less than
// the files do: the names of settings.json's env and never their values, and
// of an MCP server only its type and target (a URL's host, a command's
// name), never its arguments, env, headers or URL path. It never reads a
// credential, the keychain, or any key of .claude.json but mcpServers.
package ccsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Items are what a profile may share with ~/.claude by symlink, in the
// order they are shown: the person's own setup (cmd/claudeswitch's
// sharedBase links the same five into a new profile).
var Items = []string{"CLAUDE.md", "settings.json", "skills", "commands", "agents"}

// IsItem reports whether name is one of Items.
func IsItem(name string) bool {
	for _, it := range Items {
		if it == name {
			return true
		}
	}
	return false
}

// An item's state in a profile's dir.
const (
	Shared    = "shared"    // a link to the same item in ~/.claude
	Elsewhere = "elsewhere" // a link to anywhere else
	Own       = "own"       // a file or folder of the profile's own
	Missing   = "missing"   // absent, or a link to nothing
)

// State is item's state in dir, against the same item in base (~/.claude).
// Shared compares the files the links reach, not the link text: a link to
// ~/.claude/skills through another spelling, or to where ~/.claude/skills
// itself links, is still the same folder.
func State(base, dir, item string) string {
	p := filepath.Join(dir, item)
	li, err := os.Lstat(p)
	if err != nil {
		return Missing
	}
	if li.Mode()&fs.ModeSymlink == 0 {
		return Own
	}
	fi, err := os.Stat(p)
	if err != nil {
		return Missing // a dangling link: there is nothing to read there
	}
	if bi, err := os.Stat(filepath.Join(base, item)); err == nil && os.SameFile(fi, bi) {
		return Shared
	}
	return Elsewhere
}

// Target is where a write to path lands: path itself when it is not a link,
// else the real path its links lead to, followed by hand when the last one
// leads to nothing yet (writing creates it there, as an editor would).
func Target(path string) (string, error) {
	li, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && li.Mode()&fs.ModeSymlink == 0) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	}
	p := path
	for hops := 0; hops < 40; hops++ {
		li, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			parent, perr := filepath.EvalSymlinks(filepath.Dir(p))
			if perr != nil {
				return "", fmt.Errorf("%s links to %s, whose folder does not exist", path, p)
			}
			return filepath.Join(parent, filepath.Base(p)), nil
		}
		if err != nil {
			return "", err
		}
		if li.Mode()&fs.ModeSymlink == 0 {
			return filepath.EvalSymlinks(p)
		}
		t, err := os.Readlink(p)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		p = t
	}
	return "", fmt.Errorf("%s: too many links", path)
}

// Instructions is a CLAUDE.md.
type Instructions struct {
	Path     string // in the dir read, a link or not
	Target   string // the real path read (Path when it is not a link)
	Lines    int
	Modified time.Time
}

// ReadInstructions is dir's CLAUDE.md, nil when there is none to read.
func ReadInstructions(dir string) *Instructions {
	p := filepath.Join(dir, "CLAUDE.md")
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	target, err := Target(p)
	if err != nil {
		target = p
	}
	return &Instructions{Path: p, Target: target, Lines: CountLines(b), Modified: fi.ModTime()}
}

// CountLines counts newlines, and one more for a last line without one.
func CountLines(b []byte) int {
	n := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// Settings is what a settings.json sets, in the few words a summary needs.
type Settings struct {
	Path       string
	Target     string
	Model      string // "" when unset
	Allow      int    // permissions.allow
	Deny       int    // permissions.deny
	Hooks      []string
	StatusLine bool
	Env        []string // the names; a value is never read into here
	Keys       int      // top-level keys: 0 is `{}`, no settings at all
	// Enabled is enabledPlugins' ids set to true.
	Enabled map[string]bool
	// Err is why the file could not be read as settings, "" when it was:
	// every other field is then empty.
	Err string
}

// ReadSettings is dir's settings.json, nil when there is none.
func ReadSettings(dir string) *Settings {
	p := filepath.Join(dir, "settings.json")
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	target, terr := Target(p)
	if terr != nil {
		target = p
	}
	s := &Settings{Path: p, Target: target, Hooks: []string{}, Env: []string{}, Enabled: map[string]bool{}}
	if err != nil {
		s.Err = err.Error()
		return s
	}
	top, err := ParseObject(b)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	s.Keys = len(top)
	var model string
	if json.Unmarshal(top["model"], &model) == nil {
		s.Model = model
	}
	var perms map[string]json.RawMessage
	if json.Unmarshal(top["permissions"], &perms) == nil {
		s.Allow, s.Deny = arrayLen(perms["allow"]), arrayLen(perms["deny"])
	}
	s.Hooks = keysOf(top["hooks"])
	s.Env = keysOf(top["env"])
	if v, ok := top["statusLine"]; ok && string(bytes.TrimSpace(v)) != "null" {
		s.StatusLine = true
	}
	var enabled map[string]json.RawMessage
	if json.Unmarshal(top["enabledPlugins"], &enabled) == nil {
		for id, v := range enabled {
			var on bool
			if json.Unmarshal(v, &on) == nil && on {
				s.Enabled[id] = true
			}
		}
	}
	return s
}

// Summary is the settings in a few words: the model when set, then how many
// rules allow; "no settings" for `{}`.
func (s *Settings) Summary() string {
	switch {
	case s == nil:
		return ""
	case s.Err != "":
		return "not valid JSON"
	case s.Keys == 0:
		return "no settings"
	}
	var parts []string
	if s.Model != "" {
		parts = append(parts, "model "+s.Model)
	}
	parts = append(parts, fmt.Sprintf("%d allowed", s.Allow))
	return strings.Join(parts, " · ")
}

// ParseObject parses b as one JSON object. Its error is the sentence a
// person reads, with the line and column of a syntax error.
func ParseObject(b []byte) (map[string]json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := position(b, syn.Offset)
			return nil, fmt.Errorf("%s (line %d, column %d)", syn.Error(), line, col)
		}
		return nil, err
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, fmt.Errorf("it holds %s, not an object", jsonKind(v))
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	return top, nil
}

// position is the line and column (both from 1, the column in characters)
// of the byte a syntax error stopped at, offset bytes in.
func position(b []byte, offset int64) (int, int) {
	at := int(offset) - 1
	if at < 0 {
		at = 0
	}
	if at > len(b) {
		at = len(b)
	}
	before := b[:at]
	nl := bytes.LastIndexByte(before, '\n')
	return 1 + bytes.Count(before, []byte("\n")), 1 + utf8.RuneCount(before[nl+1:])
}

func jsonKind(v any) string {
	switch v.(type) {
	case []any:
		return "an array"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	}
	return "null"
}

func arrayLen(raw json.RawMessage) int {
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return 0
	}
	return len(a)
}

// keysOf is the keys of a JSON object, sorted; none for anything else.
func keysOf(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	out := []string{}
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Entry is one skill, agent or command.
type Entry struct {
	Name        string
	Description string
	Path        string // the skill's folder, or the agent's or command's file
}

// descriptionMax is how long a description may be, in characters.
const descriptionMax = 200

// Skills is each folder (or link to one) in dir/skills holding a SKILL.md,
// named by its front matter's name, else the folder's. Sorted by name.
func Skills(dir string) []Entry {
	root := filepath.Join(dir, "skills")
	out := []Entry{}
	ents, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range ents {
		p := filepath.Join(root, e.Name())
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		md := filepath.Join(p, "SKILL.md")
		if fi, err := os.Stat(md); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		name, desc := FrontMatter(md)
		if name == "" {
			name = e.Name()
		}
		out = append(out, Entry{Name: name, Description: desc, Path: p})
	}
	sortEntries(out)
	return out
}

// Agents is each dir/agents/*.md, named by its front matter's name, else
// the file's name without .md. Sorted by name.
func Agents(dir string) []Entry {
	root := filepath.Join(dir, "agents")
	out := []Entry{}
	ents, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range ents {
		p := filepath.Join(root, e.Name())
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		name, desc := FrontMatter(p)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, Entry{Name: name, Description: desc, Path: p})
	}
	sortEntries(out)
	return out
}

// Commands is each dir/commands/**/*.md, named as Claude Code calls it: its
// path below commands/ without .md ("git/sync"). Folders inside are walked,
// links to folders are not (one could lead back up); hidden folders are
// skipped. Sorted by name.
func Commands(dir string) []Entry {
	shown := filepath.Join(dir, "commands")
	out := []Entry{}
	root, err := filepath.EvalSymlinks(shown) // commands/ itself is often a link
	if err != nil {
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		_, desc := FrontMatter(p)
		out = append(out, Entry{Name: filepath.ToSlash(strings.TrimSuffix(rel, ".md")), Description: desc,
			Path: filepath.Join(shown, rel)})
		return nil
	})
	sortEntries(out)
	return out
}

func sortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Name != es[j].Name {
			return es[i].Name < es[j].Name
		}
		return es[i].Path < es[j].Path
	})
}

// frontMatterMax is how much of a file is read for its front matter.
const frontMatterMax = 64 << 10

// bom is the byte order mark an editor may put before the first ---.
const bom = "\xef\xbb\xbf"

// FrontMatter is the name and description in a Markdown file's YAML front
// matter: the block from a first line of --- to the next --- line. Each is
// its value's first line, unquoted; the description at most 200
// characters. Only these two keys are read, and only at the top level, so
// this is not a YAML parser and needs none.
func FrontMatter(path string) (name, description string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, frontMatterMax))
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimPrefix(lines[0], bom)) != "---" {
		return "", ""
	}
	var block []string
	closed := false
	for _, l := range lines[1:] {
		if strings.TrimRight(l, " \t") == "---" {
			closed = true
			break
		}
		block = append(block, l)
	}
	if !closed {
		return "", ""
	}
	name = scalar(block, "name")
	description = scalar(block, "description")
	if r := []rune(description); len(r) > descriptionMax {
		description = strings.TrimSpace(string(r[:descriptionMax]))
	}
	return name, description
}

// scalar is the first line of key's value in a front matter block: the rest
// of its line, or for a block scalar (| or >) or a value begun on the next
// line, the first indented line after it.
func scalar(block []string, key string) string {
	for i, l := range block {
		rest, ok := strings.CutPrefix(l, key+":")
		if !ok {
			continue
		}
		v := strings.TrimSpace(rest)
		if v != "" && v[0] != '|' && v[0] != '>' {
			return unquote(v)
		}
		for _, next := range block[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			if next[0] != ' ' && next[0] != '\t' {
				break // the next key: the value was empty
			}
			return unquote(strings.TrimSpace(next))
		}
		return ""
	}
	return ""
}

// unquote takes the quotes off a YAML scalar's first line. A quoted value
// that goes on past this line keeps its opening quote off too.
func unquote(v string) string {
	switch {
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		if s, err := strconv.Unquote(v); err == nil {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(v[1 : len(v)-1])
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		return strings.TrimSpace(strings.ReplaceAll(v[1:len(v)-1], "''", "'"))
	case v[0] == '"' || v[0] == '\'':
		return strings.TrimSpace(v[1:])
	}
	return v
}

// Plugin is one installed plugin.
type Plugin struct {
	ID          string // name@marketplace
	Name        string
	Marketplace string // "" when the id names none
	Version     string // "" when the install record has none
	Enabled     bool
}

// Plugins is dir/plugins/installed_plugins.json's plugins, each with its
// first install record's version, enabled when enabled says so. Sorted by
// id; none for a missing or unreadable file.
func Plugins(dir string, enabled map[string]bool) []Plugin {
	out := []Plugin{}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return out
	}
	var f struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &f) != nil {
		return out
	}
	type record struct {
		Version string `json:"version"`
	}
	for id, raw := range f.Plugins {
		p := Plugin{ID: id, Name: id, Enabled: enabled[id]}
		if i := strings.LastIndex(id, "@"); i > 0 {
			p.Name, p.Marketplace = id[:i], id[i+1:]
		}
		var recs []record
		var one record // the file's first format: one record, not a list
		switch {
		case json.Unmarshal(raw, &recs) == nil && len(recs) > 0:
			p.Version = recs[0].Version
		case json.Unmarshal(raw, &one) == nil:
			p.Version = one.Version
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Server is one MCP server, as much of it as may be shown.
type Server struct {
	Name   string
	Type   string // "" when it says none and has no command
	Target string // a URL's host or a command's name; "" when neither
}

// Servers is the user-scope MCP servers in a .claude.json (its top-level
// mcpServers), sorted by name; none for a missing or unreadable file.
func Servers(globalConfig string) []Server {
	out := []Server{}
	for name, raw := range mcpServers(globalConfig) {
		var s struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			URL     string `json:"url"`
		}
		_ = json.Unmarshal(raw, &s)
		typ := s.Type
		if typ == "" && s.Command != "" {
			typ = "stdio"
		}
		target := commandName(s.Command)
		if typ != "stdio" && s.URL != "" {
			target = urlHost(s.URL)
		}
		out = append(out, Server{Name: name, Type: typ, Target: target})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ServerCount is how many user-scope MCP servers a .claude.json has.
func ServerCount(globalConfig string) int { return len(mcpServers(globalConfig)) }

// mcpServers reads mcpServers alone out of a .claude.json, which also holds
// the signed-in account and every project's state: none of that is kept.
func mcpServers(globalConfig string) map[string]json.RawMessage {
	b, err := os.ReadFile(globalConfig)
	if err != nil {
		return nil
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return f.MCPServers
}

// urlHost is a URL's host alone: no scheme, user, port, path or query,
// which is where a token or a tenant goes. "" when it does not parse as one.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// commandName is a stdio command's program name. A command written as a
// shell line keeps only its program: never an argument, nor a leading
// NAME=value, which is where a secret goes.
func commandName(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if _, err := os.Stat(cmd); err != nil && strings.ContainsAny(cmd, " \t") {
		for _, f := range strings.Fields(cmd) {
			if !strings.Contains(f, "=") {
				return filepath.Base(f)
			}
		}
		return ""
	}
	return filepath.Base(cmd)
}

// Setup is what Claude Code reads from one config dir.
type Setup struct {
	Dir          string
	Instructions *Instructions
	Settings     *Settings
	Skills       []Entry
	Agents       []Entry
	Commands     []Entry
	Plugins      []Plugin
	Servers      []Server
}

// Read is dir's setup as Claude Code sees it, through its links, with the
// MCP servers of its .claude.json at globalConfig.
func Read(dir, globalConfig string) Setup {
	s := Setup{Dir: dir, Instructions: ReadInstructions(dir), Settings: ReadSettings(dir),
		Skills: Skills(dir), Agents: Agents(dir), Commands: Commands(dir), Servers: Servers(globalConfig)}
	var enabled map[string]bool
	if s.Settings != nil {
		enabled = s.Settings.Enabled
	}
	s.Plugins = Plugins(dir, enabled)
	return s
}

// ShareItem makes dir/item a link to base/item, which must exist. An item of
// the profile's own is first renamed beside it to
// <item>.claudeswitch-<YYYYMMDD-HHMMSS> (now, local time): backup is that
// path, "" when nothing was moved (it was missing, or a link). changed is
// false when it was shared already.
func ShareItem(base, dir, item string, now time.Time) (backup string, changed bool, err error) {
	src := filepath.Join(base, item)
	if _, err := os.Stat(src); err != nil {
		return "", false, err
	}
	dst := filepath.Join(dir, item)
	oldLink := ""
	switch State(base, dir, item) {
	case Shared:
		return "", false, nil
	case Own:
		backup = freeName(dst + ".claudeswitch-" + now.Local().Format("20060102-150405"))
		if err := os.Rename(dst, backup); err != nil {
			return "", false, err
		}
	default: // a link elsewhere or to nothing: the link alone goes
		if t, err := os.Readlink(dst); err == nil {
			oldLink = t
			if err := os.Remove(dst); err != nil {
				return "", false, err
			}
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	if err := os.Symlink(src, dst); err != nil {
		switch {
		case backup != "":
			_ = os.Rename(backup, dst)
		case oldLink != "":
			_ = os.Symlink(oldLink, dst)
		}
		return "", false, err
	}
	return backup, true, nil
}

// freeName is p, or p-2, p-3… when p is taken: two shares within a second
// must not rename one backup over the other.
func freeName(p string) string {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return p
	}
	for i := 2; ; i++ {
		q := fmt.Sprintf("%s-%d", p, i)
		if _, err := os.Lstat(q); errors.Is(err, fs.ErrNotExist) {
			return q
		}
	}
}

// OwnItem replaces the link dir/item with a copy of what it leads to: files
// with their mode and times, folders copied whole, links inside copied as
// links. The copy is made beside it first, so a failure leaves the link as
// it was. changed is false when the item was the profile's own already; a
// missing item, or a link to nothing, is an error wrapping fs.ErrNotExist.
func OwnItem(dir, item string) (changed bool, err error) {
	dst := filepath.Join(dir, item)
	li, err := os.Lstat(dst)
	if err != nil {
		return false, err
	}
	if li.Mode()&fs.ModeSymlink == 0 {
		return false, nil
	}
	link, err := os.Readlink(dst)
	if err != nil {
		return false, err
	}
	src, err := filepath.EvalSymlinks(dst)
	if err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(dir, "."+item+".claudeswitch-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, item)
	if err := copyTree(src, staged); err != nil {
		return false, err
	}
	if err := os.Remove(dst); err != nil {
		return false, err
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Symlink(link, dst)
		return false, err
	}
	return true, nil
}

// copyTree copies src to the new path dst: a link as a link, a folder with
// everything in it, a file with its mode and times.
func copyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(t, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		// The folder's own mode last, so a read-only one could be filled.
		return os.Chmod(dst, fi.Mode().Perm())
	case fi.Mode().IsRegular():
		return copyFile(src, dst, fi)
	}
	return fmt.Errorf("%s is not a file, a folder or a link, so it was not copied", src)
}

func copyFile(src, dst string, fi fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, fi.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// Hash is the hex SHA-256 of b: how an editor says which content it opened.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// CurrentHash is the Hash of the file at target, of nothing when it is
// absent.
func CurrentHash(target string) (string, error) {
	b, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return Hash(nil), nil
	}
	if err != nil {
		return "", err
	}
	return Hash(b), nil
}

// WriteFile replaces target with data whole: a temp file in its folder,
// renamed over it, so Claude Code never reads half of one. The file keeps
// its mode; a new one gets mode.
func WriteFile(target string, data []byte, mode fs.FileMode) error {
	if fi, err := os.Stat(target); err == nil {
		if fi.IsDir() {
			return fmt.Errorf("%s is a folder", target)
		}
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".claudeswitch-new-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, target); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ProjectDir is the folder name under projects/ where Claude Code keeps a
// working folder's transcripts: its path with every character but A-Z, a-z
// and 0-9 as "-" (/Users/x/code/a.b is -Users-x-code-a-b). Claude Code
// replaces UTF-16 units, so a character beyond the Basic Multilingual Plane
// is two.
func ProjectDir(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// LastUsed is the newest modification time of a *.jsonl directly in dir,
// zero when there is none.
func LastUsed(dir string) time.Time {
	var newest time.Time
	ents, err := os.ReadDir(dir)
	if err != nil {
		return newest
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}

// Folder is a working folder and when Claude Code last wrote a transcript
// for it (zero: never, or none kept).
type Folder struct {
	Path     string
	LastUsed time.Time
}

// Recent is those of paths that are folders now, each with its last use
// from the transcripts under projects, newest first, never-used last, then
// by path; at most limit.
func Recent(paths []string, projects string, limit int) []Folder {
	out := []Folder{}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, Folder{Path: p, LastUsed: LastUsed(filepath.Join(projects, ProjectDir(p)))})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].LastUsed, out[j].LastUsed
		switch {
		case a.Equal(b):
			return out[i].Path < out[j].Path
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		}
		return a.After(b)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
