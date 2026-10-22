package main

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Phase 1 S and G (owner decisions 2026-10-21), the plugin's hooks around
// claude.ai tool calls, `claudeswitch hook pre|post`:
//
//   - S. After a switch, Claude Code's claude.ai tools act as the previous
//     account for up to about half a minute (GROUND_TRUTH §55). Before a
//     Claude in Chrome, Artifact or Docs call, the hook waits until
//     settle_after_swap (45s) after the profile's last switch, then lets the
//     call go on through the normal flow: it never answers a permission.
//   - G. After a publish, the hook records which account owns the artifact.
//     Before a call on it while another account is live, it refuses, with
//     whose it is and what to do: `cs use <owner>` when that account has
//     room, or publish a new artifact.

const hookTOML = `priority = ["a", "b"]

[[account]]
id           = "a"
account_uuid = "ua"
org_id       = "oa"

[[account]]
id           = "b"
account_uuid = "ub"
org_id       = "oa"
`

var hookClock = time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)

// hookWorld is the default profile live on b, switched to it switchedAgo
// before hookClock, with a at aPct of its session window. The clock stands
// still unless the hook sleeps; every sleep is recorded.
func hookWorld(t *testing.T, switchedAgo time.Duration, aPct float64) (cfg string, sleeps *[]time.Duration) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	cfg = writeConfig(t, hookTOML)
	st, err := state.Load("", "default")
	if err != nil {
		t.Fatal(err)
	}
	five, seven := aPct, 20.0
	reset := hookClock.Add(2 * time.Hour)
	st.Get("a").Last = &usage.Usage{FiveHour: usage.Window{Utilization: &five, ResetsAt: &reset},
		SevenDay: usage.Window{Utilization: &seven, ResetsAt: &reset}, FetchedAt: hookClock}
	st.Get("a").LastAt = hookClock
	st.Get("b")
	st.Default().SetActive("b")
	st.Default().LastSwitch = hookClock.Add(-switchedAgo)
	st.Default().LastFrom = "a"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	now := hookClock
	var slept []time.Duration
	oldNow, oldSleep := hookNow, hookSleep
	t.Cleanup(func() { hookNow, hookSleep = oldNow, oldSleep })
	hookNow = func() time.Time { return now }
	hookSleep = func(d time.Duration) { slept = append(slept, d); now = now.Add(d) }
	return cfg, &slept
}

func runHookT(t *testing.T, cfg, verb, payload string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runHook([]string{verb}, strings.NewReader(payload), &out, &errOut, cfg)
	return out.String(), errOut.String(), code
}

func preCall(tool, input string) string {
	return `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"` + tool + `","tool_input":` + input + `}`
}

// S: a claude.ai call ten seconds after a switch waits the other 35.
func TestThePreHookWaitsOutTheSettleAfterASwitch(t *testing.T) {
	for _, tool := range []string{"Artifact", "ArtifactComments", "ArtifactData",
		"mcp__claude_ai_Claude_Docs__read", "mcp__claude-in-chrome__navigate"} {
		cfg, sleeps := hookWorld(t, 10*time.Second, 40)
		out, _, code := runHookT(t, cfg, "pre", preCall(tool, `{"action":"list"}`))
		if code != 0 || out != "" {
			t.Fatalf("%s: code %d out %q; a wait answers nothing", tool, code, out)
		}
		if len(*sleeps) != 1 || (*sleeps)[0] != 35*time.Second {
			t.Fatalf("%s: slept %v, want 35s", tool, *sleeps)
		}
	}
}

// Nothing waits long after a switch, for another tool, for another event, or
// with settle_after_swap = 0.
func TestThePreHookDoesNotWaitWhenThereIsNothingToSettle(t *testing.T) {
	cfg, sleeps := hookWorld(t, time.Minute, 40)
	runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"list"}`))
	cfg2, sleeps2 := hookWorld(t, 10*time.Second, 40)
	runHookT(t, cfg2, "pre", preCall("Bash", `{"command":"ls"}`))
	runHookT(t, cfg2, "pre", preCall("mcp__claude_ai_Notion__search", `{}`))
	runHookT(t, cfg2, "pre", strings.Replace(preCall("Artifact", `{}`), "PreToolUse", "PostToolUse", 1))
	if len(*sleeps) != 0 || len(*sleeps2) != 0 {
		t.Fatalf("slept %v and %v", *sleeps, *sleeps2)
	}
	cfg3, sleeps3 := hookWorld(t, 10*time.Second, 40)
	if err := os.WriteFile(cfg3, []byte("settle_after_swap = \"0s\"\n"+hookTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookT(t, cfg3, "pre", preCall("Artifact", `{"action":"list"}`))
	if len(*sleeps3) != 0 {
		t.Fatalf("settle_after_swap = 0 slept %v", *sleeps3)
	}
}

// A switch made while it waits starts the wait again.
func TestThePreHookWaitsOnceMoreForASwitchDuringItsWait(t *testing.T) {
	cfg, sleeps := hookWorld(t, 40*time.Second, 40)
	first := true
	inner := hookSleep
	hookSleep = func(d time.Duration) {
		inner(d)
		if first {
			first = false
			st, _ := state.Load("", "default")
			st.Default().SetActive("a")
			st.Default().LastSwitch = hookNow().Add(-2 * time.Second)
			_ = st.Save()
		}
	}
	runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"list"}`))
	if len(*sleeps) != 2 || (*sleeps)[0] != 5*time.Second || (*sleeps)[1] != 43*time.Second {
		t.Fatalf("slept %v, want 5s then 43s", *sleeps)
	}
}

func postPublish(input, response string) string {
	return `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Artifact","tool_input":` + input +
		`,"tool_response":` + response + `}`
}

// G: a publish is recorded for the live account, by id and by the session's
// file path.
func TestThePostHookRecordsWhoPublished(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	_, _, code := runHookT(t, cfg, "post", postPublish(`{"file_path":"/w/page.html"}`,
		`{"artifact_id":"abc","url":"https://claude.ai/code/artifact/abc","path":"/w/page.html","owned":true}`))
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	got, ok, err := state.ArtifactOwner("abc")
	if err != nil || !ok || got.Account != "b" || got.Seat != "ub@oa" || got.Session != "s1" ||
		got.Path != "/w/page.html" || !got.At.Equal(hookClock) {
		t.Fatalf("recorded %+v %v %v", got, ok, err)
	}

	// A result given as text still names its URL.
	runHookT(t, cfg, "post", postPublish(`{"file_path":"/w/two.html","action":"publish"}`,
		`"Published https://claude.ai/code/artifact/def to the gallery."`))
	if got, ok, _ := state.ArtifactOwner("def"); !ok || got.Account != "b" {
		t.Fatalf("from text: %+v %v", got, ok)
	}
}

// Nothing is recorded when the owner is not certain (a switch within the
// settle window, so the call may have gone out as the account before), nor
// for an asset upload or any other action.
func TestThePostHookRecordsOnlyACertainPublish(t *testing.T) {
	cfg, _ := hookWorld(t, 10*time.Second, 40)
	runHookT(t, cfg, "post", postPublish(`{"file_path":"/w/page.html"}`, `{"url":"https://claude.ai/code/artifact/abc"}`))
	cfg2, _ := hookWorld(t, time.Minute, 40)
	runHookT(t, cfg2, "post", postPublish(`{"file_path":"/w/i.png","asset":true,"url":"https://claude.ai/code/artifact/abc"}`,
		`{"url":"https://claude.ai/code/artifact/abc/assets/1"}`))
	runHookT(t, cfg2, "post", postPublish(`{"action":"read","url":"https://claude.ai/code/artifact/abc"}`,
		`{"url":"https://claude.ai/code/artifact/abc"}`))
	if got, ok, _ := state.ArtifactOwner("abc"); ok {
		t.Fatalf("recorded %+v", got)
	}
}

func record(t *testing.T, a state.Artifact) {
	t.Helper()
	if err := state.RecordArtifact(a); err != nil {
		t.Fatal(err)
	}
}

func denial(t *testing.T, out string) string {
	t.Helper()
	var r struct {
		Hook struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Hook.Event != "PreToolUse" || r.Hook.Decision != "deny" {
		t.Fatalf("not a denial: %q (%v)", out, err)
	}
	return r.Hook.Reason
}

// G: a call on a's artifact while b is live is refused, with whose it is
// and `cs use a`, which has room.
func TestThePreHookRefusesACallOnAnotherAccountsArtifact(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	record(t, state.Artifact{ID: "abc", URL: "https://claude.ai/code/artifact/abc", Account: "a", Seat: "ua@oa", At: hookClock})
	for _, call := range []string{
		preCall("Artifact", `{"url":"https://claude.ai/code/artifact/abc","file_path":"/w/page.html"}`),
		preCall("Artifact", `{"action":"read","url":"https://claude.ai/code/artifact/abc"}`),
		preCall("Artifact", `{"action":"delete","url":"https://claude.ai/code/artifact/abc"}`),
		preCall("ArtifactComments", `{"url":"https://claude.ai/code/artifact/abc"}`),
		preCall("ArtifactData", `{"url":"https://claude.ai/code/artifact/abc","collection":"c"}`),
	} {
		out, _, code := runHookT(t, cfg, "pre", call)
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		why := denial(t, out)
		for _, want := range []string{"belongs to a", "now uses b", "cs use a", "publish"} {
			if !strings.Contains(why, want) {
				t.Errorf("%s: reason lacks %q: %s", call, want, why)
			}
		}
	}
}

// With no room on the owner, the way back is not offered.
func TestThePreHookOffersNoWayBackToAnOwnerWithoutRoom(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 97)
	record(t, state.Artifact{ID: "abc", Account: "a", Seat: "ua@oa", At: hookClock})
	why := denial(t, func() string {
		out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"read","url":"https://claude.ai/artifact/abc"}`))
		return out
	}())
	if strings.Contains(why, "cs use a") || !strings.Contains(why, "publish") || !strings.Contains(why, "no room") {
		t.Fatalf("reason: %s", why)
	}
}

// The owner's own calls, unknown artifacts and `open` go on.
func TestThePreHookLetsTheOwnersAndUnknownCallsGoOn(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	record(t, state.Artifact{ID: "mine", Account: "b", Seat: "ub@oa", At: hookClock})
	record(t, state.Artifact{ID: "abc", Account: "a", Seat: "ua@oa", At: hookClock})
	for _, call := range []string{
		preCall("Artifact", `{"action":"read","url":"https://claude.ai/code/artifact/mine"}`),
		preCall("Artifact", `{"action":"read","url":"https://claude.ai/code/artifact/unknown"}`),
		preCall("Artifact", `{"action":"open","url":"https://claude.ai/code/artifact/abc"}`),
		preCall("Artifact", `{"file_path":"/w/new.html"}`),
		preCall("mcp__claude-in-chrome__navigate", `{"url":"https://claude.ai/code/artifact/abc"}`),
	} {
		if out, _, code := runHookT(t, cfg, "pre", call); code != 0 || out != "" {
			t.Errorf("%s: code %d out %q", call, code, out)
		}
	}
}

// A file path is the conversation's handle on its artifact: a publish of the
// same path in the same session updates it, so it is guarded; another
// session's publish of that path makes a new one.
func TestThePreHookGuardsARepublishByPathInTheSameSessionOnly(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	record(t, state.Artifact{ID: "abc", Path: "/w/page.html", Session: "s1", Account: "a", Seat: "ua@oa", At: hookClock})
	out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"file_path":"/w/page.html"}`))
	denial(t, out)
	other := strings.Replace(preCall("Artifact", `{"file_path":"/w/page.html"}`), `"s1"`, `"s2"`, 1)
	if out, _, _ := runHookT(t, cfg, "pre", other); out != "" {
		t.Fatalf("another session's publish was refused: %q", out)
	}
}

// Owners are matched by seat, so a renamed account still owns its artifacts,
// and is named as it is called now.
func TestTheOwnerIsMatchedBySeatAcrossARename(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	record(t, state.Artifact{ID: "old", Account: "b-before", Seat: "ub@oa", At: hookClock})
	record(t, state.Artifact{ID: "abc", Account: "a-before", Seat: "ua@oa", At: hookClock})
	if out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"read","url":"https://claude.ai/artifact/old"}`)); out != "" {
		t.Fatalf("a renamed live account's own artifact was refused: %q", out)
	}
	out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"read","url":"https://claude.ai/artifact/abc"}`))
	if why := denial(t, out); !strings.Contains(why, "belongs to a:") || strings.Contains(why, "a-before") {
		t.Fatalf("reason does not name a as it is called now: %s", why)
	}
}

// A guard waits out the settle first: the refusal is about the account the
// call will act as.
func TestThePreHookSettlesBeforeItGuards(t *testing.T) {
	cfg, sleeps := hookWorld(t, 10*time.Second, 40)
	record(t, state.Artifact{ID: "abc", Account: "a", Seat: "ua@oa", At: hookClock})
	out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"read","url":"https://claude.ai/artifact/abc"}`))
	denial(t, out)
	if len(*sleeps) != 1 {
		t.Fatalf("slept %v before refusing, want the settle first", *sleeps)
	}
}

func TestTheHookIsQuiet(t *testing.T) {
	if !quietCommand("hook", []string{"pre"}) || !quietCommand("hook", []string{"post"}) {
		t.Fatal("the plugin's hooks are not quiet")
	}
}

// The plugin registers the hook before the claude.ai tools, with a timeout
// past the longest settle, and after a publish.
func TestPluginRegistersTheClaudeAIHooks(t *testing.T) {
	b, err := os.ReadFile("../../plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string  `json:"command"`
				Timeout float64 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	find := func(event, verb string) (*regexp.Regexp, float64) {
		for _, g := range f.Hooks[event] {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, "claude-ai.sh") && strings.HasSuffix(strings.TrimSpace(h.Command), " "+verb) {
					return regexp.MustCompile(g.Matcher), h.Timeout
				}
			}
		}
		t.Fatalf("no %s hook on %s", verb, event)
		return nil, 0
	}
	pre, timeout := find("PreToolUse", "pre")
	for _, tool := range []string{"Artifact", "ArtifactComments", "ArtifactData", "mcp__claude_ai_Claude_Docs__batch",
		"mcp__claude-in-chrome__navigate"} {
		if !pre.MatchString(tool) {
			t.Errorf("pre does not run before %s", tool)
		}
	}
	for _, tool := range []string{"Bash", "Artifacts", "mcp__claude_ai_Notion__search", "Agent"} {
		if pre.MatchString(tool) {
			t.Errorf("pre runs before %s", tool)
		}
	}
	if timeout <= 300 {
		t.Errorf("pre's timeout %vs does not exceed the longest settle, 5m", timeout)
	}
	post, _ := find("PostToolUse", "post")
	if !post.MatchString("Artifact") || post.MatchString("ArtifactData") || post.MatchString("Bash") {
		t.Errorf("post matcher %q", post)
	}
	if _, err := os.Stat("../../plugin/hooks/claude-ai.sh"); err != nil {
		t.Error(err)
	}
}

// An owner no longer set up here cannot be switched to: only the new
// artifact is offered.
func TestThePreHookOffersNoWayBackToAnOwnerNoLongerSetUp(t *testing.T) {
	cfg, _ := hookWorld(t, time.Minute, 40)
	record(t, state.Artifact{ID: "abc", Account: "gone", Seat: "ug@og", At: hookClock})
	out, _, _ := runHookT(t, cfg, "pre", preCall("Artifact", `{"action":"read","url":"https://claude.ai/artifact/abc"}`))
	why := denial(t, out)
	if strings.Contains(why, "cs use") || !strings.Contains(why, "no longer set up") || !strings.Contains(why, "publish") {
		t.Fatalf("reason: %s", why)
	}
}
