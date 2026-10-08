package main

import (
	"bytes"
	"context"
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
)

// IMPROVEMENTS C2: your own Chrome profiles, one per claudeswitch profile,
// overridable per account. Chrome's Local State is read from a test file
// (never the real one: HOME is a temporary directory too), and every launch
// goes to the shim's fake launchers.

const c2TOML = `
[[account]]
id = "personal"
[[account]]
id = "research"
[[account]]
id = "work-1"
[[account]]
id = "work-team"

[[profile]]
name = "default"
pool = ["personal", "research"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["work-1", "work-team"]
`

// c2LocalState is the shape of Chrome's Local State, with keys claudeswitch
// must not care about around the two it reads.
const c2LocalState = `{
  "browser": {"enabled_labs_experiments": []},
  "profile": {
    "info_cache": {
      "Default":   {"name": "Person 1", "gaia_name": "", "avatar_icon": "x"},
      "Profile 1": {"name": "Work", "is_using_default_name": false},
      "Profile 2": {"name": "Work 2"}
    },
    "last_used": "Profile 2",
    "profiles_order": ["Default", "Profile 1", "Profile 2"]
  },
  "os_crypt": {"encrypted_key": "not-read"}
}`

// c2World is chromeWorld for C2: its own HOME, the fixture config, and
// Chrome's Local State as given ("" for none: the file does not exist).
func c2World(t *testing.T, goos, localState string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	testshim.ResetLaunches()
	oldOS, oldNow, oldPaths := chromeGOOS, chromeNow, chromeLocalStatePaths
	t.Cleanup(func() { chromeGOOS, chromeNow, chromeLocalStatePaths = oldOS, oldNow, oldPaths })
	chromeGOOS = goos
	chromeNow = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	path := filepath.Join(t.TempDir(), "Local State")
	if localState != "" {
		if err := os.WriteFile(path, []byte(localState), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chromeLocalStatePaths = func() []string { return []string{path} }
	st := c2State(t)
	for _, id := range []string{"personal", "research", "work-1", "work-team"} {
		st.Get(id) // an active account needs a record, or load drops it
	}
	st.SetEmail("work-team", "team@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return writeConfig(t, c2TOML)
}

func c2State(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func c2Launches(t *testing.T) [][]string {
	t.Helper()
	return testshim.Launches()
}

func TestChromeLocalStateReadsTheProfileListOnly(t *testing.T) {
	c2World(t, "darwin", c2LocalState)
	l := readChromeLocal()
	if !l.Found {
		t.Fatal("Local State not found")
	}
	var folders, names []string
	for _, p := range l.Profiles {
		folders = append(folders, p.Folder)
		names = append(names, p.Name)
	}
	if !slices.Equal(folders, []string{"Default", "Profile 1", "Profile 2"}) ||
		!slices.Equal(names, []string{"Person 1", "Work", "Work 2"}) {
		t.Fatalf("profiles = %+v", l.Profiles)
	}
	if l.LastUsed != "Profile 2" || !l.Profiles[2].LastUsed || l.Profiles[0].LastUsed {
		t.Fatalf("last used = %q, %+v", l.LastUsed, l.Profiles)
	}
	if l.nameOf("Profile 1") != "Work" || l.nameOf("claudeswitch-x") != "" {
		t.Fatalf("nameOf")
	}
}

func TestChromeLocalStateMissingOrUnreadableIsNoList(t *testing.T) {
	c2World(t, "darwin", "")
	if l := readChromeLocal(); l.Found || len(l.Profiles) != 0 {
		t.Fatalf("missing file read as %+v", l)
	}
	c2World(t, "darwin", "{not json")
	if l := readChromeLocal(); l.Found {
		t.Fatalf("garbage read as %+v", l)
	}
}

// The paths of the owner's decision, under HOME, and Linux's in order.
func TestChromeLocalStatePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := chromeGOOS
	t.Cleanup(func() { chromeGOOS = old })
	chromeGOOS = "darwin"
	if got := defaultChromeLocalStatePaths(); !slices.Equal(got,
		[]string{filepath.Join(home, "Library/Application Support/Google/Chrome/Local State")}) {
		t.Fatalf("darwin = %q", got)
	}
	chromeGOOS = "linux"
	if got := defaultChromeLocalStatePaths(); !slices.Equal(got, []string{
		filepath.Join(home, ".config/google-chrome/Local State"),
		filepath.Join(home, ".config/chromium/Local State")}) {
		t.Fatalf("linux = %q", got)
	}
	chromeGOOS = "windows"
	if got := defaultChromeLocalStatePaths(); len(got) != 0 {
		t.Fatalf("windows = %q", got)
	}
}

// `cs profile set <p> chrome <name>` stores the folder.
func TestProfileSetChromeStoresTheFolderOfAName(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	var out bytes.Buffer
	if err := profileSet(&out, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, out.Bytes())
	if m["profile"] != "work" || m["key"] != "chrome" || m["override"] != "Profile 1" || m["name"] != "Work" {
		t.Fatalf("set = %v", m)
	}
	text, _ := os.ReadFile(cfg)
	if !strings.Contains(string(text), `chrome = "Profile 1"`) {
		t.Fatalf("config:\n%s", text)
	}
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.ChromeFor("work") != "Profile 1" || c.ChromeFor("default") != "" {
		t.Fatalf("ChromeFor work=%q default=%q", c.ChromeFor("work"), c.ChromeFor("default"))
	}

	// A folder works as well as a name.
	out.Reset()
	if err := profileSet(&out, cfg, "default", "chrome", "Default", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, out.Bytes()); m["override"] != "Default" || m["name"] != "Person 1" {
		t.Fatalf("set by folder = %v", m)
	}

	// inherit removes it: Chrome's last used again.
	out.Reset()
	if err := profileSet(&out, cfg, "work", "chrome", "inherit", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, out.Bytes()); m["override"] != nil || m["effective"] != "Profile 2" {
		t.Fatalf("inherit = %v", m)
	}
	if c, _ := config.Load(cfg); c.ChromeFor("work") != "" {
		t.Fatal("inherit left the key")
	}
}

func TestProfileSetChromeRefusesAnUnknownName(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Nope", true)
	ce := wantCode(t, err, codeNotFound)
	for _, want := range []string{`"Person 1"`, `"Work"`, `"Work 2"`} {
		if !strings.Contains(ce.Hint, want) {
			t.Fatalf("hint %q lacks %s", ce.Hint, want)
		}
	}
	if c, _ := config.Load(cfg); c.ChromeFor("work") != "" {
		t.Fatal("refusal wrote the config")
	}
}

// Without Local State a folder name still works; something that is not a
// folder name never reaches the config.
func TestProfileSetChromeWithoutLocalStateTakesAFolder(t *testing.T) {
	cfg := c2World(t, "darwin", "")
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Profile 3", true); err != nil {
		t.Fatal(err)
	}
	if c, _ := config.Load(cfg); c.ChromeFor("work") != "Profile 3" {
		t.Fatal("folder not stored")
	}
	for _, bad := range []string{"--user-data-dir=/tmp/x", "../Default", "a/b"} {
		err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", bad, true)
		wantCode(t, err, codeInvalidValue)
	}
}

func TestConfigRefusesABadChromeFolder(t *testing.T) {
	path := writeConfig(t, c2TOML+"chrome = \"-x\"\n")
	if _, err := config.Load(path); err == nil {
		t.Fatal("a chrome folder starting with - loaded")
	}
}

func TestConfigShowsAndGetsTheProfileChrome(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runConfigCmd(&out, cfg, []string{"get", "chrome"}, true, "work"); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, out.Bytes())
	if m["key"] != "chrome" || m["value"] != "Profile 1" || m["override"] != "Profile 1" ||
		m["scope"] != "profile" || m["profile"] != "work" {
		t.Fatalf("get = %v", m)
	}
	out.Reset()
	if err := runConfigCmd(&out, cfg, []string{"get", "chrome"}, true, "default"); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, out.Bytes()); m["value"] != "" || m["override"] != nil {
		t.Fatalf("get default = %v", m)
	}
	err := runConfigCmd(&bytes.Buffer{}, cfg, []string{"get", "chrome"}, true, "")
	wantCode(t, err, codeUsage)

	out.Reset()
	if err := runConfigCmd(&out, cfg, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "chrome") || !strings.Contains(out.String(), `"Work" (Profile 1)`) ||
		!strings.Contains(out.String(), "Chrome's last used") {
		t.Fatalf("config output lacks the chrome lines:\n%s", out.String())
	}
	out.Reset()
	if err := runConfigCmd(&out, cfg, nil, true, ""); err != nil {
		t.Fatal(err)
	}
	ch, _ := decodeJSON(t, out.Bytes())["chrome"].(map[string]any)
	if ch["work"] != "Profile 1" || ch["default"] != nil {
		t.Fatalf("config --json chrome = %v", ch)
	}
}

func TestProfileListJSONCarriesTheChromeProfile(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) { return nil, keychain.ErrNotFound }
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	st := c2State(t)
	st.Profile("work").SetActive("work-team")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := profileListJSON(&out, cfg); err != nil {
		t.Fatal(err)
	}
	list := decodeJSON(t, out.Bytes())["profiles"].([]any)
	def, work := list[0].(map[string]any), list[1].(map[string]any)
	if work["chrome"] != "Profile 1" || work["chrome_name"] != "Work" {
		t.Fatalf("work = %v", work)
	}
	if def["chrome"] != nil || def["chrome_name"] != nil {
		t.Fatalf("default = %v", def)
	}
	r, _ := work["chrome_resolved"].(map[string]any)
	if r["account"] != "work-team" || r["profile_dir"] != "Profile 1" || r["rule"] != "profile" {
		t.Fatalf("work chrome_resolved = %v", work["chrome_resolved"])
	}
	if def["chrome_resolved"] != nil {
		t.Fatalf("default has no live account, resolved = %v", def["chrome_resolved"])
	}
}

// The resolution, in order: the account's own mapping, the profile's
// chrome, Chrome's last used; and which rule applied.
func TestResolveChrome(t *testing.T) {
	cfgPath := c2World(t, "darwin", c2LocalState)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st := c2State(t)
	local := readChromeLocal()

	r := resolveChrome(cfg, st, local, "work", "work-1")
	if r.Rule != "last_used" || r.Dir != "Profile 2" || r.Name != "Work 2" || r.Profile != "work" {
		t.Fatalf("nothing set: %+v", r)
	}
	cfg.Profiles[1].Chrome = "Profile 1"
	if r := resolveChrome(cfg, st, local, "work", "work-1"); r.Rule != "profile" || r.Dir != "Profile 1" ||
		r.Name != "Work" {
		t.Fatalf("profile set: %+v", r)
	}
	st.SetChromeExisting("work-1", "Default", time.Now())
	if r := resolveChrome(cfg, st, local, "work", "work-1"); r.Rule != "account" || r.Dir != "Default" ||
		r.Name != "Person 1" {
		t.Fatalf("account mapped: %+v", r)
	}
	// With no profile named, the account's pool decides.
	if r := resolveChrome(cfg, st, local, "", "work-team"); r.Profile != "work" || r.Rule != "profile" {
		t.Fatalf("by pool: %+v", r)
	}
	// No Local State and nothing set: nothing to open.
	if r := resolveChrome(cfg, st, chromeLocal{}, "default", "personal"); r.Rule != "none" || r.Dir != "" {
		t.Fatalf("none: %+v", r)
	}
}

func TestChromeOpenFollowsTheResolution(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "work-1", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	if m["profile_dir"] != "Profile 1" || m["rule"] != "profile" || m["name"] != "Work" || m["opened"] != true {
		t.Fatalf("open = %v", m)
	}
	want := [][]string{{"open", "-na", "Google Chrome", "--args", "--profile-directory=Profile 1"}}
	if got := c2Launches(t); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("launches = %q, want %q", got, want)
	}
	// Chrome's last used for an account of a profile without chrome.
	testshim.ResetLaunches()
	if _, _, err := runChromeT(t, "personal", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if got := c2Launches(t); len(got) != 1 || got[0][len(got[0])-1] != "--profile-directory=Profile 2" {
		t.Fatalf("last used: launches = %q", got)
	}
}

func TestChromeProfilesListsLocalStateAndWhoUsesEach(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	st := c2State(t)
	st.SetChromeExisting("research", "Profile 2", time.Now())
	st.SetChrome("personal", "claudeswitch-personal", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "profiles", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	if m["local_state"] != true || m["last_used"] != "Profile 2" || m["supported"] != true {
		t.Fatalf("profiles = %v", m)
	}
	list := m["chrome_profiles"].([]any)
	if len(list) != 4 {
		t.Fatalf("want the three of Local State and the one only state names, got %v", list)
	}
	get := func(i int) map[string]any { return list[i].(map[string]any) }
	if get(0)["folder"] != "Default" || get(0)["name"] != "Person 1" || get(0)["last_used"] != false ||
		get(0)["in_chrome"] != true || len(get(0)["used_by_profiles"].([]any)) != 0 {
		t.Fatalf("Default = %v", get(0))
	}
	if get(1)["folder"] != "Profile 1" || get(1)["used_by_profiles"].([]any)[0] != "work" {
		t.Fatalf("Profile 1 = %v", get(1))
	}
	if get(2)["folder"] != "Profile 2" || get(2)["last_used"] != true ||
		get(2)["used_by_accounts"].([]any)[0] != "research" {
		t.Fatalf("Profile 2 = %v", get(2))
	}
	if get(3)["folder"] != "claudeswitch-personal" || get(3)["in_chrome"] != false || get(3)["name"] != nil {
		t.Fatalf("extra = %v", get(3))
	}
	if l := c2Launches(t); len(l) != 0 {
		t.Fatalf("profiles launched %q", l)
	}

	human, _, err := runChromeT(t, "profiles", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Person 1", "Work 2", "Chrome's last used", "profile work", "research"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human output lacks %q:\n%s", want, human)
		}
	}
}

func TestChromeProfilesWithoutLocalState(t *testing.T) {
	cfg := c2World(t, "darwin", "")
	out, _, err := runChromeT(t, "profiles", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	if m["local_state"] != false || m["last_used"] != nil || len(m["chrome_profiles"].([]any)) != 0 {
		t.Fatalf("profiles = %v", m)
	}
}

func TestChromeAddExistingMapsWithoutCreatingOrLaunching(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	out, _, err := runChromeT(t, "add", "work-team", "--existing", "Work 2", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	if m["account"] != "work-team" || m["profile_dir"] != "Profile 2" || m["name"] != "Work 2" ||
		m["created"] != false || m["existing"] != true || m["opened"] != false || m["email"] != "team@example.com" {
		t.Fatalf("add --existing = %v", m)
	}
	if l := c2Launches(t); len(l) != 0 {
		t.Fatalf("add --existing launched %q", l)
	}
	cp := c2State(t).ChromeOf("work-team")
	if cp == nil || cp.Dir != "Profile 2" || !cp.Existing {
		t.Fatalf("mapping = %+v", cp)
	}

	// By folder too, replacing the mapping.
	if _, _, err := runChromeT(t, "add", "work-team", "--existing", "Default", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if cp := c2State(t).ChromeOf("work-team"); cp == nil || cp.Dir != "Default" {
		t.Fatalf("mapping = %+v", cp)
	}

	_, _, err = runChromeT(t, "add", "work-team", "--existing", "Nope", "--config", cfg)
	ce := wantCode(t, err, "not_found")
	if !strings.Contains(ce.Hint, `"Work 2"`) {
		t.Fatalf("hint = %q", ce.Hint)
	}

	// --existing belongs to add alone.
	_, _, err = runChromeT(t, "work-team", "--existing", "Default", "--config", cfg)
	wantCode(t, err, "usage")
}

// Without --existing, add creates a profile as before, also for an account
// whose mapping was an existing profile.
func TestChromeAddWithoutExistingStillCreates(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	st := c2State(t)
	st.SetChromeExisting("work-1", "Profile 1", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "add", "work-1", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, []byte(out)); m["profile_dir"] != "claudeswitch-work-1" || m["created"] != true {
		t.Fatalf("add = %v", m)
	}
	cp := c2State(t).ChromeOf("work-1")
	if cp == nil || cp.Dir != "claudeswitch-work-1" || cp.Existing {
		t.Fatalf("mapping = %+v", cp)
	}
	if got := c2Launches(t); len(got) != 1 || got[0][4] != "--profile-directory=claudeswitch-work-1" {
		t.Fatalf("launches = %q", got)
	}
}

func TestChromeSigninOpensTheResolvedProfileAtTheSignInPages(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "signin", "work-team", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	if m["account"] != "work-team" || m["profile_dir"] != "Profile 1" || m["name"] != "Work" ||
		m["rule"] != "profile" || m["opened"] != true || m["email"] != "team@example.com" {
		t.Fatalf("signin = %v", m)
	}
	want := [][]string{{"open", "-na", "Google Chrome", "--args", "--profile-directory=Profile 1",
		chromeLoginURL, webStoreURL}}
	if got := c2Launches(t); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("launches = %q\nwant       %q", got, want)
	}

	human, _, err := runChromeT(t, "signin", "work-team", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "team@example.com") || !strings.Contains(human, `"Work"`) {
		t.Fatalf("steps lack the email or the profile:\n%s", human)
	}
	// No email recorded: the steps name the account.
	human, _, err = runChromeT(t, "signin", "work-1", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "work-1") {
		t.Fatalf("steps:\n%s", human)
	}

	_, _, err = runChromeT(t, "signin", "nobody", "--config", cfg)
	wantCode(t, err, "not_found")
}

func TestChromeSigninWithNothingToOpenRefuses(t *testing.T) {
	cfg := c2World(t, "darwin", "")
	_, _, err := runChromeT(t, "signin", "work-1", "--config", cfg)
	ce := wantCode(t, err, "not_found")
	if !strings.Contains(ce.Hint, "cs profile set work chrome") {
		t.Fatalf("hint = %q", ce.Hint)
	}
	if l := c2Launches(t); len(l) != 0 {
		t.Fatalf("launched %q", l)
	}
}

// The notice after a rotation in profile P to account A.
func TestChromeSwitchNotice(t *testing.T) {
	cfgPath := c2World(t, "darwin", c2LocalState)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st := c2State(t)
	local := readChromeLocal()

	// Nothing set up anywhere: silence, as before.
	if got := chromeSwitchNotice(cfg, st, local, "work", "work-1", "work-team"); got != "" {
		t.Fatalf("nothing set: %q", got)
	}
	// Only the profile's chrome applies: sign it in.
	cfg.Profiles[1].Chrome = "Profile 1"
	want := `Claude in Chrome in "Work" is still signed in as work-1; sign it in as work-team (cs chrome signin work-team)`
	if got := chromeSwitchNotice(cfg, st, local, "work", "work-1", "work-team"); got != want {
		t.Fatalf("profile rule:\n got %q\nwant %q", got, want)
	}
	if got := chromeSwitchNotice(cfg, st, local, "work", "work-team", "work-team"); got != "" {
		t.Fatalf("not a rotation: %q", got)
	}
	// Without Local State the folder names it.
	if got := chromeSwitchNotice(cfg, st, chromeLocal{}, "work", "work-1", "work-team"); !strings.Contains(got,
		`in "Profile 1" is still signed in as work-1`) {
		t.Fatalf("no Local State: %q", got)
	}
	// The account's own mapping keeps today's notice.
	st.SetChromeExisting("work-team", "Profile 2", time.Now())
	if got := chromeSwitchNotice(cfg, st, local, "work", "work-1", "work-team"); got !=
		"Claude in Chrome: use the work-team Chrome profile (cs chrome work-team)" {
		t.Fatalf("own mapping: %q", got)
	}
}

func TestDaemonSwitchAsksForASignInWhenOnlyTheProfileMaps(t *testing.T) {
	c2World(t, "darwin", c2LocalState)
	cfg := twoProfiles()
	cfg.Profiles[1].Chrome = "Profile 1"
	r := newRig(t, cfg, true)
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
	want := `Claude in Chrome in "Work" is still signed in as w1; sign it in as w2 (cs chrome signin w2)`
	var chrome []string
	for _, s := range r.nt.sent {
		if strings.Contains(s, "Claude in Chrome") {
			chrome = append(chrome, s)
		}
	}
	if len(chrome) != 1 || !strings.Contains(chrome[0], want) {
		t.Fatalf("notifications = %q, want one with %q", r.nt.sent, want)
	}
	if !strings.Contains(r.logs.String(), "cs chrome signin w2") {
		t.Errorf("log lacks the sign-in line:\n%s", r.logs.String())
	}
	if got := st.Profile("work").LastFrom; got != "w1" {
		t.Errorf("last_from = %q, want w1", got)
	}
}

func TestUseChromeNoteNamesTheSignIn(t *testing.T) {
	cfgPath := c2World(t, "darwin", c2LocalState)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[1].Chrome = "Profile 1"
	var out bytes.Buffer
	useChromeNoteIn(&out, cfg, c2State(t), "work", "work-1", "work-team")
	if !strings.Contains(out.String(), `Claude in Chrome in "Work" is still signed in as work-1; sign it in as work-team (cs chrome signin work-team)`) {
		t.Fatalf("use note = %q", out.String())
	}
}

// `chrome list --json` (the app polls it) carries, per profile, the live
// account's resolved Chrome profile and the rotation that put it there.
func TestChromeListJSONCarriesEachLiveResolution(t *testing.T) {
	cfg := c2World(t, "darwin", c2LocalState)
	if err := profileSet(&bytes.Buffer{}, cfg, "work", "chrome", "Work", true); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)
	st := c2State(t)
	ws := st.Profile("work")
	ws.SetActive("work-team")
	ws.LastSwitch, ws.LastFrom = at, "work-1"
	st.Profile("default").SetActive("personal")
	st.SetChromeExisting("personal", "Default", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runChromeT(t, "list", "--json", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, []byte(out))
	live := m["live"].([]any)
	if len(live) != 2 {
		t.Fatalf("live = %v", live)
	}
	def, work := live[0].(map[string]any), live[1].(map[string]any)
	if work["profile"] != "work" || work["account"] != "work-team" || work["profile_dir"] != "Profile 1" ||
		work["name"] != "Work" || work["rule"] != "profile" || work["last_from"] != "work-1" ||
		work["last_switch"] != "2026-10-08T11:00:00Z" {
		t.Fatalf("work = %v", work)
	}
	if def["rule"] != "account" || def["profile_dir"] != "Default" || def["last_from"] != nil ||
		def["last_switch"] != nil {
		t.Fatalf("default = %v", def)
	}
	entries := m["chrome_profiles"].([]any)
	if e := entries[0].(map[string]any); e["account"] != "personal" || e["existing"] != true || e["name"] != "Person 1" {
		t.Fatalf("entry = %v", e)
	}
}
