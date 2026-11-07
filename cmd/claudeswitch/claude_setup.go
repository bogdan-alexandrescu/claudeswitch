package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccsetup"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// Claude Code's own setup, per profile (owner picks OC1, CC1-CC3,
// IMPROVEMENTS, 2026-10-28): the folders Claude Code was used in under a
// profile (`profile recent`), what it reads from ~/.claude and what each
// profile shares with it (`claude show`), sharing an item or making it a
// profile's own (`profile share|own`), replacing the CLAUDE.md or
// settings.json a profile reads (`claude write`, for the app's editor), and
// turning a plugin on or off (`claude plugin`). The app's contract is
// docs/APP_CLI.md.
//
// None of these reads the state, a credential or the keychain: they are
// about files in Claude Code's dirs, and ccsetup keeps what they show to
// what may be shown.

// recentLimit is how many folders `profile recent` lists per profile.
const recentLimit = 12

// writeMax is the most `claude write` takes on stdin.
const writeMax = 1 << 20

// claudePluginTimeout bounds `claude plugin enable|disable`. A var for tests.
var claudePluginTimeout = 30 * time.Second

const (
	profileRecentUsage = "usage: claudeswitch profile recent [<name>] [--json]"
	profileShareUsage  = "usage: claudeswitch profile share <name> <item> [--json]\n" +
		"  <item> is CLAUDE.md, settings.json, skills, commands or agents; the profile's own one is kept\n" +
		"  beside it as <item>.claudeswitch-<date>-<time>"
	profileOwnUsage = "usage: claudeswitch profile own <name> <item> [--json]\n" +
		"  <item> is CLAUDE.md, settings.json, skills, commands or agents"
	claudeShowUsage   = "usage: claudeswitch claude show [--profile NAME] [--json]"
	claudeWriteUsage  = "usage: claudeswitch claude write CLAUDE.md|settings.json [--profile NAME] [--if-unchanged SHA256] [--json]\n  the new content on stdin"
	claudePluginUsage = "usage: claudeswitch claude plugin enable|disable <plugin> [--profile NAME] [--json]"
	claudeUsage       = "usage: claudeswitch claude show [--profile NAME] [--json]\n" +
		"       claudeswitch claude write CLAUDE.md|settings.json [--profile NAME] [--if-unchanged SHA256] [--json]\n" +
		"       claudeswitch claude plugin enable|disable <plugin> [--profile NAME] [--json]\n" +
		"  what Claude Code reads from ~/.claude, or from one profile; write takes the new content on stdin"
)

// itemsHint lists the items share and own take.
var itemsHint = "the items are " + strings.Join(ccsetup.Items[:len(ccsetup.Items)-1], ", ") + " and " +
	ccsetup.Items[len(ccsetup.Items)-1]

// setupConfig is the config these commands read: the profiles alone. A
// missing config is the one implicit profile, as everywhere.
func setupConfig(cfgPath string) (*config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return nil, wrapErr(codeConfigInvalid, "", err)
	}
	return cfg, nil
}

// setupProfile is the profile called name, "" meaning default; an unknown
// one is invalid_value, as the contract has it for these commands.
func setupProfile(cfg *config.Config, name string) (config.Profile, error) {
	if name == "" {
		name = config.DefaultProfile
	}
	if in, ok := cfg.ProfileNamed(name); ok {
		return in, nil
	}
	return config.Profile{}, appErr(codeInvalidValue, "the profiles are "+strings.Join(quoteAll(cfg.ProfileNames()), ", "),
		"no profile named %q", name)
}

// ccPathsOf is profile in's Claude Code paths: from this shell for the
// implicit profile, else from its dir as configured.
func ccPathsOf(in config.Profile) (ccdir.Paths, error) {
	if in.FromEnv {
		return ccdir.FromEnv()
	}
	return ccdir.For(in.Dir)
}

// basePaths is ~/.claude and ~/.claude.json: Claude Code with
// CLAUDE_CONFIG_DIR unset, which every other profile shares from.
func basePaths() (ccdir.Paths, error) { return ccdir.For("") }

// isBaseDir reports whether dir is ~/.claude, compared as real paths (a
// profile whose dir links there reads the very same files).
func isBaseDir(dir string) bool {
	return config.RealFolder(dir).Overlap(config.RealFolder("")) == "same"
}

// profileRecent is `profile recent [<name>]`: the folders Claude Code has
// been used in under each profile, newest first (OC1: where Open Claude
// Code starts).
func profileRecent(w io.Writer, cfgPath, name string, asJSON bool) error {
	cfg, err := setupConfig(cfgPath)
	if err != nil {
		return err
	}
	profiles := cfg.EffectiveProfiles()
	if name != "" {
		in, err := setupProfile(cfg, name)
		if err != nil {
			return err
		}
		profiles = []config.Profile{in}
	}
	type block struct {
		name    string
		folders []ccsetup.Folder
	}
	var blocks []block
	for _, in := range profiles {
		paths, err := ccPathsOf(in)
		if err != nil {
			return wrapErr(codeFailed, "", err)
		}
		// projects' keys are the folders: nothing else of .claude.json is kept.
		blocks = append(blocks, block{in.Name, ccsetup.Recent(claudeProjects(paths.GlobalConfig), paths.Projects, recentLimit)})
	}
	if asJSON {
		list := []map[string]any{}
		for _, b := range blocks {
			folders := []map[string]any{}
			for _, f := range b.folders {
				folders = append(folders, map[string]any{"path": f.Path, "last_used": secondsOrNull(f.LastUsed)})
			}
			list = append(list, map[string]any{"profile": b.name, "folders": folders})
		}
		return emitTo(w, map[string]any{"profiles": list})
	}
	now := time.Now()
	fmt.Fprintln(w)
	for _, b := range blocks {
		indent := "  "
		if name == "" {
			fmt.Fprintf(w, "  %s\n", b.name)
			indent = "    "
		}
		if len(b.folders) == 0 {
			fmt.Fprintf(w, "%sno folders yet\n", indent)
		}
		for _, f := range b.folders {
			age := "-"
			if !f.LastUsed.IsZero() {
				age = ageShort(max(now.Sub(f.LastUsed), 0))
			}
			fmt.Fprintf(w, "%s%-5s %s\n", indent, age, tilde(f.Path))
		}
	}
	fmt.Fprintln(w)
	return nil
}

// secondsOrNull is t in RFC 3339 UTC to the second, or JSON null for the
// zero time.
func secondsOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// claudeShow is `claude show [--profile NAME]`: what Claude Code reads from
// ~/.claude and what every profile shares with it (CC1), or with a name,
// what that one profile reads, through its links.
func claudeShow(w io.Writer, cfgPath, name string, asJSON bool) error {
	cfg, err := setupConfig(cfgPath)
	if err != nil {
		return err
	}
	base, err := basePaths()
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	if name != "" {
		return claudeShowProfile(w, cfg, base, name, asJSON)
	}
	s := ccsetup.Read(base.Dir, base.GlobalConfig)
	type row struct {
		in      config.Profile
		paths   ccdir.Paths
		base    bool
		states  []string
		summary []string
		plugins []ccsetup.Plugin
		servers int
	}
	var rows []row
	for _, in := range cfg.EffectiveProfiles() {
		paths, err := ccPathsOf(in)
		if err != nil {
			return wrapErr(codeFailed, "", err)
		}
		r := row{in: in, paths: paths, base: isBaseDir(paths.Dir)}
		if !r.base {
			for _, it := range ccsetup.Items {
				st := ccsetup.State(base.Dir, paths.Dir, it)
				r.states = append(r.states, st)
				r.summary = append(r.summary, itemSummary(paths.Dir, it, st))
			}
			r.plugins = ccsetup.Plugins(paths.Dir, enabledIn(ccsetup.ReadSettings(paths.Dir)))
			r.servers = ccsetup.ServerCount(paths.GlobalConfig)
		}
		rows = append(rows, r)
	}
	if asJSON {
		m := setupJSON(s)
		profiles := []map[string]any{}
		for _, r := range rows {
			p := map[string]any{"name": r.in.Name, "dir": r.paths.Dir, "base": r.base}
			if !r.base {
				items := []map[string]any{}
				for i, it := range ccsetup.Items {
					items = append(items, map[string]any{"item": it, "state": r.states[i], "summary": r.summary[i]})
				}
				p["items"], p["plugins"], p["servers"] = items, pluginsJSON(r.plugins), r.servers
			}
			profiles = append(profiles, p)
		}
		m["profiles"] = profiles
		return emitTo(w, m)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", tilde(base.Dir))
	writeSetup(w, s, time.Now())
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  profiles")
	for _, r := range rows {
		fmt.Fprintf(w, "    %-14s %s\n", r.in.Name, shownDir(r.paths.Dir, r.base))
		if r.base {
			continue
		}
		for i, it := range ccsetup.Items {
			fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("      %-14s %-9s %s", it, r.states[i], r.summary[i]), " "))
		}
		fmt.Fprintf(w, "      %-14s %s\n", "plugins", pluginsLine(r.plugins))
		fmt.Fprintf(w, "      %-14s %d\n", "MCP servers", r.servers)
	}
	fmt.Fprintln(w)
	return nil
}

// claudeShowProfile is `claude show --profile NAME` (amendment 1, for the
// one pane with a profile picker, CC3): what that profile reads, through
// its links, and which of the five items it shares with ~/.claude.
func claudeShowProfile(w io.Writer, cfg *config.Config, base ccdir.Paths, name string, asJSON bool) error {
	in, err := setupProfile(cfg, name)
	if err != nil {
		return err
	}
	paths, err := ccPathsOf(in)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	s := ccsetup.Read(paths.Dir, paths.GlobalConfig)
	isBase := isBaseDir(paths.Dir)
	var states []string
	if !isBase {
		for _, it := range ccsetup.Items {
			states = append(states, ccsetup.State(base.Dir, paths.Dir, it))
		}
	}
	if asJSON {
		m := setupJSON(s)
		m["profile"], m["base"] = in.Name, isBase
		shares := []map[string]any{}
		for i, st := range states {
			shares = append(shares, map[string]any{"item": ccsetup.Items[i], "state": st})
		}
		m["shares"] = shares
		return emitTo(w, m)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  profile %s: %s\n", in.Name, shownDir(paths.Dir, isBase))
	writeSetup(w, s, time.Now())
	if !isBase {
		writeShares(w, states)
	}
	fmt.Fprintln(w)
	return nil
}

// shownDir is a profile's dir for people: ~-abbreviated, and said to be
// ~/.claude when it is that folder under another name.
func shownDir(dir string, base bool) string {
	if base && tilde(dir) != "~/.claude" {
		return tilde(dir) + " (~/.claude itself)"
	}
	return tilde(dir)
}

// itemSummary is what a profile reads at item, in a few words: "" when it
// reads nothing there.
func itemSummary(dir, item, state string) string {
	if state == ccsetup.Missing {
		return ""
	}
	switch item {
	case "CLAUDE.md":
		if in := ccsetup.ReadInstructions(dir); in != nil {
			return render.Plural(in.Lines, "line", "lines")
		}
	case "settings.json":
		return ccsetup.ReadSettings(dir).Summary()
	case "skills":
		return render.Plural(len(ccsetup.Skills(dir)), "skill", "skills")
	case "commands":
		return render.Plural(len(ccsetup.Commands(dir)), "command", "commands")
	case "agents":
		return render.Plural(len(ccsetup.Agents(dir)), "agent", "agents")
	}
	return ""
}

func enabledIn(s *ccsetup.Settings) map[string]bool {
	if s == nil {
		return nil
	}
	return s.Enabled
}

// setupJSON is the keys `claude show --json` gives for one dir.
func setupJSON(s ccsetup.Setup) map[string]any {
	m := map[string]any{"dir": s.Dir, "instructions": nil, "settings": nil,
		"skills": entriesJSON(s.Skills), "agents": entriesJSON(s.Agents), "commands": entriesJSON(s.Commands),
		"plugins": pluginsJSON(s.Plugins), "servers": serversJSON(s.Servers)}
	if in := s.Instructions; in != nil {
		m["instructions"] = map[string]any{"path": in.Path, "target": in.Target, "lines": in.Lines,
			"modified": in.Modified.UTC().Format(time.RFC3339)}
	}
	if st := s.Settings; st != nil {
		m["settings"] = map[string]any{"path": st.Path, "target": st.Target, "model": orNull(st.Model),
			"allow": st.Allow, "deny": st.Deny, "hooks": st.Hooks, "status_line": st.StatusLine, "env": st.Env,
			"error": orNull(st.Err)}
	}
	return m
}

func entriesJSON(es []ccsetup.Entry) []map[string]any {
	out := []map[string]any{}
	for _, e := range es {
		out = append(out, map[string]any{"name": e.Name, "description": e.Description, "path": e.Path})
	}
	return out
}

func pluginsJSON(ps []ccsetup.Plugin) []map[string]any {
	out := []map[string]any{}
	for _, p := range ps {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "marketplace": orNull(p.Marketplace),
			"version": orNull(p.Version), "enabled": p.Enabled})
	}
	return out
}

func serversJSON(ss []ccsetup.Server) []map[string]any {
	out := []map[string]any{}
	for _, s := range ss {
		out = append(out, map[string]any{"name": s.Name, "type": orNull(s.Type), "target": orNull(s.Target)})
	}
	return out
}

// writeSetup prints one dir's setup, a row per kind.
func writeSetup(w io.Writer, s ccsetup.Setup, now time.Time) {
	const label, rest = "    %-14s ", "                   "
	if in := s.Instructions; in == nil {
		fmt.Fprintf(w, label+"none\n", "CLAUDE.md")
	} else {
		fmt.Fprintf(w, label+"%s, changed %s ago%s\n", "CLAUDE.md", render.Plural(in.Lines, "line", "lines"),
			ageShort(max(now.Sub(in.Modified), 0)), linkNote(in.Path, in.Target))
	}
	switch st := s.Settings; {
	case st == nil:
		fmt.Fprintf(w, label+"none\n", "settings.json")
	case st.Err != "":
		writeWrapped(w, "not valid JSON: "+st.Err+linkNote(st.Path, st.Target), fmt.Sprintf(label, "settings.json"), rest, false)
	default:
		parts := []string{}
		if st.Model != "" {
			parts = append(parts, "model "+st.Model)
		}
		parts = append(parts, fmt.Sprintf("%d allowed", st.Allow), fmt.Sprintf("%d denied", st.Deny))
		if len(st.Hooks) > 0 {
			parts = append(parts, "hooks "+strings.Join(st.Hooks, ", "))
		}
		if st.StatusLine {
			parts = append(parts, "status line")
		}
		if len(st.Env) > 0 {
			parts = append(parts, "env "+strings.Join(st.Env, ", "))
		}
		writeWrapped(w, strings.Join(parts, " · ")+linkNote(st.Path, st.Target), fmt.Sprintf(label, "settings.json"), rest, false)
	}
	writeEntries(w, "skills", s.Skills)
	writeEntries(w, "agents", s.Agents)
	writeEntries(w, "commands", s.Commands)
	if len(s.Plugins) == 0 {
		fmt.Fprintf(w, label+"none\n", "plugins")
	}
	for i, p := range s.Plugins {
		l := ""
		if i == 0 {
			l = "plugins"
		}
		v := ""
		if p.Version != "" {
			v = " " + p.Version
		}
		fmt.Fprintf(w, label+"%s%s, %s\n", l, p.ID, v, onOff(p.Enabled))
	}
	if len(s.Servers) == 0 {
		fmt.Fprintf(w, label+"none\n", "MCP servers")
	}
	width := 0
	for _, sv := range s.Servers {
		width = max(width, len([]rune(sv.Name)))
	}
	for i, sv := range s.Servers {
		l := ""
		if i == 0 {
			l = "MCP servers"
		}
		fmt.Fprintf(w, label+"%-*s  %s\n", l, width, sv.Name,
			strings.Join(nonEmptyOf(nonEmpty(sv.Type, "no type"), sv.Target), ", "))
	}
}

// writeEntries prints skills, agents or commands, one a line, each name in
// a column and its description cut to the line.
func writeEntries(w io.Writer, kind string, es []ccsetup.Entry) {
	if len(es) == 0 {
		fmt.Fprintf(w, "    %-14s none\n", kind)
		return
	}
	width := 0
	for _, e := range es {
		width = max(width, len([]rune(e.Name)))
	}
	width = min(width, 28)
	for i, e := range es {
		l := ""
		if i == 0 {
			l = kind
		}
		line := fmt.Sprintf("    %-14s %-*s  %s", l, width, e.Name, e.Description)
		fmt.Fprintln(w, strings.TrimRight(render.Fit(line, render.Width()), " "))
	}
}

// writeShares prints, for one profile, which items it shares with
// ~/.claude, which are its own, and so on.
func writeShares(w io.Writer, states []string) {
	by := map[string][]string{}
	for i, st := range states {
		by[st] = append(by[st], ccsetup.Items[i])
	}
	var parts []string
	for _, st := range []string{ccsetup.Shared, ccsetup.Own, ccsetup.Elsewhere, ccsetup.Missing} {
		if len(by[st]) > 0 {
			word := map[string]string{ccsetup.Shared: "shared with ~/.claude", ccsetup.Own: "its own",
				ccsetup.Elsewhere: "linked elsewhere", ccsetup.Missing: "missing"}[st]
			parts = append(parts, word+": "+strings.Join(by[st], ", "))
		}
	}
	writeWrapped(w, strings.Join(parts, " · "), "    "+fmt.Sprintf("%-14s ", "shares"), "                   ", false)
}

// linkNote says where a file read through a link really is: "" when it is
// not a link.
func linkNote(path, target string) string {
	if target == "" || target == path {
		return ""
	}
	return " (→ " + shownPath(target) + ")"
}

// shownPath is tilde for a path that may be a real one: a home folder
// reached through a link (macOS's /var is /private/var) still shows as ~.
func shownPath(p string) string {
	if s := tilde(p); s != p {
		return s
	}
	if home, err := os.UserHomeDir(); err == nil {
		if real, err := filepath.EvalSymlinks(home); err == nil && strings.HasPrefix(p, real+"/") {
			return "~" + p[len(real):]
		}
	}
	return p
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func nonEmptyOf(v ...string) []string {
	var out []string
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pluginsLine is a profile's plugins on one line.
func pluginsLine(ps []ccsetup.Plugin) string {
	if len(ps) == 0 {
		return "none"
	}
	var parts []string
	for _, p := range ps {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.ID, onOff(p.Enabled)))
	}
	return strings.Join(parts, ", ")
}

// shareTarget is the profile share and own act on: a known profile that is
// not ~/.claude itself, and one of the five items, with the profile's
// paths.
func shareTarget(cfgPath, name, item string) (config.Profile, ccdir.Paths, ccdir.Paths, error) {
	cfg, err := setupConfig(cfgPath)
	if err != nil {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, err
	}
	in, err := setupProfile(cfg, name)
	if err != nil {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, err
	}
	if !ccsetup.IsItem(item) {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, appErr(codeInvalidValue, itemsHint,
			"%q is not an item a profile shares with ~/.claude", item)
	}
	paths, err := ccPathsOf(in)
	if err != nil {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, wrapErr(codeFailed, "", err)
	}
	if isBaseDir(paths.Dir) {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, appErr(codeInvalidValue,
			"share and own act on another profile's dir", "%s is ~/.claude itself", in.Name)
	}
	base, err := basePaths()
	if err != nil {
		return config.Profile{}, ccdir.Paths{}, ccdir.Paths{}, wrapErr(codeFailed, "", err)
	}
	return in, paths, base, nil
}

// profileShare is `profile share <name> <item>`: the profile's item becomes
// a link to ~/.claude's, its own one kept beside it (CC3).
func profileShare(w io.Writer, cfgPath, name, item string, asJSON bool) error {
	in, paths, base, err := shareTarget(cfgPath, name, item)
	if err != nil {
		return err
	}
	src := filepath.Join(base.Dir, item)
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return appErr(codeNotFound, "create it in ~/.claude first", "%s does not exist, so there is nothing to share",
				tilde(src))
		}
		return wrapErr(codeFailed, "", err)
	}
	backup, changed, err := ccsetup.ShareItem(base.Dir, paths.Dir, item, time.Now())
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	if asJSON {
		return emitTo(w, map[string]any{"profile": in.Name, "item": item, "state": ccsetup.Shared, "backup": orNull(backup)})
	}
	switch {
	case !changed:
		fmt.Fprintf(w, "  %s: %s is shared with ~/.claude already; nothing changed\n", in.Name, item)
	case backup != "":
		fmt.Fprintf(w, "  %s: %s is shared with ~/.claude now; its own was kept as %s\n", in.Name, item, tilde(backup))
	default:
		fmt.Fprintf(w, "  %s: %s is shared with ~/.claude now\n", in.Name, item)
	}
	return nil
}

// profileOwn is `profile own <name> <item>`: a shared item becomes a copy
// of the profile's own, which changes apart from ~/.claude from then on.
func profileOwn(w io.Writer, cfgPath, name, item string, asJSON bool) error {
	in, paths, base, err := shareTarget(cfgPath, name, item)
	if err != nil {
		return err
	}
	dst := filepath.Join(paths.Dir, item)
	if ccsetup.State(base.Dir, paths.Dir, item) == ccsetup.Missing {
		return appErr(codeNotFound, "", "%s has no %s to copy (%s)", in.Name, item, tilde(dst))
	}
	from, _ := filepath.EvalSymlinks(dst)
	changed, err := ccsetup.OwnItem(paths.Dir, item)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	if asJSON {
		return emitTo(w, map[string]any{"profile": in.Name, "item": item, "state": ccsetup.Own})
	}
	if !changed {
		fmt.Fprintf(w, "  %s: %s is its own already; nothing changed\n", in.Name, item)
		return nil
	}
	fmt.Fprintf(w, "  %s: %s is its own copy now, of %s; changes to it stay in %s\n", in.Name, item, shownPath(from), in.Name)
	return nil
}

// claudeWrite is `claude write <item>`: the CLAUDE.md or settings.json a
// profile reads, replaced with what is on stdin (CC2: the app's editor).
// Where the profile shares the file, the shared one is what changes.
func claudeWrite(w io.Writer, cfgPath, item, name, ifUnchanged string, in io.Reader, asJSON bool) error {
	if item != "CLAUDE.md" && item != "settings.json" {
		return appErr(codeInvalidValue, "claude write takes CLAUDE.md or settings.json", "%q is not a file claude write replaces", item)
	}
	if ifUnchanged != "" && !isSHA256(ifUnchanged) {
		return appErr(codeInvalidValue, "", "--if-unchanged takes the hex SHA-256 of the content opened, 64 characters")
	}
	cfg, err := setupConfig(cfgPath)
	if err != nil {
		return err
	}
	prof, err := setupProfile(cfg, name)
	if err != nil {
		return err
	}
	paths, err := ccPathsOf(prof)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	data, err := io.ReadAll(io.LimitReader(in, writeMax+1))
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	if len(data) > writeMax {
		return appErr(codeInvalidValue, "", "the new %s is larger than 1 MiB; nothing was written", item)
	}
	if item == "settings.json" {
		if _, err := ccsetup.ParseObject(data); err != nil {
			return appErr(codeInvalidValue, "", "settings.json is not valid JSON: %v", err)
		}
	}
	target, err := ccsetup.Target(filepath.Join(paths.Dir, item))
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	if ifUnchanged != "" {
		cur, err := ccsetup.CurrentHash(target)
		if err != nil {
			return wrapErr(codeFailed, "", err)
		}
		if !strings.EqualFold(cur, ifUnchanged) {
			return appErr(codeConfigChanged, "reopen it to see the change", "%s changed since it was opened; nothing was written",
				shownPath(target))
		}
	}
	if filepath.Dir(target) == paths.Dir {
		if err := os.MkdirAll(paths.Dir, 0o700); err != nil { // a profile not started yet
			return wrapErr(codeFailed, "", err)
		}
	}
	mode := fs.FileMode(0o644)
	if item == "settings.json" {
		mode = 0o600
	}
	if err := ccsetup.WriteFile(target, data, mode); err != nil {
		return wrapErr(codeFailed, "", err)
	}
	shared := sharedWith(cfg, prof.Name, item, target)
	if asJSON {
		return emitTo(w, map[string]any{"path": target, "bytes": len(data), "sha256": ccsetup.Hash(data),
			"shared_with": shared})
	}
	fmt.Fprintf(w, "  wrote %s (%s)\n", shownPath(target), render.Plural(len(data), "byte", "bytes"))
	switch len(shared) {
	case 0:
	case 1:
		fmt.Fprintf(w, "  profile %s reads it too\n", shared[0])
	default:
		fmt.Fprintf(w, "  profiles %s read it too\n", strings.Join(shared, ", "))
	}
	return nil
}

// sharedWith names the profiles other than self whose item is the file at
// target.
func sharedWith(cfg *config.Config, self, item, target string) []string {
	out := []string{}
	ti, err := os.Stat(target)
	if err != nil {
		return out
	}
	for _, in := range cfg.EffectiveProfiles() {
		if in.Name == self {
			continue
		}
		paths, err := ccPathsOf(in)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(filepath.Join(paths.Dir, item)); err == nil && os.SameFile(fi, ti) {
			out = append(out, in.Name)
		}
	}
	return out
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range strings.ToLower(s) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// claudePlugin is `claude plugin enable|disable <plugin>`: Claude Code
// itself turns the plugin on or off (CC2: plugins are switched through it,
// never by writing its settings for it), in the profile (pluginEnv), found
// on PATH as `run` finds it, and given 30 seconds.
func claudePlugin(w io.Writer, cfgPath, verb, plugin, name string, asJSON bool) error {
	if plugin == "" || strings.HasPrefix(plugin, "-") {
		return appErr(codeInvalidValue, "", "%q is not a plugin id (name@marketplace)", plugin)
	}
	cfg, err := setupConfig(cfgPath)
	if err != nil {
		return err
	}
	in, err := setupProfile(cfg, name)
	if err != nil {
		return err
	}
	paths, err := ccPathsOf(in)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return appErr(codeFailed, "install Claude Code, or put claude on PATH", "claude is not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudePluginTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "plugin", verb, plugin)
	cmd.Env = pluginEnv(in, paths.Dir)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	switch {
	case ctx.Err() != nil:
		return appErr(codeFailed, "", "claude plugin %s %s did not finish within %s", verb, plugin, claudePluginTimeout)
	case err != nil:
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = nonEmpty(lastLine(stdout.String()), fmt.Sprintf("claude plugin %s %s failed: %v", verb, plugin, err))
		}
		return appErr(codeFailed, "", "%s", msg)
	}
	if asJSON {
		return emitTo(w, map[string]any{"plugin": plugin, "profile": in.Name, "enabled": verb == "enable"})
	}
	fmt.Fprintf(w, "  %s is %s in profile %s\n", plugin, onOff(verb == "enable"), in.Name)
	return nil
}

// pluginEnv is the environment `claude plugin` runs in for profile in, whose
// dir is dir: this one, with CLAUDE_CONFIG_DIR that dir, absolute, or unset
// for ~/.claude. Absolute, not as configured as `run` sets it: Claude Code
// does not expand a ~, and this run touches files, never the credential
// whose keychain name hashes the string. A declared profile also drops
// CLAUDE_SECURESTORAGE_CONFIG_DIR, as `run` does.
func pluginEnv(in config.Profile, dir string) []string {
	env := withoutEnv(os.Environ(), ccdir.EnvConfigDir)
	if !in.FromEnv {
		env = withoutEnv(env, ccdir.EnvSecureStorageDir)
	}
	if !isBaseDir(dir) {
		env = append(env, ccdir.EnvConfigDir+"="+dir)
	}
	return env
}

func withoutEnv(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// ansiEscape is a terminal colour or cursor code, which a message for the
// app must not carry.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// lastLine is s's last line that says anything.
func lastLine(s string) string {
	lines := strings.Split(ansiEscape.ReplaceAllString(s, ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// cmdProfileSetup parses `profile recent|share|own`.
func cmdProfileSetup(verb string, args []string) error {
	usage := map[string]string{"recent": profileRecentUsage, "share": profileShareUsage, "own": profileOwnUsage}[verb]
	fs := appFlags("profile " + verb)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	p, err := parseApp(fs, args, usage)
	if err != nil {
		return err
	}
	w := io.Writer(os.Stdout)
	switch verb {
	case "recent":
		if len(p) > 1 {
			return appErr(codeUsage, usage, "recent takes at most one profile name")
		}
		name := ""
		if len(p) == 1 {
			name = p[0]
		}
		return profileRecent(w, *cfgPath, name, *asJSON)
	case "share":
		if len(p) != 2 {
			return appErr(codeUsage, usage, "share takes a profile and an item")
		}
		return profileShare(w, *cfgPath, p[0], p[1], *asJSON)
	}
	if len(p) != 2 {
		return appErr(codeUsage, usage, "own takes a profile and an item")
	}
	return profileOwn(w, *cfgPath, p[0], p[1], *asJSON)
}

// cmdClaude is `cs claude show|write|plugin`.
func cmdClaude(args []string) error {
	if len(args) == 0 {
		return appErr(codeUsage, claudeUsage, "name a claude command: show, write or plugin")
	}
	fs := appFlags("claude " + args[0])
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if isHelp(args[0]) {
		return &helpRequest{text: helpFor(claudeUsage, fs)}
	}
	w := io.Writer(os.Stdout)
	switch args[0] {
	case "show":
		profile := fs.String("profile", "", "what this profile reads, instead of ~/.claude and every profile")
		p, err := parseApp(fs, args[1:], claudeShowUsage)
		if err != nil {
			return err
		}
		if len(p) != 0 {
			return appErr(codeUsage, claudeShowUsage, "show takes no arguments; got %q", p[0])
		}
		return claudeShow(w, *cfgPath, *profile, *asJSON)
	case "write":
		profile := fs.String("profile", "", "the profile whose file to replace (default: default)")
		ifUnchanged := fs.String("if-unchanged", "", "the hex SHA-256 of the content opened; refused if the file has changed since")
		p, err := parseApp(fs, args[1:], claudeWriteUsage)
		if err != nil {
			return err
		}
		if len(p) != 1 {
			return appErr(codeUsage, claudeWriteUsage, "write takes one file: CLAUDE.md or settings.json")
		}
		return claudeWrite(w, *cfgPath, p[0], *profile, *ifUnchanged, os.Stdin, *asJSON)
	case "plugin":
		profile := fs.String("profile", "", "the profile to turn it on or off in (default: default)")
		p, err := parseApp(fs, args[1:], claudePluginUsage)
		if err != nil {
			return err
		}
		if len(p) != 2 || (p[0] != "enable" && p[0] != "disable") {
			return appErr(codeUsage, claudePluginUsage, "plugin takes enable or disable, and a plugin")
		}
		return claudePlugin(w, *cfgPath, p[0], p[1], *profile, *asJSON)
	}
	return appErr(codeUsage, claudeUsage, "unknown claude command %q", args[0])
}
