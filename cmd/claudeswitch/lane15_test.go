package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Lane 15: `cs account list --json`, a keychain-free listing the app polls,
// and `cs profile remove`.

const listTOML = `priority = ["w1", "a1"]

[[account]]
id = "a1"
account_uuid = "u1"
org_id = "o1"

[[account]]
id = "a2"
enabled = false

[[account]]
id = "a3"

[[account]]
id = "w1"

[[profile]]
name = "default"
pool = ["a1"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

// keychainTripwires makes every keychain reach of the cmd package fail the
// test, on top of the shim's own record of any `security` run.
func keychainTripwires(t *testing.T) {
	t.Helper()
	oldLive, oldRef := cliLiveFor, envItemRef
	cliLiveFor = func(config.Profile) (keychain.Live, error) {
		t.Error("the keychain was asked for a profile's live credential")
		return nil, errors.New("no")
	}
	envItemRef = func() (string, string, bool) {
		t.Error("the keychain was asked for the environment's item")
		return "", "", false
	}
	acctSeams.has = func(string) bool { t.Error("the vault was asked whether it has an account"); return false }
	acctSeams.identity = func(string) (string, string) { t.Error("the vault was asked for a seat"); return "", "" }
	acctSeams.describe = func(string) string { t.Error("the vault was asked for an email"); return "" }
	acctSeams.plan = func(string) string { t.Error("the vault was asked for a plan"); return "" }
	t.Cleanup(func() { cliLiveFor, envItemRef = oldLive, oldRef })
}

func pct(v float64) *float64 { return &v }

func TestAccountListIsKeychainFree(t *testing.T) {
	path := appWorld(t, listTOML)
	keychainTripwires(t)
	now := time.Now()
	st, _ := state.Load("", "default", "work")
	st.SetEmail("a1", "a1@example.com")
	st.SetPlan("a1", "Max 20x")
	st.Profile("work").SetActive("w1")
	st.Profile("work").Pinned = "w1"
	w1 := st.Get("w1")
	w1.Last = &usage.Usage{FiveHour: usage.Window{Utilization: pct(30)}, SevenDay: usage.Window{Utilization: pct(50)}}
	w1.LastAt = now.Add(-time.Minute)
	w1.RefreshExpiry = now.Add(48 * time.Hour)
	w1.TokenExpiry = now.Add(time.Hour)
	a1 := st.Get("a1")
	a1.Last = &usage.Usage{FiveHour: usage.Window{Utilization: pct(100)}}
	a1.LastAt = now.Add(-time.Minute)
	a1.BurntTil, a1.BurntWin = now.Add(30*time.Minute), "five_hour"
	st.Get("a2").LastErr = "usage API: 401 Unauthorized"
	st.Get("a3").RefreshExpiry = now.Add(-time.Hour)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	before := testshim.Invocations()
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	if after := testshim.Invocations(); after != before {
		t.Fatalf("account list ran a forbidden binary:\n%s", strings.TrimPrefix(after, before))
	}
	m := decodeJSON(t, buf.Bytes())
	raw, ok := m["accounts"].([]any)
	if !ok {
		t.Fatalf("no accounts list: %s", buf.String())
	}
	byID := map[string]map[string]any{}
	var order []string
	for _, r := range raw {
		a := r.(map[string]any)
		id := a["id"].(string)
		byID[id] = a
		order = append(order, id)
		for _, k := range []string{"id", "email", "plan", "seat", "enabled", "profile", "active_in",
			"pinned", "refresh_expires_at", "access_expires_at", "state", "reading"} {
			if _, ok := a[k]; !ok {
				t.Errorf("%s lacks %q (absent values are null, never missing)", id, k)
			}
		}
	}
	if want := []string{"w1", "a1", "a3", "a2"}; !slices.Equal(order, want) {
		t.Errorf("order %v, want rotation order then the disabled: %v", order, want)
	}

	w := byID["w1"]
	if w["profile"] != "work" || w["active_in"] != "work" || w["pinned"] != true ||
		w["state"] != "available" || w["plan"] != nil || w["email"] != nil || w["seat"] != nil {
		t.Errorf("w1: %v", w)
	}
	if w["refresh_expires_at"] == nil || w["access_expires_at"] == nil {
		t.Errorf("w1's expiries were recorded: %v", w)
	}
	rd, _ := w["reading"].(map[string]any)
	if rd == nil || rd["five_hour"] != 30.0 || rd["seven_day"] != 50.0 || rd["at"] == nil || rd["error"] != nil {
		t.Errorf("w1's reading: %v", w["reading"])
	}

	a := byID["a1"]
	if a["email"] != "a1@example.com" || a["plan"] != "Max 20x" || a["seat"] != "u1@o1" ||
		a["profile"] != "default" || a["active_in"] != nil || a["pinned"] != false || a["state"] != "refused" ||
		a["enabled"] != true {
		t.Errorf("a1: %v", a)
	}
	if rd, _ := a["reading"].(map[string]any); rd == nil || rd["refused_until"] == nil {
		t.Errorf("a1's reading names when the refusal ends: %v", a["reading"])
	}
	if b := byID["a2"]; b["state"] != "needs_login" || b["enabled"] != false || b["scope"] != nil {
		t.Errorf("a2 (401, disabled): %v", b)
	}
	if c := byID["a3"]; c["state"] != "needs_login" || c["reading"] != nil {
		t.Errorf("a3 (refresh token expired, never read): %v", c)
	}

	// The human form reads nothing more.
	buf.Reset()
	if err := accountList(&buf, path, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Max 20x") || !strings.Contains(buf.String(), "needs login") {
		t.Errorf("human form:\n%s", buf.String())
	}
	if after := testshim.Invocations(); after != before {
		t.Fatal("the human form ran a forbidden binary")
	}
}

func TestAccountListThroughTheDispatcher(t *testing.T) {
	path := appWorld(t, listTOML)
	keychainTripwires(t)
	err := cmdAccount([]string{"list", "extra", "--config", path, "--json"})
	if got := errCode(t, err); got != codeUsage {
		t.Errorf("list takes no arguments: %q", got)
	}
}

// The profile's own records, as a daemon that had resolved its item left them.
func seedWorkLive(t *testing.T) {
	t.Helper()
	st, _ := state.Load("", "default", "work")
	ps := st.Profile("work")
	ps.SetActive("w1")
	ps.Item = &state.ItemRef{Service: "Claude Code-credentials-1234abcd", Dir: "~/.claude-work"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRemoveMovesItsPoolAndGuardsItsCredential(t *testing.T) {
	path := appWorld(t, profilesTOML)
	keychainTripwires(t)
	seedWorkLive(t)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := profileRemove(&buf, path, "work", "", true, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["profile"] != "work" || m["to"] != "default" || m["daemon_running"] != false {
		t.Errorf("answered %v", m)
	}
	if mv, _ := m["moved"].([]any); len(mv) != 1 || mv[0] != "w1" {
		t.Errorf("moved %v", m["moved"])
	}
	g, _ := m["ghost"].(map[string]any)
	if g == nil || g["account"] != "w1" || g["profile"] != "work" || g["why"] != state.GhostRemoved {
		t.Errorf("ghost %v", m["ghost"])
	}
	kept, _ := m["kept"].(map[string]any)
	if kept == nil || kept["dir"] != "~/.claude-work" || kept["credential"] != "Claude Code-credentials-1234abcd" {
		t.Errorf("kept %v: the answer says the folder and the credential stay", m["kept"])
	}
	if pools, _ := m["pools"].(map[string]any); pools == nil || pools["work"] != nil {
		t.Errorf("pools %v", m["pools"])
	}

	cfg := loadOrFail(t, path)
	if declaredIndex(cfg, "work") >= 0 {
		t.Fatal("the [[profile]] block stayed")
	}
	if owner, _ := cfg.ProfileOf("w1"); owner != "default" {
		t.Errorf("w1 is in %q", owner)
	}
	if !strings.Contains(readFile(t, path), `name = "default"`) {
		t.Error("the other profile went too")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the profile's folder was touched: %v", err)
	}

	// With no daemon the CLI recorded the ghost itself (D22/D25), and the
	// record no longer implies a second one.
	st, _ := state.Load("")
	gs := st.GhostList()
	if len(gs) != 1 || gs[0].Account != "w1" || gs[0].Service != "Claude Code-credentials-1234abcd" {
		t.Fatalf("recorded ghosts %+v", gs)
	}
	if ps := st.Profiles["work"]; ps != nil && ps.Active != "" {
		t.Errorf("the removed profile still records %q live", ps.Active)
	}
	if got := allGhosts(cfg, st); len(got) != 1 {
		t.Errorf("CLI checks see %d ghosts, want the one", len(got))
	}
}

func TestProfileRemoveWithNothingLiveMakesNoGhost(t *testing.T) {
	path := appWorld(t, profilesTOML)
	keychainTripwires(t)
	var buf bytes.Buffer
	if err := profileRemove(&buf, path, "work", "", true, true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["ghost"] != nil {
		t.Errorf("ghost %v with nothing live", m["ghost"])
	}
	if st, _ := state.Load(""); len(st.GhostList()) != 0 {
		t.Error("a ghost was recorded with nothing live")
	}
}

func TestProfileRemoveWithADaemonLeavesTheGhostToItsReload(t *testing.T) {
	path := appWorld(t, profilesTOML)
	keychainTripwires(t)
	seedWorkLive(t)
	oldLock, oldWait := tryDaemonLock, waitDaemonConfig
	t.Cleanup(func() { tryDaemonLock, waitDaemonConfig = oldLock, oldWait })
	tryDaemonLock = func() (*state.Lock, error) { return nil, state.ErrDaemonRunning }
	waited := ""
	waitDaemonConfig = func(hash string) bool { waited = hash; return true }

	var buf bytes.Buffer
	if err := profileRemove(&buf, path, "work", "", true, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["daemon_running"] != true || m["daemon_loaded"] != true {
		t.Errorf("answered %v", m)
	}
	raw, _ := os.ReadFile(path)
	if waited != config.ContentHash(raw) {
		t.Error("it did not wait for the daemon to run the edited config")
	}
	// The running daemon's reload makes the ghost (makeGhost); until it has,
	// the CLI's own checks derive it from the state, and the answer says so.
	if g, _ := m["ghost"].(map[string]any); g == nil || g["account"] != "w1" {
		t.Errorf("ghost %v", m["ghost"])
	}
	if st, _ := state.Load(""); len(st.Ghosts) != 0 {
		t.Error("the CLI recorded a ghost under a running daemon, which owns the profile's record")
	}

	// A daemon that does not load it in time: the edit stays, said so.
	path = appWorld(t, profilesTOML)
	keychainTripwires(t)
	waitDaemonConfig = func(string) bool { return false }
	buf.Reset()
	if err := profileRemove(&buf, path, "work", "", true, true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["daemon_loaded"] != false {
		t.Errorf("answered %v", m)
	}
	if declaredIndex(loadOrFail(t, path), "work") >= 0 {
		t.Error("the edit was undone")
	}
}

func TestProfileRemoveToAnotherProfile(t *testing.T) {
	path := appWorld(t, profilesTOML+"\n[[profile]]\nname = \"lab\"\ndir = \"~/.claude-lab\"\npool = [\"a2\"]\n")
	keychainTripwires(t)
	var buf bytes.Buffer
	if err := profileRemove(&buf, path, "lab", "work", true, true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if owner, _ := cfg.ProfileOf("a2"); owner != "work" {
		t.Errorf("a2 is in %q, want work", owner)
	}
	if m := decodeJSON(t, buf.Bytes()); m["to"] != "work" {
		t.Errorf("answered %v", m)
	}
}

func TestProfileRemoveDefaultNeedsSomewhereForItsAccounts(t *testing.T) {
	path := appWorld(t, profilesTOML)
	keychainTripwires(t)
	before := readFile(t, path)
	var buf bytes.Buffer
	err := profileRemove(&buf, path, "default", "", true, true)
	if got := errCode(t, err); got != codeUsage || !strings.Contains(err.Error(), "--to") {
		t.Errorf("default without --to: %q, %v", got, err)
	}
	if readFile(t, path) != before {
		t.Fatal("a refusal changed the config")
	}
	// With --to, everything default held moves, including the accounts it
	// held only by D6 (a2 is listed nowhere).
	if err := profileRemove(&buf, path, "default", "work", true, true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	for _, id := range []string{"a1", "a2", "w1"} {
		if owner, _ := cfg.ProfileOf(id); owner != "work" {
			t.Errorf("%s is in %q", id, owner)
		}
	}
}

func TestProfileRemoveRefusals(t *testing.T) {
	var buf bytes.Buffer

	// No default to fall back to: would_orphan, unless --to.
	path := appWorld(t, strings.Replace(profilesTOML, `name = "default"`+"\npool = [\"a1\"]",
		`name = "home"`+"\npool = [\"a1\", \"a2\"]", 1))
	keychainTripwires(t)
	before := readFile(t, path)
	if got := errCode(t, profileRemove(&buf, path, "work", "", true, true)); got != codeWouldOrphan {
		t.Errorf("no default: %q", got)
	}
	if got := errCode(t, profileRemove(&buf, path, "nope", "", true, true)); got != codeNotFound {
		t.Errorf("unknown profile: %q", got)
	}
	if got := errCode(t, profileRemove(&buf, path, "work", "nope", true, true)); got != codeNotFound {
		t.Errorf("unknown --to: %q", got)
	}
	if got := errCode(t, profileRemove(&buf, path, "work", "work", true, true)); got != codeUsage {
		t.Errorf("--to itself: %q", got)
	}
	promptsOff = true
	t.Cleanup(func() { promptsOff = false })
	err := profileRemove(&buf, path, "work", "home", false, true)
	if got := errCode(t, err); got != codeConfirm || !strings.Contains(err.Error(), "guard") {
		t.Errorf("no --yes: %q, %v (the message names the ghost behaviour)", got, err)
	}
	if readFile(t, path) != before {
		t.Fatal("a refusal changed the config")
	}

	// The last profile.
	path = appWorld(t, "[[account]]\nid = \"a1\"\n\n[[profile]]\nname = \"default\"\npool = [\"a1\"]\n")
	keychainTripwires(t)
	if got := errCode(t, profileRemove(&buf, path, "default", "", true, true)); got != codeLastProfile {
		t.Errorf("last profile: %q", got)
	}
	// No [[profile]] blocks: the implicit profile is not removable.
	path = appWorld(t, "[[account]]\nid = \"a1\"\n")
	keychainTripwires(t)
	if got := errCode(t, profileRemove(&buf, path, "default", "", true, true)); got != codeLastProfile {
		t.Errorf("implicit profile: %q", got)
	}
}

func TestProfileRemoveThroughTheDispatcher(t *testing.T) {
	path := appWorld(t, profilesTOML)
	keychainTripwires(t)
	if got := errCode(t, cmdProfile([]string{"remove", "--config", path, "--json"})); got != codeUsage {
		t.Errorf("no name: %q", got)
	}
}
