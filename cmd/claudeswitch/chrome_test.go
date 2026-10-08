package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// IMPROVEMENTS C1: `cs chrome`. Every launch goes to the shim's fake `open`
// and Chrome binaries, which record argv and launch nothing.

const chromeTOML = `
[[account]]
id = "personal"
[[account]]
id = "w1"
[[account]]
id = "w2"

[[profile]]
name = "default"

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1", "w2"]
`

const webStoreURL = "https://chromewebstore.google.com/detail/fcoeoabgfenejglbffodgkkbkcdhcgfn"

// chromeWorld isolates one test: its own HOME (so its own state.json), a
// config, the platform it pretends to be on, and w2's email recorded in
// state.json (owner decision: never the keychain; the shim fails the run if
// anything reaches `security`).
func chromeWorld(t *testing.T, goos string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	testshim.ResetLaunches()
	oldOS, oldNow := chromeGOOS, chromeNow
	t.Cleanup(func() { chromeGOOS, chromeNow = oldOS, oldNow })
	chromeGOOS = goos
	chromeNow = func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) }
	st := chromeState(t)
	st.SetEmail("w2", "w2@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return writeConfig(t, chromeTOML)
}

func runChromeT(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := runChrome(&out, &errOut, args)
	return out.String(), errOut.String(), err
}

func chromeState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func wantCode(t *testing.T, err error, code string) *chromeError {
	t.Helper()
	var ce *chromeError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a chromeError with code %q", err, code)
	}
	if ce.Code != code {
		t.Fatalf("code = %q (%s), want %q", ce.Code, ce.Message, code)
	}
	return ce
}

func TestChromeAddOnMacOpensANewProfileAndRecordsIt(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	out, _, err := runChromeT(t, "add", "w2", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := testshim.Launches()
	want := [][]string{{"open", "-na", "Google Chrome", "--args", "--profile-directory=claudeswitch-w2",
		"https://claude.ai/login", webStoreURL}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("launches = %q\nwant       %q", got, want)
	}
	cp := chromeState(t).ChromeOf("w2")
	if cp == nil || cp.Dir != "claudeswitch-w2" {
		t.Fatalf("mapping = %+v, want claudeswitch-w2", cp)
	}
	for _, s := range []string{"w2@example.com", "Claude in Chrome", "cs chrome w2", "sign"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

func TestChromeAddOnLinuxRunsChromeWithTheProfileDirectory(t *testing.T) {
	cfg := chromeWorld(t, "linux")
	if _, _, err := runChromeT(t, "add", "w1", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	got := testshim.Launches()
	if len(got) != 1 {
		t.Fatalf("launches = %q, want one", got)
	}
	if got[0][0] != "google-chrome" ||
		!slices.Equal(got[0][1:], []string{"--profile-directory=claudeswitch-w1", "https://claude.ai/login", webStoreURL}) {
		t.Fatalf("launch = %q", got[0])
	}
}

func TestChromeAddWithoutAnEmailStillSaysWhichAccount(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	out, _, err := runChromeT(t, "add", "w1", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sign in to claude.ai as w1\n") {
		t.Fatalf("with no recorded email the steps name the account id:\n%s", out)
	}
}

// A profile directory read back from state.json is checked again before it
// reaches Chrome's argv: the file is editable, and "--…" would be a flag.
func TestChromeRefusesAnInvalidRecordedProfileDir(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	for _, dir := range []string{"--user-data-dir=/tmp/x", "../Default", ""} {
		st := chromeState(t)
		st.SetChrome("w1", dir, time.Now())
		if err := st.Save(); err != nil {
			t.Fatal(err)
		}
		testshim.ResetLaunches()
		_, _, err := runChromeT(t, "w1", "--config", cfg)
		wantCode(t, err, "invalid_value")
		if _, _, err := runChromeT(t, "add", "w1", "--config", cfg); err == nil {
			t.Fatalf("add reopened the invalid dir %q", dir)
		}
		if l := testshim.Launches(); len(l) != 0 {
			t.Fatalf("dir %q: launched %q", dir, l)
		}
	}
}

// Owner decision (lane 13): `cs rename` moves the Chrome mapping and the
// recorded email to the new id; the Chrome profile directory is unchanged.
func TestRenameMovesTheChromeMapping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("w1")] = &keychain.Blob{
		ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-1", RefreshToken: "r-1"},
		Meta:          &keychain.Meta{AccountID: "w1", AccountUUID: "u1", OrgID: "o1"},
	}
	path := writeConfig(t, chromeTOML)
	st := chromeState(t)
	st.AddVaulted("w1")
	st.SetChrome("w1", "claudeswitch-w1", time.Now())
	st.SetEmail("w1", "w1@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if err := cmdRename([]string{"--config", path, "w1", "w9"}); err != nil {
		t.Fatal(err)
	}
	after := chromeState(t)
	if after.ChromeOf("w1") != nil || after.EmailOf("w1") != "" {
		t.Fatal("the old id still has a Chrome mapping or an email")
	}
	if cp := after.ChromeOf("w9"); cp == nil || cp.Dir != "claudeswitch-w1" {
		t.Fatalf("w9's mapping = %+v, want the same directory", cp)
	}
	if got := after.EmailOf("w9"); got != "w1@example.com" {
		t.Fatalf("w9's email = %q", got)
	}
}

func TestChromeRefusesAnUnsupportedPlatformAndRecordsNothing(t *testing.T) {
	cfg := chromeWorld(t, "windows")
	_, _, err := runChromeT(t, "add", "w1", "--config", cfg)
	wantCode(t, err, "unsupported_platform")
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q on an unsupported platform", l)
	}
	if chromeState(t).ChromeOf("w1") != nil {
		t.Fatal("recorded a mapping although nothing was opened")
	}
}

func TestChromeAddRefusesAnUnknownAccount(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	_, _, err := runChromeT(t, "add", "stranger", "--config", cfg)
	wantCode(t, err, "not_found")
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q for an unknown account", l)
	}
}

// The profile directory is derived from the account id and must itself be a
// valid name; an id too long for that is refused, not truncated.
func TestChromeAddRefusesAProfileDirThatIsNotAValidName(t *testing.T) {
	long := strings.Repeat("x", 60)
	cfg := chromeWorld(t, "darwin")
	cfg = writeConfig(t, "[[account]]\nid = \""+long+"\"\n")
	_, _, err := runChromeT(t, "add", long, "--config", cfg)
	wantCode(t, err, "invalid_value")
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q", l)
	}
}

func TestChromeOpenNeedsAMappingAndThenOpensIt(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	_, _, err := runChromeT(t, "w1", "--config", cfg)
	ce := wantCode(t, err, "not_found")
	if !strings.Contains(ce.Hint, "cs chrome add w1") {
		t.Fatalf("hint = %q, want it to name `cs chrome add w1`", ce.Hint)
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q without a mapping", l)
	}

	if _, _, err := runChromeT(t, "add", "w1", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	testshim.ResetLaunches()
	for _, args := range [][]string{{"w1"}, {"open", "w1"}} {
		testshim.ResetLaunches()
		if _, _, err := runChromeT(t, append(args, "--config", cfg)...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		want := [][]string{{"open", "-na", "Google Chrome", "--args", "--profile-directory=claudeswitch-w1"}}
		if got := testshim.Launches(); !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("%v: launches = %q, want %q", args, got, want)
		}
	}
}

// `cs chrome` alone opens the profile of the account live in the caller's
// Claude Code profile (CLAUDE_CONFIG_DIR → profile → active account).
func TestChromeWithNoAccountFollowsTheCallersProfile(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal") // an active account needs a record, or load drops it
	st.Profile("work").SetActive("w2")
	st.Profile("default").SetActive("personal")
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	st.SetChrome("personal", "claudeswitch-personal", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	if _, _, err := runChromeT(t, "--config", cfg); err != nil {
		t.Fatal(err)
	}
	got := testshim.Launches()
	if len(got) != 1 || got[0][len(got[0])-1] != "--profile-directory=claudeswitch-w2" {
		t.Fatalf("launches = %q, want work's live account w2", got)
	}
}

func TestChromeWithNoAccountAndNoLiveAccountRefuses(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	setCCDir(t, "~/.claude-work")
	_, _, err := runChromeT(t, "--config", cfg)
	wantCode(t, err, "not_found")
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q", l)
	}
}

func TestChromeListJSON(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal") // an active account needs a record, or load drops it
	st.Profile("work").SetActive("w2")
	st.SetChrome("w2", "claudeswitch-w2", time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	st.SetChrome("w1", "claudeswitch-w1", time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "list", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profiles []struct {
			Account    string   `json:"account"`
			ProfileDir string   `json:"profile_dir"`
			Added      *string  `json:"added"`
			LiveIn     []string `json:"live_in"`
		} `json:"chrome_profiles"`
		Supported bool `json:"supported"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !got.Supported || len(got.Profiles) != 2 {
		t.Fatalf("list = %+v", got)
	}
	p := got.Profiles[1]
	if p.Account != "w2" || p.ProfileDir != "claudeswitch-w2" || p.Added == nil ||
		*p.Added != "2026-10-07T12:00:00Z" || !slices.Equal(p.LiveIn, []string{"work"}) {
		t.Fatalf("w2 entry = %+v", p)
	}
	if got.Profiles[0].LiveIn == nil {
		t.Fatal("live_in must be [] (never null) for an account live nowhere")
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("list launched %q", l)
	}
}

func TestChromeForgetDropsTheMappingOnly(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	if _, _, err := runChromeT(t, "add", "w1", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	testshim.ResetLaunches()
	out, _, err := runChromeT(t, "forget", "w1", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got["account"] != "w1" || got["profile_dir"] != "claudeswitch-w1" || got["forgotten"] != true {
		t.Fatalf("forget = %v", got)
	}
	if chromeState(t).ChromeOf("w1") != nil {
		t.Fatal("mapping still there")
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("forget launched %q", l)
	}
	_, _, err = runChromeT(t, "forget", "w1", "--config", cfg)
	wantCode(t, err, "not_found")
}

// --json failures print the app's stable error object on stdout.
func TestChromeJSONErrorObject(t *testing.T) {
	var out bytes.Buffer
	writeChromeError(&out, &chromeError{Code: "not_found", Message: "no Chrome profile for w1",
		Hint: "cs chrome add w1"})
	var got struct {
		Error struct{ Code, Message, Hint string } `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out.String())
	}
	if got.Error.Code != "not_found" || got.Error.Hint != "cs chrome add w1" || got.Error.Message == "" {
		t.Fatalf("error object = %+v", got)
	}
}

func TestChromeAddJSON(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	out, _, err := runChromeT(t, "add", "w1", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got["account"] != "w1" || got["profile_dir"] != "claudeswitch-w1" || got["opened"] != true ||
		got["created"] != true {
		t.Fatalf("add = %v", got)
	}
	if v, ok := got["email"]; !ok || v != nil {
		t.Fatalf("email = %v (present %v), want null when unknown", v, ok)
	}
}

// The rotation notice (owner decision, lane 13): whenever the account just
// switched to has a Chrome profile, name it; when it has none but some other
// account has one (so Claude in Chrome is in use), say how to make one;
// nothing for someone who never set Chrome up, or when nothing moved.
func TestChromeRotationNotice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st := chromeState(t)
	if got := chromeRotationNotice(st, "w1", "w2"); got != "" {
		t.Fatalf("no mappings: %q", got)
	}
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	use := "Claude in Chrome: use the w2 Chrome profile (cs chrome w2)"
	for _, from := range []string{"w1", ""} {
		if got := chromeRotationNotice(st, from, "w2"); got != use {
			t.Fatalf("from %q: notice = %q, want %q", from, got, use)
		}
	}
	if got := chromeRotationNotice(st, "w2", "w2"); got != "" {
		t.Fatalf("not a rotation: %q", got)
	}
	missing := "Claude in Chrome: w1 has no Chrome profile — cs chrome add w1"
	if got := chromeRotationNotice(st, "w2", "w1"); got != missing {
		t.Fatalf("to an unmapped account: %q, want %q", got, missing)
	}
}

// The daemon says it once, with the switch.
func TestDaemonSwitchNamesTheChromeProfile(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5)
	put(st, "w1", 90)
	put(st, "w2", 20)
	st.SetChrome("w1", "claudeswitch-w1", time.Now())
	st.SetChrome("w2", "claudeswitch-w2", time.Now())

	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	var chrome []string
	for _, s := range r.nt.sent {
		if strings.Contains(s, "Claude in Chrome") {
			chrome = append(chrome, s)
		}
	}
	if len(chrome) != 1 || !strings.Contains(chrome[0], "cs chrome w2") {
		t.Fatalf("notifications = %q, want one naming cs chrome w2", r.nt.sent)
	}
	if !strings.Contains(r.logs.String(), "cs chrome w2") {
		t.Errorf("log lacks the Chrome line:\n%s", r.logs.String())
	}
	// A later evaluation with no switch says nothing more.
	before := len(r.nt.sent)
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	for _, s := range r.nt.sent[before:] {
		if strings.Contains(s, "Claude in Chrome") {
			t.Fatalf("repeated without a rotation: %q", s)
		}
	}
}

func TestDaemonSwitchToAnUnmappedAccountSaysHowToMapIt(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5)
	put(st, "w1", 90)
	put(st, "w2", 20)
	st.SetChrome("w1", "claudeswitch-w1", time.Now())
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	var chrome []string
	for _, s := range r.nt.sent {
		if strings.Contains(s, "Claude in Chrome") {
			chrome = append(chrome, s)
		}
	}
	if len(chrome) != 1 || !strings.Contains(chrome[0], "w2 has no Chrome profile — cs chrome add w2") {
		t.Fatalf("notifications = %q, want one saying cs chrome add w2", r.nt.sent)
	}
}

func TestDaemonSwitchSaysNothingAboutChromeWhenNoneIsSetUp(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5)
	put(st, "w1", 90)
	put(st, "w2", 20)
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	for _, s := range r.nt.sent {
		if strings.Contains(s, "Chrome") {
			t.Fatalf("notified %q although no account has a Chrome profile", s)
		}
	}
}

// `cs use` prints the same line after its swap.
func TestUseChromeNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st := chromeState(t)
	st.SetChrome("w1", "claudeswitch-w1", time.Now())
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	var out bytes.Buffer
	useChromeNote(&out, st, "w1", "w2")
	if !strings.Contains(out.String(), "Claude in Chrome: use the w2 Chrome profile (cs chrome w2)") {
		t.Fatalf("use note = %q", out.String())
	}
	out.Reset()
	useChromeNote(&out, st, "w2", "w2")
	if out.Len() != 0 {
		t.Fatalf("no rotation, but printed %q", out.String())
	}
}

// The plugin hook: a Claude in Chrome tool that fails because the extension
// is on another account gets one hint per rotation naming the account and the
// command.
func TestChromeHintOncePerRotation(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal") // an active account needs a record, or load drops it
	st.Profile("work").SetActive("w2")
	st.Profile("work").LastSwitch = time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")

	failure := `{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__navigate",` +
		`"error":"Claude in Chrome is signed in to a different account. Both must use the same claude.ai account."}`
	hint := func(payload string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := runChromeHint(strings.NewReader(payload), &out, &errOut, cfg)
		return out.String(), errOut.String(), code
	}

	_, stderr, code := hint(failure)
	if code != 2 || !strings.Contains(stderr, "w2") || !strings.Contains(stderr, "cs chrome w2") {
		t.Fatalf("first hint: code %d stderr %q", code, stderr)
	}
	if out, stderr, code := hint(failure); code != 0 || out != "" || stderr != "" {
		t.Fatalf("second hint in the same rotation: code %d out %q stderr %q", code, out, stderr)
	}

	// A new rotation earns a new hint.
	st = chromeState(t)
	st.Profile("work").SetActive("w1")
	st.Profile("work").LastSwitch = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	notConnected := `{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__tabs_context_mcp",` +
		`"error":"Browser extension is not connected"}`
	_, stderr, code = hint(notConnected)
	if code != 2 || !strings.Contains(stderr, "cs chrome add w1") {
		t.Fatalf("after a rotation: code %d stderr %q (w1 has no Chrome profile yet)", code, stderr)
	}
}

// Lane 13 review (MAJOR): a successful tool call is page content, never an
// error. A page that says "not connected" (a router, a VPN, a hostile page)
// must not produce a hint, nor use up the rotation's one hint.
func TestChromeHintIgnoresSuccessfulCallsWhateverTheyContain(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w2")
	st.Profile("work").SetActive("w2")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	for _, payload := range []string{
		`{"hook_event_name":"PostToolUse","tool_name":"mcp__claude-in-chrome__read_page",` +
			`"tool_response":[{"type":"text","text":"VPN status: not connected. Use the same claude.ai account."}]}`,
		`{"tool_name":"mcp__claude-in-chrome__get_page_text","tool_response":"not connected"}`,
		`{"hook_event_name":"PostToolUse","tool_name":"mcp__claude-in-chrome__get_page_text","error":"not connected"}`,
	} {
		var out, errOut bytes.Buffer
		if code := runChromeHint(strings.NewReader(payload), &out, &errOut, cfg); code != 0 ||
			out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("%s: code %d out %q err %q", payload, code, out.String(), errOut.String())
		}
	}
	if got := chromeState(t).ChromeHinted("work"); got != "" {
		t.Fatalf("page content used up the rotation's hint: %q", got)
	}
	// The real failure still gets its hint.
	var out, errOut bytes.Buffer
	failure := `{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__navigate",` +
		`"error":"not connected"}`
	if code := runChromeHint(strings.NewReader(failure), &out, &errOut, cfg); code != 2 ||
		!strings.Contains(errOut.String(), "cs chrome add w2") {
		t.Fatalf("failure: code %d err %q", code, errOut.String())
	}
}

// The plugin registers the hint for failures only.
func TestPluginRegistersTheChromeHintForFailuresOnly(t *testing.T) {
	b, err := os.ReadFile("../../plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for event, groups := range f.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, "chrome-hint") && event != "PostToolUseFailure" {
					t.Errorf("chrome-hint registered on %s", event)
				}
			}
		}
	}
	if len(f.Hooks["PostToolUseFailure"]) != 1 {
		t.Errorf("PostToolUseFailure hooks = %+v", f.Hooks["PostToolUseFailure"])
	}
}

func TestChromeHintIgnoresOtherToolsAndOtherErrors(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal") // an active account needs a record, or load drops it
	st.Profile("work").SetActive("w2")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	for _, payload := range []string{
		`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"not connected"}`,
		`{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__navigate","error":"timeout"}`,
		`not json`,
	} {
		var out, errOut bytes.Buffer
		if code := runChromeHint(strings.NewReader(payload), &out, &errOut, cfg); code != 0 ||
			out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("%s: code %d out %q err %q", payload, code, out.String(), errOut.String())
		}
	}
}
