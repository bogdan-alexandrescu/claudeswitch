package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// probedBrowser is one browser list_connected_browsers reported.
type probedBrowser struct {
	DeviceID    string
	Name        string
	ConnectedAt time.Time
	InUse       bool
}

// chromeCheckSettle is how long `chrome check` gives a Chrome profile it
// opened before asking again: a launched profile's extension connected
// within 3 s (GROUND_TRUTH §56).
var chromeCheckSettle = 5 * time.Second

// chromeProbe asks a fresh Claude Code process, in the Claude Code profile
// whose config dir is configDir ("" for ~/.claude), which browsers the live
// account's Claude in Chrome channel has. A fresh process is the point: one
// that is already running may keep a connection made before a switch
// (GROUND_TRUTH §56). It costs one short headless Claude Code run.
var chromeProbe = func(configDir string) ([]probedBrowser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "claude", "-p", "--chrome", "--no-session-persistence",
		"--allowedTools", "mcp__claude-in-chrome__list_connected_browsers")
	c.Stdin = strings.NewReader("Call mcp__claude-in-chrome__list_connected_browsers once and print its raw " +
		"result verbatim, with nothing else.")
	env := os.Environ()[:0:0]
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, ccdir.EnvConfigDir+"=") {
			env = append(env, kv)
		}
	}
	if configDir != "" {
		dir, err := ccdir.ExpandHome(configDir)
		if err != nil {
			return nil, err
		}
		env = append(env, ccdir.EnvConfigDir+"="+dir)
	}
	c.Env = env
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("claude -p --chrome: %v: %s", err, strings.TrimSpace(out.String()))
	}
	return parseBrowserList(out.String()), nil
}

// parseBrowserList reads the first JSON array of browsers in a model's answer.
func parseBrowserList(text string) []probedBrowser {
	for i := strings.IndexByte(text, '['); i >= 0; {
		var raw []struct {
			DeviceID    string  `json:"deviceId"`
			Name        string  `json:"name"`
			ConnectedAt float64 `json:"connectedAt"`
			InUse       bool    `json:"inUse"`
		}
		if json.NewDecoder(strings.NewReader(text[i:])).Decode(&raw) == nil {
			var out []probedBrowser
			for _, b := range raw {
				if b.DeviceID == "" {
					continue
				}
				out = append(out, probedBrowser{DeviceID: b.DeviceID, Name: b.Name, InUse: b.InUse,
					ConnectedAt: time.UnixMilli(int64(b.ConnectedAt))})
			}
			return out
		}
		j := strings.IndexByte(text[i+1:], '[')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return nil
}

// chromeCheck is `cs chrome check` (owner decision 2026-10-23): which
// browsers a new Claude in Chrome connection finds for the account live in a
// profile, asked of a fresh process. When none answers and the account has a
// Chrome profile of its own, that profile is opened (C4) and asked again.
func chromeCheck(stdout, human io.Writer, cfg *config.Config, st *state.State, profName string, asJSON bool) error {
	prof, err := pickProfile(cfg, profName)
	if err != nil {
		return chromeFail("not_found", "", "%v", err)
	}
	account := ""
	if ps := st.Profiles[prof.Name]; ps != nil {
		account = ps.Active
	}
	if account == "" || account == state.Unattributed {
		return chromeFail("not_found", "", "no account is recorded live in profile %s", prof.Name)
	}
	var chromeProfile map[string]any
	label := ""
	cp := st.ChromeOf(account)
	if cp != nil {
		label = readChromeLocal().nameOf(cp.Dir)
		chromeProfile = map[string]any{"dir": cp.Dir, "name": orNull(label)}
		if label == "" {
			label = cp.Dir
		}
	}
	browsers, err := chromeProbe(prof.Dir)
	if err != nil {
		return chromeFail("failed", "", "%v", err)
	}
	opened := false
	if len(browsers) == 0 && cp != nil {
		if launchChromeBackground(cp.Dir) == nil {
			opened = true
			time.Sleep(chromeCheckSettle)
			if browsers, err = chromeProbe(prof.Dir); err != nil {
				return chromeFail("failed", "", "%v", err)
			}
		}
	}
	if asJSON {
		list := []map[string]any{}
		for _, b := range browsers {
			list = append(list, map[string]any{"device_id": b.DeviceID, "name": b.Name,
				"connected_at": b.ConnectedAt, "in_use": b.InUse})
		}
		same := []string{}
		if len(browsers) == 0 {
			same = append(same, sameEmailAccounts(st, account)...)
		}
		return emitJSONTo(stdout, map[string]any{"profile": prof.Name, "account": account,
			"chrome_profile": chromeProfile, "browsers": list, "reachable": len(browsers) > 0, "opened": opened,
			"same_email": same})
	}
	fmt.Fprintf(human, "\n  %s: %s\n", prof.Name, account)
	for _, b := range browsers {
		use := ""
		if b.InUse {
			use = ", in use"
		}
		fmt.Fprintf(human, "  browser %s, connected %s%s\n", shortDevice(b.DeviceID), b.ConnectedAt.Local().Format("15:04"), use)
	}
	if cp != nil {
		fmt.Fprintf(human, "  its own Chrome profile: %q (%s)%s\n", label, cp.Dir, map[bool]string{true: ", opened just now", false: ""}[opened])
	} else {
		fmt.Fprintf(human, "  %s has no Chrome profile of its own (cs chrome add %s)\n", account, account)
	}
	switch {
	case len(browsers) == 1:
		fmt.Fprintf(human, "  ✓ a new Claude in Chrome connection reaches it\n")
	case len(browsers) > 1:
		fmt.Fprintf(human, "  ✓ a new Claude in Chrome connection reaches them; with several, Claude asks which to use\n")
	default:
		fmt.Fprintf(human, "  ✗ no browser answers on %s's channel: its Chrome profile's extension is not signed in as %s,"+
			" or the profile is not running\n", account, account)
		if line := sameEmailCause(st, account); line != "" {
			fmt.Fprintf(human, "  %s\n", line)
		}
	}
	fmt.Fprintf(human, "  a session that started before a switch reaches it after /mcp → claude-in-chrome → Reconnect\n\n")
	return nil
}

// shortDevice is a device id's first block, as people read it.
func shortDevice(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}

// sameEmailCause is the likely reason no browser answers for an account
// whose email has a seat in another organization too (owner decision
// 2026-10-25): the extension keeps the organization claude.ai was on when it
// was opened, so it can be on the other seat's channel; switching claude.ai
// to the account's organization and opening the extension again re-ties it
// (GROUND_TRUTH §56). "" when no other account has its email.
func sameEmailCause(st *state.State, account string) string {
	others := sameEmailAccounts(st, account)
	if len(others) == 0 {
		return ""
	}
	return fmt.Sprintf("%s is also %s, in another organization: the extension is likely tied to that one, "+
		"since it keeps the organization claude.ai was on when it was opened. In %s's Chrome profile, switch "+
		"claude.ai to %s's organization, then open Claude in Chrome again (or give %s a fresh Chrome profile: "+
		"cs chrome add %s).", st.EmailOf(account), strings.Join(others, ", "), account, account, account, account)
}
