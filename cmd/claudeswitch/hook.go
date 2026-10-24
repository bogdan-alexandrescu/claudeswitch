package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// hookNow and hookSleep are the hooks' clock, replaced in tests.
var (
	hookNow   = time.Now
	hookSleep = time.Sleep
)

// hookCall is the part of a Claude Code hook's input the hooks read.
type hookCall struct {
	Event    string          `json:"hook_event_name"`
	Session  string          `json:"session_id"`
	Tool     string          `json:"tool_name"`
	Input    json.RawMessage `json:"tool_input"`
	Response json.RawMessage `json:"tool_response"`
}

// artifactInput is the part of an Artifact tool call's input that names the
// artifact it acts on.
type artifactInput struct {
	Action   string `json:"action"`
	URL      string `json:"url"`
	FilePath string `json:"file_path"`
	Asset    bool   `json:"asset"`
}

// artifactTools are the tools that act on one artifact, named by its URL.
var artifactTools = map[string]bool{"Artifact": true, "ArtifactComments": true, "ArtifactData": true}

func cmdHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	rest := parseInterleaved(fs, args)
	os.Exit(runHook(rest, os.Stdin, os.Stdout, os.Stderr, *cfgPath))
}

// runHook is `claudeswitch hook pre|post`, the plugin's hooks around
// claude.ai tool calls (IMPROVEMENTS phase 1 S and G, owner decisions
// 2026-10-21). It never fails a tool call by failing itself: every error,
// and every call it has nothing to say about, exits 0 in silence, and the
// call goes on through Claude Code's normal flow. Its only answer is a
// refusal of a call on another account's artifact (pre), as hook JSON on
// stdout.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer, cfgPath string) int {
	_ = stderr
	if len(args) == 0 {
		return 0
	}
	var in hookCall
	b, err := io.ReadAll(io.LimitReader(stdin, 16<<20))
	if err != nil || json.Unmarshal(b, &in) != nil {
		return 0
	}
	switch args[0] {
	case "pre":
		hookPre(in, stdout, cfgPath)
	case "post":
		hookPost(in, cfgPath)
	}
	return 0
}

// hookPre runs before a Claude in Chrome, Artifact or Docs call. First it
// settles (S): Claude Code's claude.ai tools act as the account before a
// switch for up to about half a minute after it (GROUND_TRUTH §55), so the
// call waits until settle_after_swap after the profile's last switch. Then,
// for an Artifact tool, it guards (G): a call on an artifact another account
// published fails as "There is no Artifact at …", so it is refused first,
// saying whose it is and what to do.
func hookPre(in hookCall, stdout io.Writer, cfgPath string) {
	if in.Event != "PreToolUse" || !detector.ClaudeAITool(in.Tool) {
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return
	}
	prof, err := pickProfile(cfg, "")
	if err != nil {
		return
	}
	st := settle(cfg, prof.Name)
	if st == nil {
		return
	}
	ps := st.Profiles[prof.Name]
	if ps == nil || ps.Active == "" || ps.Active == state.Unattributed {
		return
	}
	live := ps.Active
	var why string
	switch {
	case artifactTools[in.Tool]:
		owner, ok := artifactCallOwner(in)
		if !ok || ownedBy(cfg, owner, live) {
			return
		}
		name := ownerName(cfg, owner)
		why = fmt.Sprintf("[claudeswitch] This artifact belongs to %s: it was published while %s was live, and "+
			"claude.ai lets only the account that published an artifact read, change or delete it. This session "+
			"now uses %s (claudeswitch rotated its profile). ", name, name, live)
		why += wayBack(cfg, st, name, "publish it again as a new artifact under "+live)
	case opensPages[in.Tool]:
		why = navigationRefusal(cfg, st, in, live)
	}
	if why == "" {
		if note := browserMoved(cfg, st, prof.Name, in, live); note != "" {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{
				"hookEventName":     "PreToolUse",
				"additionalContext": note,
			}})
		}
		return
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": why,
	}}
	_ = json.NewEncoder(stdout).Encode(out)
}

// navigationRefusal is why a navigation in the live account's own browser
// to another account's artifact is refused (phase 3 C1), or "".
func navigationRefusal(cfg *config.Config, st *state.State, in hookCall, live string) string {
	dir, own := st.OwnBrowser(live)
	if !own {
		return ""
	}
	owner, ok := navigationOwner(cfg, in, live)
	if !ok {
		return ""
	}
	name := ownerName(cfg, owner)
	why := fmt.Sprintf("[claudeswitch] This artifact belongs to %s, and claude.ai shows an artifact only to its "+
		"owner. Claude in Chrome here reaches %s's browser (Chrome profile %q, signed in as %s), where it "+
		"would show as private. ", name, live, readChromeLocal().nameOf(dir), live)
	return why + wayBack(cfg, st, name, "open one of "+live+"'s own instead")
}

// browserMoved is what a session's first Claude in Chrome call after its
// profile switched accounts tells Claude (owner decision 2026-10-23, in
// place of moving tabs between Chrome profiles): the tabs it opened before
// stayed in the previous account's Chrome profile, and a new connection
// reaches the live account's, so it reopens the pages it needs there. A
// session's first call ever has nothing to compare with, and a profile
// whose accounts share one browser moved nothing: both say nothing.
func browserMoved(cfg *config.Config, st *state.State, profile string, in hookCall, live string) string {
	if !strings.HasPrefix(in.Tool, detector.BrowserToolPrefix) || in.Session == "" {
		return ""
	}
	now := hookNow()
	prev, found, err := state.NoteChromeSession(state.ChromeSession{Session: in.Session, Profile: profile,
		Account: live, At: now})
	if err != nil || !found || prev.Profile != profile || prev.Account == live {
		return ""
	}
	local := readChromeLocal()
	from := resolveChrome(cfg, st, local, profile, prev.Account)
	to := resolveChrome(cfg, st, local, profile, live)
	if from.Dir != "" && from.Dir == to.Dir {
		return ""
	}
	when := ""
	if ps := st.Profiles[profile]; ps != nil && ps.LastSwitch.After(prev.At) {
		when = " at " + ps.LastSwitch.Local().Format("15:04")
	}
	msg := fmt.Sprintf("[claudeswitch] Since this session's last Claude in Chrome call, claudeswitch switched "+
		"Claude Code from %s to %s%s. Claude in Chrome follows the live account: ", prev.Account, live, when)
	if to.Rule == "account" {
		msg += fmt.Sprintf("a new connection reaches %s's Chrome profile %q. ", live, to.label())
	} else {
		msg += fmt.Sprintf("a new connection reaches only a browser whose Claude in Chrome is signed in as %s, "+
			"and %s has no Chrome profile of its own (cs chrome add %s). ", live, live, live)
	}
	if from.Rule == "account" {
		msg += fmt.Sprintf("The tabs this session opened before are in %s's (%q), out of its reach, and their "+
			"tab ids no longer work. ", prev.Account, from.label())
	} else {
		msg += fmt.Sprintf("The tabs this session opened before are in the browser %s used, out of its reach, and "+
			"their tab ids no longer work. ", prev.Account)
	}
	return msg + "Do not ask the person to sign in or to move tabs: call tabs_context_mcp, then reopen the " +
		"pages you still need with navigate."
}

// artifactCallOwner is the recorded owner of the artifact an Artifact tool
// call acts on: by its URL, or, for a publish without one, by the file this
// session published it from. `open` acts on nothing of claude.ai's: it shows
// the page to the person, in their browser, as whoever that is.
func artifactCallOwner(in hookCall) (state.Artifact, bool) {
	var ai artifactInput
	_ = json.Unmarshal(in.Input, &ai)
	if ai.Action == "open" {
		return state.Artifact{}, false
	}
	var owner state.Artifact
	var found bool
	if id := state.ArtifactID(ai.URL); id != "" {
		owner, found, _ = state.ArtifactOwner(id)
	} else if ai.FilePath != "" && (ai.Action == "" || ai.Action == "publish") {
		owner, found, _ = state.ArtifactAt(in.Session, ai.FilePath)
	}
	return owner, found
}

// opensPages are the Claude in Chrome tools whose input can open a URL:
// navigate, and browser_batch, whose actions may include navigates.
var opensPages = map[string]bool{
	detector.BrowserToolPrefix + "navigate":      true,
	detector.BrowserToolPrefix + "browser_batch": true,
}

// navigationOwner is the owner of the first recorded artifact, not the live
// account's, that a navigation would open (phase 3 C1).
func navigationOwner(cfg *config.Config, in hookCall, live string) (state.Artifact, bool) {
	for _, u := range artifactURL.FindAllString(string(in.Input), -1) {
		if a, ok, _ := state.ArtifactOwner(state.ArtifactID(u)); ok && !ownedBy(cfg, a, live) {
			return a, true
		}
	}
	return state.Artifact{}, false
}

// ownerName is a recorded owner as it is called now: by its seat, which
// survives a rename, else by the name it had.
func ownerName(cfg *config.Config, owner state.Artifact) string {
	if id := cfg.AccountBySeat(owner.Seat); id != "" {
		return id
	}
	return owner.Account
}

// wayBack is what a refusal offers: switching to the owner when it is set up
// here and has room, else only the other way (instead).
func wayBack(cfg *config.Config, st *state.State, name, instead string) string {
	if !slices.ContainsFunc(cfg.Accounts, func(a config.Account) bool { return a.ID == name }) {
		return fmt.Sprintf("%s is no longer set up here, so %s.", name, instead)
	}
	room, how := hasRoom(cfg, st, name, hookNow())
	if room {
		return fmt.Sprintf("Either run `cs use %s` (%s has room: %s) and retry, or %s.", name, name, how, instead)
	}
	return fmt.Sprintf("%s has no room now (%s), so %s, or wait for %s to recover.", name, how, instead, name)
}

// settle waits until settle_after_swap after the profile's last switch, and
// again for a switch made while it waits, never longer than
// MaxSettleAfterSwap in all. It returns the state as it stands after, nil
// when it cannot be read.
func settle(cfg *config.Config, profile string) *state.State {
	w := cfg.Settle()
	deadline := hookNow().Add(config.MaxSettleAfterSwap)
	for {
		st, err := state.Load("", cfg.ProfileNames()...)
		if err != nil {
			return nil
		}
		ps := st.Profiles[profile]
		if w <= 0 || ps == nil || ps.LastSwitch.IsZero() {
			return st
		}
		left := min(ps.LastSwitch.Add(w).Sub(hookNow()), w, deadline.Sub(hookNow()))
		if left <= 0 {
			return st
		}
		hookSleep(left)
	}
}

// ownedBy reports whether the recorded owner is the live account: by seat
// when both are known, which survives a rename, else by name.
func ownedBy(cfg *config.Config, owner state.Artifact, live string) bool {
	if seat := cfg.SeatOf(live); seat != "" && owner.Seat != "" {
		return seat == owner.Seat
	}
	return owner.Account == live
}

// hasRoom reports whether an account could be switched to now, and its
// standing in words: under both its triggers on its last reading, and not
// refused. One never read is offered, said as unknown.
func hasRoom(cfg *config.Config, st *state.State, id string, now time.Time) (bool, string) {
	a := st.Accounts[id]
	if a == nil || a.Last == nil {
		return true, "its usage is not known yet; `cs status` reads it"
	}
	if !a.BurntTil.IsZero() && now.Before(a.BurntTil) {
		return false, "refused until " + a.BurntTil.Local().Format("15:04")
	}
	how := fmt.Sprintf("session %.0f%%, week %.0f%%", a.Last.FiveHour.Pct(), a.Last.SevenDay.Pct())
	_, _, over := a.Last.WorstAgainst(cfg.TriggerFor(usage.FiveHourKey), cfg.TriggerFor(usage.SevenDayKey))
	return over < 0, how
}

// artifactURL finds an artifact's URL in a tool result, whatever its shape:
// the publish result's url field, or the first one in its text.
var artifactURL = regexp.MustCompile(`(?:https?://)?claude\.ai/(?:code/)?artifact/[A-Za-z0-9_-]+`)

// hookPost runs after an Artifact call and records a publish's owner: the
// live account, which the settle made the one the call acted as. When a
// switch came inside the settle window the call may have gone out as the
// account before, so nothing is recorded: a guard on a wrong owner would
// refuse the owner's own calls.
func hookPost(in hookCall, cfgPath string) {
	if in.Event != "PostToolUse" || in.Tool != "Artifact" {
		return
	}
	var ai artifactInput
	_ = json.Unmarshal(in.Input, &ai)
	if (ai.Action != "" && ai.Action != "publish") || ai.Asset {
		return
	}
	var res struct {
		URL  string `json:"url"`
		ID   string `json:"artifact_id"`
		Path string `json:"path"`
	}
	_ = json.Unmarshal(in.Response, &res)
	url := res.URL
	if state.ArtifactID(url) == "" {
		url = artifactURL.FindString(string(in.Response))
	}
	id := state.ArtifactID(url)
	if id == "" {
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return
	}
	prof, err := pickProfile(cfg, "")
	if err != nil {
		return
	}
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		return
	}
	ps := st.Profiles[prof.Name]
	if ps == nil || ps.Active == "" || ps.Active == state.Unattributed {
		return
	}
	now := hookNow()
	if w := cfg.Settle(); w > 0 && now.Sub(ps.LastSwitch) < w {
		return
	}
	path := ai.FilePath
	if path == "" {
		path = res.Path
	}
	_ = state.RecordArtifact(state.Artifact{ID: id, URL: url, Path: path, Session: in.Session,
		Account: ps.Active, Seat: cfg.SeatOf(ps.Active), At: now})
}
