package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// IMPROVEMENTS C4 (owner decision 2026-10-23): Claude in Chrome follows the
// account. A new bridge connection finds only a browser on the live account's
// own channel, and a Chrome profile's extension connects only while that
// profile runs (GROUND_TRUTH §56: launching personal's brought its
// browser back in 3 s). So right after every switch, to an account with a
// Chrome profile mapped to it, claudeswitch opens that profile in the
// background, and nothing ever asks the person to sign Claude in Chrome in
// again.

func TestTheFollowLaunchOpensAProfileWithoutTakingFocus(t *testing.T) {
	got, err := chromeArgvBackground("darwin", "claudeswitch-w2")
	if err != nil || !slices.Equal(got, []string{"open", "-g", "-na", "Google Chrome", "--args",
		"--profile-directory=claudeswitch-w2"}) {
		t.Fatalf("darwin: %q %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := chromeArgvBackground("linux", "claudeswitch-w2"); err == nil {
		t.Fatal("linux without a Chrome on PATH: no error")
	}
}

// followRig is two profiles: default on a, work on w1 at 90%, so a poll
// rotates work to w2.
func followRig(t *testing.T, live bool) *rig {
	t.Helper()
	chromeWorld(t, "darwin")
	r := newRig(t, twoProfiles(), live)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5)
	put(st, "w1", 90)
	put(st, "w2", 20)
	return r
}

func chromeNotices(r *rig) []string {
	var out []string
	for _, s := range r.nt.sent {
		if strings.Contains(s, "Claude in Chrome") {
			out = append(out, s)
		}
	}
	return out
}

func TestDaemonSwitchOpensTheNewAccountsChromeProfile(t *testing.T) {
	r := followRig(t, true)
	r.d.st.SetChrome("w1", "claudeswitch-w1", time.Now())
	r.d.st.SetChrome("w2", "claudeswitch-w2", time.Now())
	testshim.ResetLaunches()
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	want := []string{"open", "-g", "-na", "Google Chrome", "--args", "--profile-directory=claudeswitch-w2"}
	if l := testshim.Launches(); len(l) != 1 || !slices.Equal(l[0], want) {
		t.Fatalf("launches = %q, want one: %q", l, want)
	}
	n := chromeNotices(r)
	if len(n) != 1 || !strings.Contains(n[0], "opened w2's Chrome profile") || strings.Contains(strings.ToLower(n[0]), "sign") {
		t.Fatalf("notices = %q", n)
	}
	// No switch, no launch.
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	if l := testshim.Launches(); len(l) != 1 {
		t.Fatalf("launched again without a switch: %q", l)
	}
}

func TestADryRunDaemonOpensNoChromeProfile(t *testing.T) {
	r := followRig(t, false)
	r.d.st.SetChrome("w2", "claudeswitch-w2", time.Now())
	testshim.ResetLaunches()
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("a dry run launched %q", l)
	}
}

// An account with no Chrome profile of its own opens nothing; the notice says
// how to give it one, never to sign in.
func TestASwitchToAnAccountWithoutAChromeProfileOpensNothing(t *testing.T) {
	r := followRig(t, true)
	r.d.st.SetChrome("w1", "claudeswitch-w1", time.Now())
	testshim.ResetLaunches()
	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q for an account without a Chrome profile", l)
	}
	n := chromeNotices(r)
	if len(n) != 1 || !strings.Contains(n[0], "cs chrome add w2") || strings.Contains(strings.ToLower(n[0]), "sign") {
		t.Fatalf("notices = %q", n)
	}
}

// `cs use` does the same, through the same function, and its --json answer
// carries the line (the app switches through it).
func TestUseOpensTheNewAccountsChromeProfile(t *testing.T) {
	chromeWorld(t, "darwin")
	cfg := loadOrFail(t, writeConfig(t, chromeTOML))
	st := chromeState(t)
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	testshim.ResetLaunches()
	var out bytes.Buffer
	useChromeNoteIn(&out, cfg, st, "work", "w1", "w2")
	if !strings.Contains(out.String(), "opened w2's Chrome profile") {
		t.Fatalf("use note = %q", out.String())
	}
	if l := testshim.Launches(); len(l) != 1 || l[0][len(l[0])-1] != "--profile-directory=claudeswitch-w2" {
		t.Fatalf("launches = %q", l)
	}
	out.Reset()
	useChromeNoteIn(&out, cfg, st, "work", "w2", "w2")
	if out.Len() != 0 || len(testshim.Launches()) != 1 {
		t.Fatalf("no rotation, but printed %q or launched", out.String())
	}
}

// The hook after a failed Claude in Chrome call opens the live account's
// Chrome profile itself, once per rotation, and tells Claude to retry, never
// to ask the person to sign in; a session that started on another account
// is told a new session reaches the live account's browser.
func TestChromeHintOpensTheLiveAccountsProfileItself(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal")
	st.Profile("work").SetActive("w2")
	st.Profile("work").LastSwitch = time.Date(2026, 10, 23, 11, 0, 0, 0, time.UTC)
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	testshim.ResetLaunches()
	failure := `{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__tabs_context_mcp",` +
		`"error":"Browser extension is not connected"}`
	var out, errOut bytes.Buffer
	code := runChromeHint(strings.NewReader(failure), &out, &errOut, cfg)
	msg := errOut.String()
	// A session that started on another account moves to the live
	// account's browser with /mcp → claude-in-chrome → Reconnect (GROUND_TRUTH
	// §56, 2026-10-23), where /chrome does not; a new session works too.
	if code != 2 || !strings.Contains(msg, "opened") || !strings.Contains(msg, "retry") ||
		!strings.Contains(msg, "/mcp") || !strings.Contains(msg, "Reconnect") ||
		!strings.Contains(msg, "claude --resume") || !strings.Contains(msg, "Do not ask") ||
		!strings.Contains(msg, "claude -p --chrome") {
		t.Fatalf("code %d, hint %q", code, msg)
	}
	if strings.Contains(msg, "signin") || strings.Contains(msg, "sign it in") || strings.Contains(msg, "Tell the user to run") {
		t.Fatalf("the hint asks for a sign-in or a command: %q", msg)
	}
	want := []string{"open", "-g", "-na", "Google Chrome", "--args", "--profile-directory=claudeswitch-w2"}
	if l := testshim.Launches(); len(l) != 1 || !slices.Equal(l[0], want) {
		t.Fatalf("launches = %q", l)
	}
}

// With no Chrome profile of its own, the hint says how to give it one.
func TestChromeHintForAnAccountWithoutAProfileAsksForNoSignIn(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal")
	st.Profile("work").SetActive("w1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	testshim.ResetLaunches()
	var out, errOut bytes.Buffer
	code := runChromeHint(strings.NewReader(`{"hook_event_name":"PostToolUseFailure","tool_name":`+
		`"mcp__claude-in-chrome__navigate","error":"not connected"}`), &out, &errOut, cfg)
	msg := errOut.String()
	if code != 2 || !strings.Contains(msg, "cs chrome add w1") || strings.Contains(msg, "signin") {
		t.Fatalf("code %d, hint %q", code, msg)
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Fatalf("launched %q", l)
	}
}

// An email with seats in several organizations (work-team and personal
// are alice@example.com in two): the extension keeps the organization claude.ai
// is on when it is opened (alice@example.com, 2026-10-23; re-tied by switching
// claude.ai and opening it again, AI-ENG-2-MAX, 2026-10-25), so `chrome add`
// says to choose the account's organization on claude.ai before opening it.
func TestChromeAddWarnsAboutTheOrganizationForAnEmailInSeveral(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.SetEmail("w1", "w2@example.com") // chromeWorld gave w2 this email
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "add", "w2", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lo := strings.ToLower(out); !strings.Contains(lo, "organization") || !strings.Contains(lo, "before") ||
		!strings.Contains(out, "w1") {
		t.Fatalf("no warning about the organization:\n%s", out)
	}
	// One seat per email: no warning.
	st = chromeState(t)
	st.SetEmail("w1", "w1@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, _ = runChromeT(t, "add", "w2", "--config", cfg)
	if strings.Contains(out, "first opened") {
		t.Fatalf("warned for an email with one seat:\n%s", out)
	}
}

// The failure hint says the same, so Claude does not retry or switch for a
// browser that is on another seat's channel.
func TestChromeHintNamesAnotherSeatOfTheSameEmail(t *testing.T) {
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Profile("work").SetActive("w2")
	st.Profile("work").LastSwitch = time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	st.SetEmail("w2", "pat@example.com")
	st.SetEmail("w1", "pat@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	setCCDir(t, "~/.claude-work")
	testshim.ResetLaunches()
	failure := `{"hook_event_name":"PostToolUseFailure","tool_name":"mcp__claude-in-chrome__tabs_context_mcp",` +
		`"error":"Browser extension is not connected"}`
	var errOut bytes.Buffer
	runChromeHint(strings.NewReader(failure), &bytes.Buffer{}, &errOut, cfg)
	msg := errOut.String()
	for _, want := range []string{"pat@example.com is also w1", "another organization", "do not switch accounts"} {
		if !strings.Contains(msg, want) {
			t.Errorf("hint lacks %q: %s", want, msg)
		}
	}
}
