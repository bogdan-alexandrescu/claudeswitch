package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 16: scope removed (S1), `config clean`, `add --from` (B2),
// `version --json` (B8) and why's best account (C).

const legacyCfg = `switch_at = 85

[[account]]
id = "a1"
scope = "personal"

# the work account
[[account]]
id = "w1"
scope = "work"

[[profile]]
name = "default"
pool = ["a1"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]

# directory rules, from before profiles
[project."~/work/**"]
eligible = ["work"]

[project."~/home/**"]
prefer = ["personal"]
`

func TestVersionJSONCarriesTheContract(t *testing.T) {
	var buf bytes.Buffer
	if err := cmdVersion(&buf, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["version"] != version || m["contract"] != float64(appContract) || appContract < 2 {
		t.Fatalf("version --json = %v", m)
	}
	buf.Reset()
	if err := cmdVersion(&buf, nil); err != nil || buf.String() != "claudeswitch "+version+"\n" {
		t.Fatalf("plain version = %q, %v", buf.String(), err)
	}
}

func TestAccountListHasNoScope(t *testing.T) {
	path := appWorld(t, legacyCfg)
	keychainTripwires(t)
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"scope"`) {
		t.Fatalf("account list still reports scope:\n%s", buf.String())
	}
}

func TestAccountScopeVerbIsGone(t *testing.T) {
	path := appWorld(t, legacyCfg)
	err := cmdAccount([]string{"scope", "a1", "work", "--config", path, "--json"})
	if got := errCode(t, err); got != codeUsage {
		t.Fatalf("account scope: code %q", got)
	}
	if strings.Contains(accountUsage, "scope") {
		t.Errorf("the usage still offers scope: %s", accountUsage)
	}
}

func TestDirectLoginPendingHasNoScope(t *testing.T) {
	path := appWorld(t, appTOML)
	out := captureStdout(t, func() error {
		return cmdLogin([]string{"newone", "--config", path, "--direct", "--json", "--no-open"})
	})
	t.Cleanup(func() { _ = oauth.ClearPending() })
	m := decodeJSON(t, []byte(out))
	pend, _ := m["pending"].(map[string]any)
	if pend == nil {
		t.Fatalf("answered %v", m)
	}
	if _, ok := pend["scope"]; ok {
		t.Errorf("pending still carries scope: %v", pend)
	}
}

func TestLegacyConfigLoadsAndWarnsOnce(t *testing.T) {
	path := appWorld(t, legacyCfg)
	cfg := loadOrFail(t, path)
	legacyWarned, legacyQuiet = false, false
	t.Cleanup(func() { legacyWarned, legacyQuiet = false, false })
	var b bytes.Buffer
	warnLegacy(&b, cfg)
	got := b.String()
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "a1") || !strings.Contains(got, "w1") ||
		!strings.Contains(got, "claudeswitch config clean") {
		t.Fatalf("warning:\n%s", got)
	}
	b.Reset()
	warnLegacy(&b, loadOrFail(t, appWorld(t, "[[account]]\nid = \"a1\"\n")))
	if b.Len() != 0 {
		t.Fatalf("a clean config warned: %q", b.String())
	}
}

// A single-line edit changes its line only: the legacy lines stay until
// `config clean`.
func TestConfigSetLeavesLegacyLines(t *testing.T) {
	path := appWorld(t, legacyCfg)
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, path, []string{"set", "switch_at", "80"}, true, ""); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "switch_at = 80") || !strings.Contains(got, `scope = "personal"`) ||
		!strings.Contains(got, `[project."~/work/**"]`) {
		t.Fatalf("after config set:\n%s", got)
	}
}

func TestConfigCleanRemovesScopeAndProjects(t *testing.T) {
	path := appWorld(t, legacyCfg)
	var buf bytes.Buffer
	err := configClean(&buf, path, false, true)
	if got := errCode(t, err); got != codeConfirm {
		t.Fatalf("without --yes: code %q", got)
	}
	var ae *appError
	if errors.As(err, &ae) && (!strings.Contains(ae.Message, "a1") || !strings.Contains(ae.Message, "2")) {
		t.Errorf("the confirmation must say what goes: %q", ae.Message)
	}
	if readFile(t, path) != legacyCfg {
		t.Fatal("refused, yet the file changed")
	}

	buf.Reset()
	if err := configClean(&buf, path, true, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	removed, _ := m["removed"].(map[string]any)
	sc, _ := removed["scope"].([]any)
	if len(sc) != 2 || sc[0] != "a1" || sc[1] != "w1" || removed["projects"] != float64(2) || m["path"] != path {
		t.Fatalf("answer %v", m)
	}
	got := readFile(t, path)
	for _, gone := range []string{"scope", "[project", "eligible", "prefer", "directory rules"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"switch_at = 85", "# the work account", `name = "work"`, `pool = ["w1"]`} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q went:\n%s", kept, got)
		}
	}
	if lg := loadOrFail(t, path).Legacy(); len(lg.Scope) != 0 || lg.Projects != 0 {
		t.Errorf("still legacy after clean: %+v", lg)
	}

	// Nothing left: a zero answer, not an error.
	buf.Reset()
	if err := configClean(&buf, path, false, true); err != nil {
		t.Fatalf("a clean config: %v", err)
	}
	m = decodeJSON(t, buf.Bytes())
	removed, _ = m["removed"].(map[string]any)
	if sc, _ := removed["scope"].([]any); sc == nil || len(sc) != 0 || removed["projects"] != float64(0) {
		t.Fatalf("answer %v", m)
	}
}

// addSeams fakes the two reaches `add` makes before the vault: the profile's
// identity (claude auth status) and its live item.
func addSeams(t *testing.T) (asked *[]string, liveAsked *[]string) {
	t.Helper()
	var ids, lives []string
	oldID, oldLive := addIdentity, cliLiveFor
	addIdentity = func(in config.Profile) (string, string, string) {
		ids = append(ids, in.Name)
		if in.Name == "work" {
			return "jane@example.com", "Example Org", "org-1"
		}
		return "sam@example.com", "Other Org", "org-2"
	}
	cliLiveFor = func(in config.Profile) (keychain.Live, error) {
		lives = append(lives, in.Name)
		return nil, errors.New("stop before the vault")
	}
	t.Cleanup(func() { addIdentity, cliLiveFor = oldID, oldLive })
	return &ids, &lives
}

func TestAddFromSuggestsTheSourceProfilesAccount(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	ids, _ := addSeams(t)
	_, err := addAccount(path, "default", "work", false, nil)
	var ae *appError
	if !errors.As(err, &ae) || ae.Code != codeNameRequired || ae.Hint != "example-org" {
		t.Fatalf("got %#v", err)
	}
	if len(*ids) == 0 || (*ids)[0] != "work" {
		t.Fatalf("identity asked of %v, want work (the source)", *ids)
	}
}

func TestAddFromReadsTheSourceAndJoinsTheTarget(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	_, lives := addSeams(t)
	_, err := addAccount(path, "default", "work", false, []string{"newone"})
	if err == nil || !strings.Contains(err.Error(), `profile "work"`) {
		t.Fatalf("got %v", err)
	}
	if len(*lives) != 1 || (*lives)[0] != "work" {
		t.Fatalf("live item read from %v, want work", *lives)
	}
	// An account another pool lists cannot be filed under the target.
	_, err = addAccount(path, "default", "work", false, []string{"w1"})
	if got := errCode(t, err); got != codeOutsidePool {
		t.Fatalf("w1 into default: code %q", got)
	}
}

func TestAddFromUnknownProfile(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	addSeams(t)
	_, err := addAccount(path, "", "nope", false, []string{"x"})
	if got := errCode(t, err); got != codeNotFound {
		t.Fatalf("code %q", got)
	}
}

func TestWhyJSONNamesTheBestAccount(t *testing.T) {
	_, cfg, st := multiWorld(t)
	out := whyJSON(cfg, st, time.Now(), "")
	list, _ := out["profiles"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("profiles = %v", out)
	}
	for _, m := range list {
		switch m["profile"] {
		case "work":
			if m["best"] != "w2" || m["best_why"] != nil {
				t.Errorf("work: best %v, why %v", m["best"], m["best_why"])
			}
		case "default":
			if m["best"] != nil || !strings.Contains(m["best_why"].(string), "no other account") {
				t.Errorf("default: best %v, why %v", m["best"], m["best_why"])
			}
		}
	}
	if _, ok := out["dir"]; ok {
		t.Error("why --json still reports the directory, which only fed project rules")
	}
}

func TestWhyJSONSingleProfileBest(t *testing.T) {
	_, cfg, st := pinWorld(t, 90)
	out := whyJSON(cfg, st, time.Now(), "")
	if out["best"] != "b" {
		t.Fatalf("best = %v", out["best"])
	}
	if _, ok := out["best_why"]; !ok {
		t.Error("best_why must be present (null)")
	}
}

var _ = state.DefaultProfile

// Review: a comment directly against a [project] header with no blank line
// above it may be the end of the block before (a commented-out setting):
// clean keeps it. A comment run set off by a blank line goes with the table.
func TestConfigCleanKeepsCommentsThatBelongAbove(t *testing.T) {
	text := "[[profile]]\nname = \"work\"\n# switch_at = 70\n[project.\"~/a/**\"]\neligible = [\"work\"]\n" +
		"\n# rules for b\n[project.\"~/b/**\"]\nprefer = [\"work\"]\n"
	got := removeLegacy(text)
	if !strings.Contains(got, "# switch_at = 70") {
		t.Errorf("a comment of the profile block went:\n%s", got)
	}
	if strings.Contains(got, "# rules for b") || strings.Contains(got, "[project") {
		t.Errorf("the second table or its own comment survived:\n%s", got)
	}
}

// Review: the legacy warning is printed once per process, and never for
// the status line, hooks or `context`, whose stderr is someone else's.
func TestLegacyWarningOncePerProcessAndQuietForHooks(t *testing.T) {
	cfg := loadOrFail(t, appWorld(t, legacyCfg))
	t.Cleanup(func() { legacyWarned, legacyQuiet = false, false })
	legacyWarned, legacyQuiet = false, false
	var b bytes.Buffer
	warnLegacy(&b, cfg)
	warnLegacy(&b, cfg)
	if strings.Count(b.String(), "warning:") != 1 {
		t.Fatalf("warned %d times:\n%s", strings.Count(b.String(), "warning:"), b.String())
	}
	for _, c := range [][]string{{"statusline"}, {"context"}, {"chrome", "hint"}} {
		if !quietCommand(c[0], c[1:]) {
			t.Errorf("%v is not quiet", c)
		}
	}
	if quietCommand("status", nil) || quietCommand("chrome", []string{"list"}) {
		t.Error("an ordinary command is quiet")
	}
	legacyWarned, legacyQuiet = false, true
	b.Reset()
	warnLegacy(&b, cfg)
	if b.Len() != 0 {
		t.Fatalf("a quiet command warned: %q", b.String())
	}
}

// Owner decision (lane 16): Switch to best never offers an account live in
// another profile, nor one that needs a sign-in.
func TestWhyJSONBestSkipsAccountsLiveElsewhereOrNeedingLogin(t *testing.T) {
	_, cfg, st := multiWorld(t)
	st.Default().SetActive("w2") // signed in by hand in the default profile
	work := whyBlock(t, whyJSON(cfg, st, time.Now(), ""), "work")
	if work["best"] != nil || !strings.Contains(work["best_why"].(string), "w2: live in default") {
		t.Fatalf("work: best %v, why %v", work["best"], work["best_why"])
	}

	st.Default().SetActive("personal")
	st.Get("w2").LastErr = "usage API: 401 Unauthorized"
	work = whyBlock(t, whyJSON(cfg, st, time.Now(), ""), "work")
	if work["best"] != nil || !strings.Contains(work["best_why"].(string), "w2: needs a sign-in") {
		t.Fatalf("work: best %v, why %v", work["best"], work["best_why"])
	}
}

func whyBlock(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	list, _ := out["profiles"].([]map[string]any)
	for _, m := range list {
		if m["profile"] == name {
			return m
		}
	}
	t.Fatalf("no %s block in %v", name, out)
	return nil
}

// Owner decision (lane 16): a login saved from one profile into another's
// accounts stays allowed, and says what happens next.
func TestAddAcrossProfilesSaysWhatHappens(t *testing.T) {
	if got := crossProfileNote("default", "work"); got !=
		"still signed in in default — default will move off it; work can use it after" {
		t.Fatalf("note = %q", got)
	}
	if got := crossProfileNote("work", "work"); got != "" {
		t.Fatalf("same profile: note = %q", got)
	}
}

// The account just added to work while still live in default: work's loop
// never swaps it in, and default's loop moves off it.
func TestDaemonAfterAddAcrossProfiles(t *testing.T) {
	cfg := twoProfiles()
	cfg.Accounts = append(cfg.Accounts, config.Account{ID: "x"})
	cfg.Priority = append(cfg.Priority, "x")
	cfg.Profiles[1].Pool = []string{"w1", "w2", "x"}
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("x")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 20)
	put(st, "w1", 90)
	put(st, "w2", 99)
	put(st, "x", 1)

	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	for _, s := range r.v.ops("swap") {
		if s.account == "x" {
			t.Fatalf("work swapped in x while it is live in default: %+v", s)
		}
	}
	if st.Profile("work").Active == "x" {
		t.Fatal("work recorded x as live")
	}

	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	swaps := r.v.ops("swap")
	if len(swaps) != 1 || swaps[0].account != "a" || swaps[0].item != r.items["default"] {
		t.Fatalf("swaps = %+v, want default moved off x onto a", swaps)
	}
	if got := st.Profile("default").Active; got != "a" {
		t.Fatalf("default is on %q, want a", got)
	}
}

// Review: the vault sync finding the live credential is an account just
// added from this profile into another's pool (it was unknown before) is
// the state the person chose: Info. A credential that changed from one
// configured account to another pool's stays an error.
func TestDaemonCrossPoolAfterAddFromIsInfo(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	il := r.prof("default")
	r.d.noteCrossPool(il, "w2", "")
	logs := r.logs.String()
	if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, "moving off it") || strings.Contains(logs, "level=ERROR") {
		t.Fatalf("after add --from:\n%s", logs)
	}
	r2 := newRig(t, twoProfiles(), true)
	r2.d.noteCrossPool(r2.prof("default"), "w2", "a")
	if !strings.Contains(r2.logs.String(), "level=ERROR") {
		t.Fatalf("an unexpected change was not an error:\n%s", r2.logs.String())
	}
}
