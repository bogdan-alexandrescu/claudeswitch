package main

// Integration with Claude Code itself: the context a SessionStart hook hands
// the model, and installing the status line into Claude Code's settings.
//
// The plugin under plugin/ is the other half. It calls into these commands
// rather than reading state itself, so there is one implementation of what a
// reading means and the plugin carries no logic that can drift from it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// cmdContext prints a few lines describing quota for Claude Code to read at
// the start of a session. Like the status line it is strictly read-only — no
// keychain, no API, no state writes — because it runs on every session start,
// and a hook that raised a keychain prompt each time would be worse than none.
func cmdContext(args []string) error {
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	parseInterleaved(fs, args)

	cfg, err := config.Load(*cfgPath)
	if err != nil && cfg == nil {
		fmt.Println("[claudeswitch] config unreadable; run `claudeswitch doctor` in a terminal")
		return nil // never fail a session start over a config problem
	}
	st, err := state.Load("")
	if err != nil {
		st = nil
	}
	renderContext(os.Stdout, cfg, st, time.Now(), state.DaemonRunning(), currentDir())
	return nil
}

// renderContext is cmdContext without the I/O, so the wording can be tested.
//
// Every line is prefixed so that, among other hooks' output, it is obvious
// where it came from. The normal case is two lines; anything more means
// something needs attention.
func renderContext(w io.Writer, cfg *config.Config, st *state.State, now time.Time, daemon bool, dir string) {
	const p = "[claudeswitch] "
	if len(cfg.Accounts) == 0 {
		fmt.Fprintln(w, p+"no accounts set up yet; run `claudeswitch setup` in a terminal")
		return
	}
	if st == nil || st.Active == "" {
		fmt.Fprintln(w, p+"no account selected yet; `claudeswitch status` shows what is available")
		return
	}
	a, ok := st.Accounts[st.Active]
	if !ok || a.Last == nil {
		fmt.Fprintf(w, p+"active account %s, no usage reading yet\n", st.Active)
		return
	}

	line := fmt.Sprintf("active %s · session %.0f%% (resets %s) · week %.0f%% (resets %s)",
		st.Active,
		a.Last.FiveHour.Pct(), slUntil(a.Last.FiveHour.ResetsAt, now),
		a.Last.SevenDay.Pct(), slUntil(a.Last.SevenDay.ResetsAt, now))
	if age := now.Sub(a.LastAt); !a.LastAt.IsZero() && age > staleDecisionAfter(cfg) {
		line += fmt.Sprintf(" · reading %s old", age.Round(time.Minute))
	}
	fmt.Fprintln(w, p+line)

	if !a.BurntTil.IsZero() && now.Before(a.BurntTil) {
		fmt.Fprintf(w, p+"%s was refused until %s\n", st.Active, a.BurntTil.Local().Format("15:04"))
	}

	daemonNote := "daemon running, rotates automatically"
	switch {
	case !daemon:
		daemonNote = "daemon NOT running, so nothing rotates automatically"
	case !st.DaemonLive:
		daemonNote = "daemon in dry-run: it reports swaps but does not make them"
	}
	fmt.Fprintf(w, p+"rotates at session %.0f%% / week %.0f%%, mid-turn at %.0f%% · %s\n",
		cfg.TriggerFor(usage.FiveHourKey), cfg.TriggerFor(usage.SevenDayKey), cfg.HardFloor, daemonNote)

	dec, _ := policy.Explain(policy.Input{
		Cfg: cfg, St: st, Now: now, LastSwitch: st.LastSwitch,
		Pinned: st.Pinned, Dir: dir, Lookahead: lookahead(cfg),
	})
	switch dec.Kind {
	case policy.Switch:
		fmt.Fprintf(w, p+"next: %s. `claudeswitch use %s` swaps now, no restart\n", dec, dec.Target)
	case policy.Wait:
		fmt.Fprintf(w, p+"next: %s\n", dec)
	}
}

// settingsPath is Claude Code's user settings file, honouring
// CLAUDE_CONFIG_DIR the way Claude Code does.
func settingsPath() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

const statuslineCommand = "claudeswitch statusline"

// ourStatusline matches a status line command that runs this program, through
// either name and with or without a path in front.
var ourStatusline = regexp.MustCompile(`(^|/)(claudeswitch|cs)\s+statusline\b`)

// cmdStatuslineManage handles `statusline install` and `statusline uninstall`.
func cmdStatuslineManage(verb string, args []string) error {
	fs := flag.NewFlagSet("statusline "+verb, flag.ExitOnError)
	force := fs.Bool("force", false, "replace a status line that is not claudeswitch's")
	parseInterleaved(fs, args)

	path, err := settingsPath()
	if err != nil {
		return err
	}
	var msg string
	switch verb {
	case "install":
		msg, err = installStatusline(path, *force)
	default:
		msg, err = uninstallStatusline(path)
	}
	if err != nil {
		return err
	}
	fmt.Printf("  %s\n", msg)
	return nil
}

// installStatusline sets statusLine in the settings file at path, unless one is
// already there. Someone else's status line is left alone without --force: it
// was a choice, and silently replacing it would be a bad way to find out.
func installStatusline(path string, force bool) (string, error) {
	s, err := readSettings(path)
	if err != nil {
		return "", err
	}
	if raw, ok := s.get("statusLine"); ok {
		cmd := statuslineCommandOf(raw)
		if ourStatusline.MatchString(cmd) {
			return "status line already runs claudeswitch; nothing to do", nil
		}
		if !force {
			return "", fmt.Errorf("%s already has a status line (%q); leaving it. "+
				"`claudeswitch statusline install --force` replaces it", path, cmd)
		}
	}
	val, _ := json.Marshal(map[string]string{"type": "command", "command": statuslineCommand})
	s.set("statusLine", val)
	if err := s.write(path); err != nil {
		return "", err
	}
	return fmt.Sprintf("✓ status line set in %s (takes effect on the next render)", path), nil
}

// uninstallStatusline removes statusLine only when it is ours.
func uninstallStatusline(path string) (string, error) {
	s, err := readSettings(path)
	if err != nil {
		return "", err
	}
	raw, ok := s.get("statusLine")
	if !ok {
		return "no status line configured; nothing to do", nil
	}
	if cmd := statuslineCommandOf(raw); !ourStatusline.MatchString(cmd) {
		return fmt.Sprintf("status line is not claudeswitch's (%q); leaving it", cmd), nil
	}
	s.del("statusLine")
	if err := s.write(path); err != nil {
		return "", err
	}
	return fmt.Sprintf("✓ status line removed from %s", path), nil
}

// statuslineInstalled reports whether the settings file runs our status line.
func statuslineInstalled() (bool, string) {
	path, err := settingsPath()
	if err != nil {
		return false, ""
	}
	s, err := readSettings(path)
	if err != nil {
		return false, path
	}
	raw, ok := s.get("statusLine")
	return ok && ourStatusline.MatchString(statuslineCommandOf(raw)), path
}

func statuslineCommandOf(raw json.RawMessage) string {
	var v struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Command
}

// settingsFile is a JSON object with its keys kept in their original order.
// Claude Code's settings are edited by hand, and rewriting someone's file in
// alphabetical order to change one key is the kind of diff people notice.
type settingsFile struct {
	keys   []string
	vals   map[string]json.RawMessage
	orig   []byte
	exists bool
}

func readSettings(path string) (*settingsFile, error) {
	s := &settingsFile{vals: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.orig, s.exists = b, true
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("%s is not a JSON object; leaving it untouched", path)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%s is not valid JSON (%v); leaving it untouched", path, err)
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON (%v); leaving it untouched", path, err)
		}
		if _, dup := s.vals[k]; !dup {
			s.keys = append(s.keys, k)
		}
		s.vals[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); leaving it untouched", path, err)
	}
	return s, nil
}

func (s *settingsFile) get(k string) (json.RawMessage, bool) {
	v, ok := s.vals[k]
	return v, ok
}

func (s *settingsFile) set(k string, v json.RawMessage) {
	if _, ok := s.vals[k]; !ok {
		s.keys = append(s.keys, k)
	}
	s.vals[k] = v
}

func (s *settingsFile) del(k string) {
	delete(s.vals, k)
	for i, key := range s.keys {
		if key == k {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			break
		}
	}
}

// write saves the file atomically, keeping a copy of what was there before.
func (s *settingsFile) write(path string) error {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range s.keys {
		if i > 0 {
			buf.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		buf.WriteString("\n  ")
		buf.Write(kb)
		buf.WriteString(": ")
		if err := json.Indent(&buf, s.vals[k], "  ", "  "); err != nil {
			return err
		}
	}
	if len(s.keys) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if s.exists {
		if err := os.WriteFile(path+".claudeswitch.bak", s.orig, mode); err != nil {
			return fmt.Errorf("could not back up %s, so did not change it: %w", path, err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// pluginInstalled reports whether Claude Code has the claudeswitch plugin
// installed, from its own record of installed plugins.
func pluginInstalled() bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var v struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	for k := range v.Plugins {
		if strings.HasPrefix(k, "claudeswitch@") {
			return true
		}
	}
	return false
}
