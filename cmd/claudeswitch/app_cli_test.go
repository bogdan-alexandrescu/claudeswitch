package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// The app's CLI (IMPROVEMENTS M6): every command answers --json with one
// object, and every refusal with {"error":{code,message,hint}}.

// appWorld gives a test its own HOME (so state.json is its own) and a config.
func appWorld(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRunning := daemonRunning
	daemonRunning = func() bool { return false }
	oldLive := accountLiveIn
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		return "", false
	}
	oldAcct := acctSeams
	acctSeams = acctDeps{
		has:         func(string) bool { return false },
		identity:    func(string) (string, string) { return "", "" },
		describe:    func(string) string { return "" },
		plan:        func(string) string { return "" },
		deleteVault: func(string) error { return errors.New("no vault in tests") },
		twin:        func(*config.Config, *state.State, string) string { return "" },
	}
	t.Cleanup(func() { daemonRunning, accountLiveIn, acctSeams = oldRunning, oldLive, oldAcct })
	return path
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, b)
	}
	return m
}

// errCode is the code the error object carries.
func errCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got none")
	}
	var buf bytes.Buffer
	writeJSONError(&buf, err)
	m := decodeJSON(t, buf.Bytes())
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object: %s", buf.String())
	}
	for _, k := range []string{"code", "message", "hint"} {
		if _, ok := e[k]; !ok {
			t.Fatalf("error object lacks %q: %s", k, buf.String())
		}
	}
	return e["code"].(string)
}

func TestErrorObjectShape(t *testing.T) {
	if got := errCode(t, appErr(codeLive, "do this", "it is live")); got != codeLive {
		t.Errorf("code %q", got)
	}
	if got := errCode(t, errors.New("plain")); got != codeFailed {
		t.Errorf("a plain error is %q, want failed", got)
	}
	if got := errCode(t, state.ErrDaemonRunning); got != codeDaemonRunning {
		t.Errorf("the daemon lock refusal is %q", got)
	}
	wrapped := wrapErr(codeNotFound, "h", errors.New("gone"))
	var buf bytes.Buffer
	writeJSONError(&buf, wrapped)
	e := decodeJSON(t, buf.Bytes())["error"].(map[string]any)
	if e["message"] != "gone" || e["hint"] != "h" {
		t.Errorf("wrapped: %v", e)
	}
	if appErr(codeUsage, "the hint", "the message").Error() != "the message\n  the hint" {
		t.Error("the human form is the message, then the hint")
	}
}

func TestJSONRequested(t *testing.T) {
	for args, want := range map[string]bool{
		"use a --json": true, "--json=true": true, "use a": false, "run p -- --json": false,
	} {
		if got := jsonRequested(strings.Fields(args)); got != want {
			t.Errorf("%q: %v", args, got)
		}
	}
}

// The schema is generated from the table `cs config` uses: every setting,
// its default from config.Defaults, and the profile scope on exactly the
// keys a [[profile]] may override.
func TestConfigSchemaDescribesEverySetting(t *testing.T) {
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, "", []string{"schema"}, true, ""); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Settings []struct {
			Key, Type, Default, Description string
			Scopes                          []string
			Min, Max                        *float64
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Settings) != len(settings()) {
		t.Fatalf("%d settings in the schema, %d in the table", len(out.Settings), len(settings()))
	}
	def := config.Defaults()
	var profile []string
	for i, s := range settings() {
		got := out.Settings[i]
		if got.Key != s.name || got.Default != s.get(def) || got.Type == "" || got.Description == "" {
			t.Errorf("%s: %+v", s.name, got)
		}
		if slices.Contains(got.Scopes, "profile") {
			profile = append(profile, got.Key)
		}
	}
	want := []string{"switch_at", "switch_at_weekly", "hard_floor", "landing_margin", "prefer", "models"}
	slices.Sort(want)
	slices.Sort(profile)
	if !slices.Equal(profile, want) {
		t.Errorf("profile-scoped: %v, want %v", profile, want)
	}
}

const appTOML = `# my thresholds
switch_at = 80 # keep this comment

priority = ["a1", "a2"]

# the first account
[[account]]
id = "a1"

# the second account
[[account]]
id = "a2"

[[account]]
id = "a3"
`

func TestConfigSetIsATextualEditKeepingCommentsModeAndLink(t *testing.T) {
	real := appWorld(t, appTOML)
	if err := os.Chmod(real, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, link, []string{"set", "switch_at", "75"}, true, ""); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["previous"] != "80" || m["value"] != "75" || m["key"] != "switch_at" {
		t.Errorf("set answered %v", m)
	}
	text := readFile(t, real)
	if !strings.Contains(text, "switch_at = 75 # keep this comment") || !strings.Contains(text, "# the second account") {
		t.Errorf("the edit lost the layout:\n%s", text)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	// A key not yet in the file is added at the top level.
	buf.Reset()
	if err := runConfigCmd(&buf, link, []string{"set", "cooldown", "7m"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if c, err := config.Load(link); err != nil || c.Cooldown.String() != "7m0s" || c.SwitchAt != 75 {
		t.Fatalf("read back %+v, %v", c, err)
	}
	buf.Reset()
	if err := runConfigCmd(&buf, link, []string{"get", "cooldown"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["value"] != "7m0s" || m["scope"] != "global" {
		t.Errorf("get answered %v", m)
	}
}

func TestConfigSetRefusals(t *testing.T) {
	path := appWorld(t, appTOML)
	before := readFile(t, path)
	var buf bytes.Buffer
	for _, c := range []struct {
		args []string
		code string
	}{
		{[]string{"set", "switch_at", "lots"}, codeInvalidValue},
		{[]string{"set", "switch_at", "120"}, codeInvalidValue},
		{[]string{"set", "hard_floor", "50"}, codeInvalidValue}, // below switch_at
		{[]string{"set", "switch_when", "later"}, codeInvalidValue},
		{[]string{"set", "nonsense", "1"}, codeNotFound},
		{[]string{"set", "switch_at"}, codeUsage},
	} {
		if got := errCode(t, runConfigCmd(&buf, path, c.args, true, "")); got != c.code {
			t.Errorf("%v: code %q, want %q", c.args, got, c.code)
		}
	}
	if readFile(t, path) != before {
		t.Error("a refused set changed the file")
	}
}

const profilesTOML = `priority = ["a1", "a2", "w1"]

[[account]]
id = "a1"
[[account]]
id = "a2"
[[account]]
id = "w1"

[[profile]]
name = "default"
pool = ["a1"]

# the work one
[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

func TestProfileSetOverridesAndInherits(t *testing.T) {
	path := appWorld(t, profilesTOML)
	var buf bytes.Buffer
	if err := profileSet(&buf, path, "work", "switch_at", "70", true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["override"] != "70" || m["effective"] != "70" {
		t.Errorf("answered %v", m)
	}
	cfg := loadOrFail(t, path)
	if cfg.SwitchAtFor("work") != 70 || cfg.SwitchAtFor("default") != config.DefaultSwitchAt {
		t.Errorf("work %v default %v", cfg.SwitchAtFor("work"), cfg.SwitchAtFor("default"))
	}
	if !strings.Contains(readFile(t, path), "# the work one") {
		t.Error("the comment went")
	}
	buf.Reset()
	if err := profileSet(&buf, path, "work", "models", "none", true); err != nil {
		t.Fatal(err)
	}
	if got := loadOrFail(t, path).ModelsFor("work"); got == nil || len(got) != 0 {
		t.Errorf("models none: %#v", got)
	}
	buf.Reset()
	if err := profileSet(&buf, path, "work", "switch_at", "inherit", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["override"] != nil {
		t.Errorf("inherit answered %v", m)
	}
	if loadOrFail(t, path).Profiles[1].SwitchAt != 0 {
		t.Error("inherit left the override")
	}
	if got := errCode(t, profileSet(&buf, path, "work", "hard_floor", "50", true)); got != codeInvalidValue {
		t.Errorf("hard_floor below switch_at: %q", got)
	}
	if got := errCode(t, profileSet(&buf, path, "work", "poll_hot", "1m", true)); got != codeNotFound {
		t.Errorf("a global-only key: %q", got)
	}
	if got := errCode(t, profileSet(&buf, path, "nowhere", "switch_at", "70", true)); got != codeNotFound {
		t.Errorf("an unknown profile: %q", got)
	}
	// config get --profile reports the effective value and the override.
	buf.Reset()
	if err := profileSet(&buf, path, "work", "landing_margin", "0", true); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runConfigCmd(&buf, path, []string{"get", "landing_margin"}, true, "work"); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["value"] != "0" || m["override"] != "0" || m["profile"] != "work" {
		t.Errorf("get --profile: %v", m)
	}
}

func TestProfilePoolAddRemoveAndMove(t *testing.T) {
	path := appWorld(t, profilesTOML)
	var buf bytes.Buffer
	// a2 is in default by D6 only: work may take it.
	if err := profilePool(&buf, path, "work", "add", "a2", "", true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["profile"] != "work" || m["changed"] != true {
		t.Errorf("answered %v", m)
	}
	if owner, _ := loadOrFail(t, path).ProfileOf("a2"); owner != "work" {
		t.Fatalf("a2 is in %q", owner)
	}
	// a1 is listed by default: D1 refuses, naming the move.
	err := profilePool(&buf, path, "work", "add", "a1", "", true)
	if got := errCode(t, err); got != codeInOtherPool || !strings.Contains(err.Error(), "--to work") {
		t.Errorf("listed elsewhere: %q, %v", got, err)
	}
	// remove --to moves it in one edit.
	buf.Reset()
	if err := profilePool(&buf, path, "default", "remove", "a1", "work", true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if owner, _ := cfg.ProfileOf("a1"); owner != "work" || len(cfg.Profiles[0].Pool) != 0 {
		t.Fatalf("after the move: a1 in %q, default lists %v", owner, cfg.Profiles[0].Pool)
	}
	// Removing from work: a1 falls back into default (D6).
	buf.Reset()
	if err := profilePool(&buf, path, "work", "remove", "a1", "", true); err != nil {
		t.Fatal(err)
	}
	if owner, _ := loadOrFail(t, path).ProfileOf("a1"); owner != "default" {
		t.Errorf("a1 in %q", owner)
	}
}

func TestProfilePoolRefusesOrphansAndLiveAccounts(t *testing.T) {
	path := appWorld(t, strings.Replace(strings.Replace(profilesTOML, `name = "default"`, `name = "home"`, 1),
		`pool = ["a1"]`, `pool = ["a1", "a2"]`, 1))
	before := readFile(t, path)
	var buf bytes.Buffer
	if got := errCode(t, profilePool(&buf, path, "work", "remove", "w1", "", true)); got != codeWouldOrphan {
		t.Errorf("no default to fall back to: %q", got)
	}
	accountLiveIn = func(_ *config.Config, _ *state.State, id string, consider func(string, bool) bool) (string, bool) {
		if id == "w1" && consider("work", false) {
			return "work", true
		}
		return "", false
	}
	err := profilePool(&buf, path, "work", "remove", "w1", "home", true)
	if got := errCode(t, err); got != codeLive || !strings.Contains(err.Error(), `"work"`) {
		t.Errorf("live in the source: %q, %v", got, err)
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
}

func TestProfilePoolAddRefusesAnAccountLiveElsewhere(t *testing.T) {
	path := appWorld(t, profilesTOML)
	before := readFile(t, path)
	var asked []string
	accountLiveIn = func(_ *config.Config, _ *state.State, id string, consider func(string, bool) bool) (string, bool) {
		for _, n := range []string{"default", "work"} {
			if consider(n, false) {
				asked = append(asked, n)
			}
		}
		if id == "a2" && consider("default", false) {
			return "default", true
		}
		return "", false
	}
	var buf bytes.Buffer
	if got := errCode(t, profilePool(&buf, path, "work", "add", "a2", "", true)); got != codeLive {
		t.Errorf("live in default: %q", got)
	}
	if slices.Contains(asked, "work") {
		t.Error("the profile it joins was counted as elsewhere")
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
}

func TestPriorityScopePinAndUnpin(t *testing.T) {
	path := appWorld(t, appTOML)
	var buf bytes.Buffer
	if err := setPriority(&buf, path, []string{"a3", "a1"}, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	order, _ := m["order"].([]any)
	if len(order) != 3 || order[0] != "a3" || order[1] != "a1" || order[2] != "a2" {
		t.Errorf("order %v", m["order"])
	}
	if got := errCode(t, setPriority(&buf, path, []string{"a1", "zz"}, true)); got != codeNotFound {
		t.Errorf("unknown id: %q", got)
	}
	if got := errCode(t, setPriority(&buf, path, []string{"a1", "a1"}, true)); got != codeInvalidValue {
		t.Errorf("twice: %q", got)
	}

	// Pinning needs the account live in its profile.
	if got := errCode(t, accountPin(&buf, path, "a1", false, true)); got != codeNotActive {
		t.Errorf("pin of a non-live account: %q", got)
	}
	st, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("a1") // an account with no record is not believed live
	st.Profile("default").SetActive("a1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := accountPin(&buf, path, "a1", false, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Load(""); st.Profile("default").Pinned != "a1" {
		t.Error("the pin was not saved")
	}
	buf.Reset()
	if err := accountUnpin(&buf, path, "", "", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); len(m["unpinned"].([]any)) != 1 {
		t.Errorf("unpin answered %v", m)
	}
	if st, _ := state.Load(""); st.Profile("default").Pinned != "" {
		t.Error("the pin stayed")
	}
}

// M5: delete removes the credential, the block, the priority and pool
// entries and the state record, after the §3 check, leaving the rest of the
// file as it was.
func TestAccountDeleteRemovesEverything(t *testing.T) {
	path := appWorld(t, profilesTOML)
	deleted := ""
	acctSeams.has = func(id string) bool { return id == "w1" }
	acctSeams.identity = func(string) (string, string) { return "person-1@org-1", "org-1" }
	acctSeams.deleteVault = func(id string) error { deleted = id; return nil }
	st, _ := state.Load("")
	st.Get("w1").OrgID = "org-1"
	st.AddVaulted("w1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "w1", true, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	rm := m["removed"].(map[string]any)
	if m["account"] != "w1" || m["seat"] != "person-1@org-1" || rm["credential"] != true || rm["config"] != true ||
		rm["priority"] != true || rm["pool"] != "work" {
		t.Errorf("answered %v", m)
	}
	if deleted != "w1" {
		t.Error("the credential was not deleted")
	}
	cfg := loadOrFail(t, path)
	if hasAccount(cfg, "w1") || slices.Contains(cfg.Priority, "w1") || len(cfg.Profiles[1].Pool) != 0 {
		t.Errorf("still named: %+v", cfg)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "# the work one") {
		t.Errorf("the next block's comment went:\n%s", text)
	}
	if st, _ := state.Load(""); st.Accounts["w1"] != nil || slices.Contains(st.Vaulted, "w1") {
		t.Error("the state record stayed")
	}
}

func TestAccountDeleteRefusals(t *testing.T) {
	path := appWorld(t, profilesTOML)
	before := readFile(t, path)
	acctSeams.has = func(string) bool { return true }
	acctSeams.deleteVault = func(string) error { t.Fatal("deleted after a refusal"); return nil }
	var buf bytes.Buffer

	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		return "work", true
	}
	err := accountDelete(&buf, path, "w1", true, true)
	if got := errCode(t, err); got != codeLive || !strings.Contains(err.Error(), `profile "work"`) {
		t.Errorf("live: %q, %v", got, err)
	}
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		return "", false
	}

	promptsOff = true
	t.Cleanup(func() { promptsOff = false })
	if got := errCode(t, accountDelete(&buf, path, "w1", false, true)); got != codeConfirm {
		t.Errorf("no --yes and no terminal: %q", got)
	}
	acctSeams.has = func(string) bool { return false }
	if got := errCode(t, accountDelete(&buf, path, "nobody", true, true)); got != codeNotFound {
		t.Errorf("unknown: %q", got)
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
	// A failed credential delete puts the config back.
	acctSeams.has = func(string) bool { return true }
	acctSeams.deleteVault = func(string) error { return errors.New("store says no") }
	if err := accountDelete(&buf, path, "w1", true, true); err == nil {
		t.Fatal("a failed credential delete must fail")
	}
	if readFile(t, path) != before {
		t.Error("the config was not put back")
	}
}

func TestDeleteBlockKeepsNeighbours(t *testing.T) {
	out, err := deleteBlock(appTOML, blockRef{"account", "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"a2"`+"\nscope") || strings.Contains(out, "# the second account") {
		t.Errorf("the block or its comment stayed:\n%s", out)
	}
	if !strings.Contains(out, "# the first account") || !strings.Contains(out, `id = "a3"`) {
		t.Errorf("a neighbour went:\n%s", out)
	}
	multi := "priority = [\n  \"a1\", # first\n  \"a2\",\n]\n"
	got, found, err := mapArray(multi, blockRef{}, "priority", func(v []string) []string {
		return slices.DeleteFunc(v, func(s string) bool { return s == "a1" })
	})
	if err != nil || !found || got != "priority = [\"a2\"]\n" {
		t.Errorf("multi-line list: %q, %v, %v", got, found, err)
	}
}

func TestAddWithNoNameAndNoTerminalHintsTheSuggestion(t *testing.T) {
	_, err := chooseAddName(nil, false, liveIs("jane@example.com", "Example Org", "org-1"), nil, noPrompt(t))
	var ae *appError
	if !errors.As(err, &ae) || ae.Code != codeNameRequired || ae.Hint != "example-org" {
		t.Fatalf("got %#v", err)
	}
}

func TestDirectLoginJSON(t *testing.T) {
	path := appWorld(t, appTOML)
	out := captureStdout(t, func() error {
		return cmdLogin([]string{"newone", "--config", path, "--direct", "--json", "--no-open"})
	})
	m := decodeJSON(t, []byte(out))
	pend, _ := m["pending"].(map[string]any)
	if u, _ := m["url"].(string); !strings.HasPrefix(u, "https://") || m["expires_at"] == nil || pend == nil ||
		pend["account"] != "newone" || pend["new_account"] != true {
		t.Errorf("answered %v", m)
	}
	t.Cleanup(func() { _ = oauth.ClearPending() })
}

func TestLoginCodeWithNothingPending(t *testing.T) {
	path := appWorld(t, appTOML)
	cfg := loadOrFail(t, path)
	st, _ := state.Load("")
	_, err := loginComplete(cfg, st, nil, "a-code")
	if got := errCode(t, err); got != codeNoPendingLogin {
		t.Errorf("code %q", got)
	}
}

func TestRecoveryJSON(t *testing.T) {
	path := appWorld(t, appTOML+"account_uuid = \"person-3\"\norg_id = \"org-3\"\n")
	cfg := loadOrFail(t, path)
	cleared := ""
	deps := recoveryDeps{
		list: func([]string) []vault.RecoveryItem {
			return []vault.RecoveryItem{{Slot: "r1", Profile: "default", Seat: "person-3@org-3", HasRefresh: true}}
		},
		clear: func(slot string) error { cleared = slot; return nil },
	}
	var buf bytes.Buffer
	if err := runRecovery(&buf, cfg, nil, recoveryOpts{json: true}, deps); err != nil {
		t.Fatal(err)
	}
	items := decodeJSON(t, buf.Bytes())["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["account"] != "a3" || items[0].(map[string]any)["renewable"] != true {
		t.Errorf("list answered %v", items)
	}
	if got := errCode(t, runRecovery(&buf, cfg, []string{"clear", "r1"}, recoveryOpts{json: true}, deps)); got != codeConfirm {
		t.Errorf("clear without --yes: %q", got)
	}
	buf.Reset()
	if err := runRecovery(&buf, cfg, []string{"clear", "r1"}, recoveryOpts{json: true, yes: true}, deps); err != nil {
		t.Fatal(err)
	}
	if cleared != "r1" || decodeJSON(t, buf.Bytes())["cleared"] != true {
		t.Error("not cleared")
	}
}
