package main

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// Shared pools (docs/PROFILES.md §10, D31-D35): the CLI side. `cs pool`
// edits [[pool]] blocks, `cs profile pools` sets what a profile lists, and
// every account JSON names its pool and the profiles that may use it.

const sharedTOML = `# shared pools, hand-written
priority = ["p1", "t1", "t2", "s1", "loose"]

[[account]]
id = "p1"
[[account]]
id = "t1"
[[account]]
id = "t2"
[[account]]
id = "s1"
[[account]]
id = "loose"

# the one both profiles use
[[pool]]
name     = "team"
accounts = ["t1", "t2"]

[[pool]]
name     = "mine"
accounts = ["p1"]

[[profile]]
name  = "default"
pools = ["mine", "team"]

# the work laptop
[[profile]]
name  = "work"
dir   = "~/.claude-work"
pools = ["team"]

[[profile]]
name = "solo"
dir  = "~/.claude-solo"
pool = ["s1"]
`

// liveAt makes accountLiveIn report id live in profile (and nothing else).
func liveAt(id, profile string) {
	accountLiveIn = func(_ *config.Config, _ *state.State, got string, consider func(string, bool) bool) (string, bool) {
		if got == id && consider(profile, false) {
			return profile, true
		}
		return "", false
	}
}

// noLiveLookup keeps profile JSON off the keychain: no profile's live item
// is looked up.
func noLiveLookup(t *testing.T) {
	t.Helper()
	old := cliLiveFor
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return nil, errors.New("not in tests") }
	t.Cleanup(func() { cliLiveFor = old })
}

// poolsOf is a pool list answer's pools, by name.
func poolsOf(t *testing.T, m map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	list, ok := m["pools"].([]any)
	if !ok {
		t.Fatalf("no pools list: %v", m)
	}
	for _, p := range list {
		pm := p.(map[string]any)
		out[pm["name"].(string)] = pm
	}
	return out
}

func strs(v any) []string {
	out := []string{}
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestPoolListShowsEveryPoolItsAccountsAndWhoUsesIt(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := poolList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if names := func() []string {
		var n []string
		for _, p := range m["pools"].([]any) {
			n = append(n, p.(map[string]any)["name"].(string))
		}
		return n
	}(); !reflect.DeepEqual(names, []string{"team", "mine", "solo"}) {
		t.Errorf("pools in order %v, want the [[pool]] blocks then the profiles' own", names)
	}
	ps := poolsOf(t, m)
	if p := ps["team"]; !reflect.DeepEqual(strs(p["accounts"]), []string{"t1", "t2"}) ||
		!reflect.DeepEqual(strs(p["profiles"]), []string{"default", "work"}) || p["own"] != false {
		t.Errorf("team: %v", p)
	}
	if p := ps["solo"]; !reflect.DeepEqual(strs(p["accounts"]), []string{"s1"}) ||
		!reflect.DeepEqual(strs(p["profiles"]), []string{"solo"}) || p["own"] != true {
		t.Errorf("solo's own pool (D35): %v", p)
	}
	if got := strs(m["unlisted"]); !reflect.DeepEqual(got, []string{"loose"}) {
		t.Errorf("unlisted %v, want the account no pool lists (D6)", got)
	}

	buf.Reset()
	if err := poolList(&buf, path, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"team", "t1, t2", "default, work", "solo", "loose"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the human list lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestPoolCreateAddMoveRemoveAndDelete(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := poolCreate(&buf, path, "spare", []string{"loose"}, true); err != nil {
		t.Fatal(err)
	}
	ps := poolsOf(t, decodeJSON(t, buf.Bytes()))
	if p := ps["spare"]; p == nil || !reflect.DeepEqual(strs(p["accounts"]), []string{"loose"}) ||
		len(strs(p["profiles"])) != 0 {
		t.Fatalf("spare after create: %v", p)
	}
	if got, _ := loadOrFail(t, path).PoolOf("loose"); got != "spare" {
		t.Fatalf("loose is in pool %q", got)
	}

	// D32: one pool per account.
	err := poolCreate(&buf, path, "dup", []string{"t1"}, true)
	if got := errCode(t, err); got != codeInOtherPool || !strings.Contains(err.Error(), `"team"`) {
		t.Errorf("an account already in a pool: %q, %v", got, err)
	}
	if got := errCode(t, poolCreate(&buf, path, "team", nil, true)); got != codeInvalidValue {
		t.Errorf("a pool name taken: %q", got)
	}
	if got := errCode(t, poolCreate(&buf, path, "solo", nil, true)); got != codeInvalidValue {
		t.Errorf("a name a profile's own pool has: %q", got)
	}
	if got := errCode(t, poolCreate(&buf, path, "x", []string{"nobody"}, true)); got != codeNotFound {
		t.Errorf("an unknown account: %q", got)
	}
	err = poolAdd(&buf, path, "spare", "t1", true)
	if got := errCode(t, err); got != codeInOtherPool || !strings.Contains(err.Error(), `"team"`) ||
		!strings.Contains(err.Error(), "pool move t1 --to spare") {
		t.Errorf("add of an account another pool lists: %q, %v", got, err)
	}

	buf.Reset()
	if err := poolMove(&buf, path, "t1", "spare", true); err != nil {
		t.Fatal(err)
	}
	ps = poolsOf(t, decodeJSON(t, buf.Bytes()))
	if !reflect.DeepEqual(strs(ps["spare"]["accounts"]), []string{"loose", "t1"}) ||
		!reflect.DeepEqual(strs(ps["team"]["accounts"]), []string{"t2"}) {
		t.Errorf("after the move: spare %v, team %v", ps["spare"]["accounts"], ps["team"]["accounts"])
	}

	buf.Reset()
	if err := poolRemove(&buf, path, "spare", "t1", true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if _, listed := cfg.PoolOf("t1"); listed || !cfg.InPool("default", "t1") {
		t.Errorf("a removed account is in no pool and joins default (D6): listed %v, default %v",
			listed, cfg.InPool("default", "t1"))
	}
	if got := errCode(t, poolRemove(&buf, path, "spare", "t1", true)); got != codeNotFound {
		t.Errorf("remove of an account the pool does not list: %q", got)
	}

	// Own pools are addressed by their profile's name.
	buf.Reset()
	if err := poolAdd(&buf, path, "solo", "t1", true); err != nil {
		t.Fatal(err)
	}
	if in, _ := loadOrFail(t, path).ProfileNamed("solo"); !reflect.DeepEqual(in.Pool, []string{"s1", "t1"}) {
		t.Errorf("solo's own pool %v", in.Pool)
	}

	// A pool profiles list, and a profile's own pool, are deletable since
	// D38; TestPoolDelete* cover them.
	buf.Reset()
	if err := poolDelete(&buf, path, "spare", true); err != nil {
		t.Fatal(err)
	}
	if ps := poolsOf(t, decodeJSON(t, buf.Bytes())); ps["spare"] != nil {
		t.Error("spare is still listed")
	}
	cfg = loadOrFail(t, path)
	if _, ok := cfg.PoolNamed("spare"); ok {
		t.Error("the [[pool]] block stayed")
	}
	if _, listed := cfg.PoolOf("loose"); listed {
		t.Error("a deleted pool's accounts are in no pool")
	}
	text := readFile(t, path)
	for _, keep := range []string{"# shared pools, hand-written", "# the one both profiles use", "# the work laptop"} {
		if !strings.Contains(text, keep) {
			t.Errorf("comment %q lost:\n%s", keep, text)
		}
	}
	if got := errCode(t, poolDelete(&buf, path, "nosuch", true)); got != codeNotFound {
		t.Errorf("delete of no pool: %q", got)
	}
}

// With no default profile an account in no pool is a config error (D6), so
// taking it out of its pool is would_orphan, as `profile pool remove` is.
func TestPoolRemoveWithNoDefaultIsWouldOrphan(t *testing.T) {
	path := appWorld(t, strings.Replace(strings.Replace(sharedTOML, `name  = "default"`, `name  = "home"`, 1),
		`accounts = ["p1"]`, `accounts = ["p1", "loose"]`, 1))
	before := readFile(t, path)
	var buf bytes.Buffer
	err := poolRemove(&buf, path, "team", "t1", true)
	if got := errCode(t, err); got != codeWouldOrphan || !strings.Contains(err.Error(), "pool move t1 --to") {
		t.Errorf("no default to fall back to: %q, %v", got, err)
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
}

// §3 for pool edits: an account a profile loses must not be live there. A
// profile that comes to share it while another holds it is fine (D34).
func TestPoolEditsRefuseTakingAnAccountFromTheProfileItIsLiveIn(t *testing.T) {
	path := appWorld(t, sharedTOML)
	noLiveLookup(t)
	var buf bytes.Buffer
	liveAt("t1", "work")
	if err := poolCreate(&buf, path, "spare", nil, true); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)
	err := poolMove(&buf, path, "t1", "spare", true)
	if got := errCode(t, err); got != codeLive || !strings.Contains(err.Error(), `"work"`) {
		t.Errorf("move off the profile it is live in: %q, %v", got, err)
	}
	if got := errCode(t, poolRemove(&buf, path, "team", "t1", true)); got != codeLive {
		t.Errorf("remove from the profile it is live in: %q", got)
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
	// Sharing: solo comes to use team while work holds t1. Only one profile
	// at a time uses it (D34), so nothing is refused.
	buf.Reset()
	if err := profilePools(&buf, path, "solo", []string{"team"}, true); err != nil {
		t.Fatalf("subscribing to a pool whose account is live in another subscriber: %v", err)
	}
}

func TestProfilePoolsConvertsAnOwnPoolAndSetsTheOrder(t *testing.T) {
	path := appWorld(t, sharedTOML)
	noLiveLookup(t)
	var buf bytes.Buffer
	if err := profilePools(&buf, path, "solo", []string{"team"}, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["name"] != "solo" || !reflect.DeepEqual(strs(m["pools"]), []string{"solo", "team"}) ||
		!reflect.DeepEqual(strs(m["pool"]), []string{"s1", "t1", "t2"}) {
		t.Errorf("answered %v; want solo's own pool converted and leading", m)
	}
	cfg := loadOrFail(t, path)
	p, ok := cfg.PoolNamed("solo")
	if !ok || !reflect.DeepEqual(p.Accounts, []string{"s1"}) {
		t.Fatalf("no [[pool]] solo holding s1: %+v", cfg.Pools)
	}
	in := cfg.Profiles[declaredIndex(cfg, "solo")]
	if len(in.Pool) != 0 || !reflect.DeepEqual(in.Pools, []string{"solo", "team"}) {
		t.Errorf("solo's block: pool %v pools %v", in.Pool, in.Pools)
	}
	if got := cfg.ProfilesOf("t1"); !reflect.DeepEqual(got, []string{"default", "work", "solo"}) {
		t.Errorf("t1 is used by %v", got)
	}
	if text := readFile(t, path); !strings.Contains(text, "dir  = \"~/.claude-solo\"\npools = [\"solo\", \"team\"]\n") {
		t.Errorf("pools did not take the pool line's place:\n%s", text)
	}

	// The list says the order, the converted pool included.
	buf.Reset()
	if err := profilePools(&buf, path, "solo", []string{"team", "solo"}, true); err != nil {
		t.Fatal(err)
	}
	if in, _ := loadOrFail(t, path).ProfileNamed("solo"); !reflect.DeepEqual(in.PoolNames, []string{"team", "solo"}) {
		t.Errorf("solo's pools %v, want team then solo", in.PoolNames)
	}

	// Naming another profile's own pool converts that one too, and both
	// profiles then list it.
	path = appWorld(t, sharedTOML)
	buf.Reset()
	if err := profilePools(&buf, path, "work", []string{"team", "solo"}, true); err != nil {
		t.Fatal(err)
	}
	cfg = loadOrFail(t, path)
	if got := cfg.ProfilesOf("s1"); !reflect.DeepEqual(got, []string{"work", "solo"}) {
		t.Errorf("s1 is used by %v, want work and solo", got)
	}
	if in, _ := cfg.ProfileNamed("solo"); !reflect.DeepEqual(in.PoolNames, []string{"solo"}) {
		t.Errorf("solo after its pool was shared: %v", in.PoolNames)
	}
	if !strings.Contains(readFile(t, path), "# the work laptop") {
		t.Error("a comment went")
	}

	for name, c := range map[string]struct {
		profile string
		pools   []string
		code    string
	}{
		"unknown pool":    {"work", []string{"nosuch"}, codeNotFound},
		"named twice":     {"work", []string{"team", "team"}, codeInvalidValue},
		"unknown profile": {"nobody", []string{"team"}, codeNotFound},
		"no pools":        {"work", nil, codeUsage},
	} {
		if got := errCode(t, profilePools(&buf, path, c.profile, c.pools, true)); got != c.code {
			t.Errorf("%s: %q, want %q", name, got, c.code)
		}
	}
}

// `profile pool` keeps working on a profile's own pool; one that lists pools
// is pointed to `cs pool`.
func TestProfilePoolOnAProfileThatListsPools(t *testing.T) {
	path := appWorld(t, sharedTOML)
	before := readFile(t, path)
	var buf bytes.Buffer
	err := profilePool(&buf, path, "work", "add", "loose", "", true)
	if got := errCode(t, err); got != codeUsage || !strings.Contains(err.Error(), "claudeswitch pool add") {
		t.Errorf("a profile that lists pools: %q, %v", got, err)
	}
	err = profilePool(&buf, path, "solo", "add", "t1", "", true)
	if got := errCode(t, err); got != codeInOtherPool || !strings.Contains(err.Error(), `"team"`) {
		t.Errorf("an account in a [[pool]]: %q, %v", got, err)
	}
	if err := profilePool(&buf, path, "solo", "remove", "s1", "work", true); errCode(t, err) != codeUsage {
		t.Errorf("--to a profile that lists pools: %v", err)
	}
	if readFile(t, path) != before {
		t.Error("a refusal changed the config")
	}
	buf.Reset()
	if err := profilePool(&buf, path, "solo", "add", "loose", "", true); err != nil {
		t.Fatal(err)
	}
	if in, _ := loadOrFail(t, path).ProfileNamed("solo"); !reflect.DeepEqual(in.Pool, []string{"s1", "loose"}) {
		t.Errorf("solo's own pool %v", in.Pool)
	}
}

func TestAccountJSONNamesItsPoolAndEveryProfileThatMayUseIt(t *testing.T) {
	path := appWorld(t, sharedTOML)
	cfg := loadOrFail(t, path)
	st, _ := state.Load("", cfg.ProfileNames()...)
	st.Profile("work").SetActive("t1")
	now := time.Now()
	for id, want := range map[string]struct {
		pool     any
		profiles []string
		profile  any
	}{
		"t1":    {"team", []string{"default", "work"}, "default"},
		"s1":    {"solo", []string{"solo"}, "solo"},
		"loose": {nil, []string{"default"}, "default"},
	} {
		m := accountJSON(cfg, st, accountByID(cfg, id), now)
		if m["pool"] != want.pool || !reflect.DeepEqual(m["profiles"], want.profiles) || m["profile"] != want.profile {
			t.Errorf("account list %s: pool %v profiles %v profile %v", id, m["pool"], m["profiles"], m["profile"])
		}
		s := map[string]any{}
		addProfileJSON(s, cfg, st, id)
		if s["pool"] != want.pool || !reflect.DeepEqual(s["profiles"], want.profiles) || s["profile"] != want.profile {
			t.Errorf("status %s: %v", id, s)
		}
	}
	if m := accountJSON(cfg, st, accountByID(cfg, "t1"), now); m["active_in"] != "work" {
		t.Errorf("active_in %v", m["active_in"])
	}
	v := vaultedAccountJSON(path, "t1", gotSeat("u1", "o1"), false)
	if !reflect.DeepEqual(v["profiles"], []string{"default", "work"}) || v["profile"] != "default" {
		t.Errorf("login --code: %v", v)
	}
	var buf bytes.Buffer
	if err := historyUsage(&buf, path, 30, true, now); err != nil {
		t.Fatal(err)
	}
	for _, a := range decodeJSON(t, buf.Bytes())["accounts"].([]any) {
		if am := a.(map[string]any); am["id"] == "t1" &&
			(am["pool"] != "team" || !reflect.DeepEqual(strs(am["profiles"]), []string{"default", "work"})) {
			t.Errorf("history --usage: %v", am)
		}
	}
}

func TestProfileListJSONGivesEachProfilesPoolsAndThePools(t *testing.T) {
	path := appWorld(t, sharedTOML)
	noLiveLookup(t)
	var buf bytes.Buffer
	if err := profileListJSON(&buf, path); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	want := map[string][]string{"default": {"mine", "team"}, "work": {"team"}, "solo": {"solo"}}
	for _, p := range m["profiles"].([]any) {
		pm := p.(map[string]any)
		name := pm["name"].(string)
		if got := strs(pm["pools"]); !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s lists %v, want %v", name, got, want[name])
		}
		if name == "work" && !reflect.DeepEqual(strs(pm["listed"]), []string{"t1", "t2"}) {
			t.Errorf("work's listed %v: a pool it lists is listed, not D6", pm["listed"])
		}
	}
	var list bytes.Buffer
	if err := poolList(&list, path, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m["pools"], decodeJSON(t, list.Bytes())["pools"]) {
		t.Errorf("profile list's pools %v differ from pool list's", m["pools"])
	}
}

// D5's refusal names every profile that may use the account.
func TestOutsidePoolNamesEveryProfileThatMayUseIt(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, sharedTOML))
	solo, _ := cfg.ProfileNamed("solo")
	err := refuseOutsidePool(cfg, solo, "t1")
	if got := errCode(t, err); got != codeOutsidePool ||
		!strings.Contains(err.Error(), `"default"`) || !strings.Contains(err.Error(), `"work"`) {
		t.Errorf("%q, %v", got, err)
	}
	work, _ := cfg.ProfileNamed("work")
	if err := refuseOutsidePool(cfg, work, "t1"); err != nil {
		t.Errorf("t1 is in work's pools: %v", err)
	}
}

// D20 with named pools: a new account joining a profile that lists pools
// goes into the first one it lists.
func TestANewAccountJoinsTheFirstPoolItsProfileLists(t *testing.T) {
	path := writeConfig(t, sharedTOML)
	msg, err := recordSeat(loadOrFail(t, path), "t3", gotSeat("p3", "o3"), "work")
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if p, _ := cfg.PoolOf("t3"); p != "team" || !cfg.InPool("work", "t3") {
		t.Errorf("t3 is in pool %q", p)
	}
	if !strings.Contains(msg, `"team"`) || !strings.Contains(msg, `"work"`) {
		t.Errorf("the message must name the pool and the profile: %q", msg)
	}
	if !strings.Contains(newAccountNotice(cfg, cfg.EffectiveProfiles()[1], "t4"), `"team"`) {
		t.Error("login's notice must name the pool a new account joins")
	}
	// A profile with its own pool keeps D20 as it was.
	if _, err := recordSeat(loadOrFail(t, path), "s2", gotSeat("p4", "o4"), "solo"); err != nil {
		t.Fatal(err)
	}
	if in, _ := loadOrFail(t, path).ProfileNamed("solo"); !reflect.DeepEqual(in.Pool, []string{"s1", "s2"}) {
		t.Errorf("solo's own pool %v", in.Pool)
	}
	if !strings.Contains(readFile(t, path), "# the one both profiles use") {
		t.Error("a comment went")
	}
}

// Rename reaches the [[pool]] lists, and the pools and profiles an account is
// in are the same after it.
func TestRenameRenamesInsideAPool(t *testing.T) {
	path := writeConfig(t, strings.Replace(sharedTOML, `accounts = ["t1", "t2"]`,
		"accounts = [\n  \"t1\", # first\n  \"t2\",\n]", 1))
	if err := renameInConfig(loadOrFail(t, path), "t1", "t9"); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if p, _ := cfg.PoolNamed("team"); !reflect.DeepEqual(p.Accounts, []string{"t9", "t2"}) {
		t.Errorf("team after the rename: %v", p.Accounts)
	}
	if got := cfg.ProfilesOf("t9"); !reflect.DeepEqual(got, []string{"default", "work"}) {
		t.Errorf("t9 is used by %v", got)
	}
}

// Delete drops the account from its [[pool]] too, or the config would name
// an account it no longer has.
func TestAccountDeleteDropsItFromItsPool(t *testing.T) {
	path := appWorld(t, sharedTOML)
	acctSeams.has = func(id string) bool { return false }
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "t1", true, true); err != nil {
		t.Fatal(err)
	}
	if rm := decodeJSON(t, buf.Bytes())["removed"].(map[string]any); rm["pool"] != "team" {
		t.Errorf("removed.pool %v, want team", rm["pool"])
	}
	if p, _ := loadOrFail(t, path).PoolNamed("team"); !reflect.DeepEqual(p.Accounts, []string{"t2"}) {
		t.Errorf("team after the delete: %v", p.Accounts)
	}
}

// Removing a profile moves only its own accounts; the pools it listed stay,
// for whoever else lists them. Into a profile that lists pools, they join its
// first one (as D20).
func TestProfileRemoveLeavesNamedPoolsAndMovesItsOwnAccounts(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := profileRemove(&buf, path, "work", "", true, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if moved := m["moved"].([]any); len(moved) != 0 {
		t.Errorf("moved %v: work had no accounts of its own", moved)
	}
	cfg := loadOrFail(t, path)
	if p, _ := cfg.PoolNamed("team"); !reflect.DeepEqual(p.Accounts, []string{"t1", "t2"}) ||
		!reflect.DeepEqual(cfg.ProfilesOf("t1"), []string{"default"}) {
		t.Errorf("team after work went: %v, used by %v", p.Accounts, cfg.ProfilesOf("t1"))
	}

	path = appWorld(t, sharedTOML)
	buf.Reset()
	if err := profileRemove(&buf, path, "solo", "work", true, true); err != nil {
		t.Fatal(err)
	}
	cfg = loadOrFail(t, path)
	if p, _ := cfg.PoolOf("s1"); p != "team" || !cfg.InPool("work", "s1") {
		t.Errorf("s1 is in pool %q", p)
	}

	// A [[pool]] named after its profile (an own pool converted) is a pool
	// like any other: it stays, and the output says nobody lists it now.
	path = appWorld(t, sharedTOML)
	noLiveLookup(t)
	if err := profilePools(&bytes.Buffer{}, path, "solo", []string{"team"}, true); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := profileRemove(&buf, path, "solo", "", true, false); err != nil {
		t.Fatal(err)
	}
	if p, _ := loadOrFail(t, path).PoolOf("s1"); p != "solo" {
		t.Errorf("s1 is in pool %q, want it left in solo", p)
	}
	if !strings.Contains(buf.String(), "pool solo is listed by no profile") {
		t.Errorf("the output does not name the pool left unlisted:\n%s", buf.String())
	}
}

// account pin acts on the profile the account is live in, or --profile.
func TestPinOfASharedAccountActsWhereItIsLive(t *testing.T) {
	path := appWorld(t, sharedTOML)
	st, _ := state.Load("", "default", "work", "solo")
	st.Get("t1")
	st.Profile("work").SetActive("t1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountPin(&buf, path, "t1", "", false, true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["profile"] != "work" {
		t.Errorf("pinned in %v, want work, where it is live", m["profile"])
	}
	err := accountPin(&buf, path, "t1", "default", false, true)
	if got := errCode(t, err); got != codeNotActive || !strings.Contains(err.Error(), "default") {
		t.Errorf("--profile default, where it is not live: %q, %v", got, err)
	}
	if got := errCode(t, accountPin(&buf, path, "t2", "", false, true)); got != codeNotActive {
		t.Errorf("live nowhere: %q", got)
	}
	if got := errCode(t, accountPin(&buf, path, "t1", "nobody", false, true)); got != codeNotFound {
		t.Errorf("an unknown --profile: %q", got)
	}
}

// Chrome judges an account in no profile it is live in by this shell's
// profile when that one may use it, else the first that may.
func TestChromeProfileForASharedAccount(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, sharedTOML))
	st := &state.State{Profiles: map[string]*state.ProfileState{}}
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	if got := chromeProfileFor(cfg, st, "t1"); got != "default" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "~/.claude-work")
	if got := chromeProfileFor(cfg, st, "t1"); got != "work" {
		t.Errorf("in work's shell: %q", got)
	}
	if got := chromeProfileFor(cfg, st, "s1"); got != "solo" {
		t.Errorf("an account work may not use: %q", got)
	}
	st.Profiles["default"] = &state.ProfileState{Active: "t1"}
	if got := chromeProfileFor(cfg, st, "t1"); got != "default" {
		t.Errorf("live in default: %q", got)
	}
}

// `add --from S --profile T` of an account both may use: S does not move off
// it because of the add.
func TestCrossProfileNoteForASharedAccount(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, sharedTOML))
	if got := crossProfileNote(cfg, "work", "default", "t1"); strings.Contains(got, "will move off") ||
		!strings.Contains(got, "work") || !strings.Contains(got, "default") {
		t.Errorf("shared: %q", got)
	}
	if got := crossProfileNote(cfg, "solo", "default", "t1"); !strings.Contains(got, "solo will move off it") {
		t.Errorf("not shared: %q", got)
	}
	if got := crossProfileNote(cfg, "work", "work", "t1"); got != "" {
		t.Errorf("same profile: %q", got)
	}
}

// profile create --pool still makes the profile's own pool, and an account
// in a pool is refused (D32), with the way to share that pool instead.
func TestProfileCreateRefusesAnAccountInANamedPool(t *testing.T) {
	createHome(t)
	path := writeConfig(t, sharedTOML)
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "lab", pool: []string{"t1"}})
	if got := errCode(t, err); got != codeInOtherPool || !strings.Contains(err.Error(), `"team"`) ||
		!strings.Contains(err.Error(), "profile pools lab team") {
		t.Errorf("%q, %v", got, err)
	}
}

// The why view never shows an account live in another profile as ready.
func TestWhyJSONShowsAnAccountLiveElsewhereAsInUse(t *testing.T) {
	cfg := loadOrFail(t, appWorld(t, sharedTOML))
	st, _ := state.Load("", cfg.ProfileNames()...)
	st.Profile("default").SetActive("p1")
	st.Profile("work").SetActive("t1")
	m := whyJSON(cfg, st, time.Now(), "default")
	for _, p := range m["profiles"].([]map[string]any) {
		for _, a := range p["accounts"].([]map[string]any) {
			if a["id"] != "t1" {
				continue
			}
			if a["eligible"] != false || !strings.Contains(a["why"].(string), "in use by work") ||
				a["active_in"] != "work" {
				t.Errorf("t1 in default: %v", a)
			}
		}
	}
	if !slices.Contains(cfg.ProfilesOf("t1"), "default") {
		t.Fatal("setup: default may use t1")
	}
}

func TestPoolThroughTheDispatcher(t *testing.T) {
	path := appWorld(t, sharedTOML)
	out := captureStdout(t, func() error { return cmdPool([]string{"list", "--config", path, "--json"}) })
	if ps := poolsOf(t, decodeJSON(t, []byte(out))); ps["team"] == nil {
		t.Errorf("list: %s", out)
	}
	out = captureStdout(t, func() error {
		return cmdPool([]string{"create", "spare", "loose", "--config", path, "--json"})
	})
	if ps := poolsOf(t, decodeJSON(t, []byte(out))); ps["spare"] == nil {
		t.Errorf("create: %s", out)
	}
	out = captureStdout(t, func() error {
		return cmdPool([]string{"move", "loose", "--to", "team", "--config", path, "--json"})
	})
	if ps := poolsOf(t, decodeJSON(t, []byte(out))); !slices.Contains(strs(ps["team"]["accounts"]), "loose") {
		t.Errorf("move: %s", out)
	}
	if got := errCode(t, cmdPool([]string{"move", "loose", "--config", path, "--json"})); got != codeUsage {
		t.Errorf("move without --to: %q", got)
	}
	if got := errCode(t, cmdPool([]string{"nosuch", "--config", path})); got != codeUsage {
		t.Errorf("unknown verb: %q", got)
	}
	if got := errCode(t, cmdPool(nil)); got != codeUsage {
		t.Errorf("no verb: %q", got)
	}
	noLiveLookup(t)
	out = captureStdout(t, func() error {
		return cmdProfile([]string{"pools", "solo", "team", "--config", path, "--json"})
	})
	if m := decodeJSON(t, []byte(out)); m["name"] != "solo" {
		t.Errorf("profile pools: %s", out)
	}
}

// `why` and `status` decide as the daemon decides (D34): an account state
// records live in another profile is not a candidate, so the decision they
// show is the one the daemon will take.
func TestWhyDecidesWithoutAccountsInUseElsewhere(t *testing.T) {
	cfg := loadOrFail(t, appWorld(t, sharedTOML))
	st, _ := state.Load("", cfg.ProfileNames()...)
	st.Profile("default").SetActive("p1")
	st.Profile("work").SetActive("t1")
	for _, v := range profileViews(cfg, st, "default") {
		if got := v.input(st, time.Now()).Unavailable["t1"]; got != "in use by work" {
			t.Errorf("default's policy input passes over t1 as %q, want \"in use by work\"", got)
		}
	}
}

// A reload that changes which pools a profile uses says so: the daemon
// adopted the shared pools on 2026-10-20 without a line in its log, since
// only accounts, priority and global settings were described.
func TestReloadDescribesAProfilesPools(t *testing.T) {
	before := loadOrFail(t, writeConfig(t, strings.Replace(sharedTOML, `pools = ["team"]`, `pools = ["team", "mine"]`, 1)))
	after := loadOrFail(t, writeConfig(t, sharedTOML))
	got := strings.Join(configChanges(before, after), "; ")
	if !strings.Contains(got, "profile work") || !strings.Contains(got, "team (t1, t2)") ||
		!strings.Contains(got, "mine (p1)") {
		t.Errorf("changes %q do not describe work's pools before and after", got)
	}
	if strings.Contains(got, "profile default") {
		t.Errorf("changes %q describe default, whose pools did not change", got)
	}
}

// D37: a new account goes into the pool chosen: a [[pool]], a profile's own
// pool by its profile's name, or none.
func TestTheChosenPoolResolves(t *testing.T) {
	cfg := loadOrFail(t, appWorld(t, sharedTOML))
	for _, c := range []struct {
		pool   string
		none   bool
		want   joinTarget
		errHas string
	}{
		{"mine", false, joinTarget{Pool: "mine"}, ""},
		{"solo", false, joinTarget{Pool: "solo"}, ""},
		{"", true, joinTarget{None: true}, ""},
		{"nowhere", false, joinTarget{}, "nowhere"},
		{"mine", true, joinTarget{}, "--no-pool"},
	} {
		got, err := resolveJoin(cfg, c.pool, c.none)
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("%q/%v: got %+v, %v; want an error naming %s", c.pool, c.none, got, err, c.errHas)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q/%v: got %+v, %v; want %+v", c.pool, c.none, got, err, c.want)
		}
	}
	noDefault := loadOrFail(t, writeConfig(t, strings.Replace(
		strings.Replace(sharedTOML, `name  = "default"`, `name  = "home"`, 1),
		"id = \"loose\"", "id = \"loose\"\nenabled = false", 1)))
	if _, err := resolveJoin(noDefault, "", true); err == nil {
		t.Error("--no-pool with no default profile leaves the account rotating nowhere; want a refusal")
	}
}

func TestANewAccountLandsInThePoolChosen(t *testing.T) {
	for _, c := range []struct {
		join   joinTarget
		want   string
		listed bool
	}{
		{joinTarget{Pool: "mine"}, "mine", true},
		{joinTarget{Pool: "solo"}, "solo", true},
		{joinTarget{None: true}, "", false},
	} {
		cfg := loadOrFail(t, appWorld(t, sharedTOML))
		if _, err := recordSeatTo(cfg, "n1", gotSeat("pn", "on"), c.join); err != nil {
			t.Fatalf("%+v: %v", c.join, err)
		}
		got, listed := loadOrFail(t, cfg.Path).PoolOf("n1")
		if got != c.want || listed != c.listed {
			t.Errorf("%+v: n1 in pool %q (listed %v), want %q", c.join, got, listed, c.want)
		}
	}
}

func TestDirectLoginRemembersThePoolChosen(t *testing.T) {
	path := appWorld(t, sharedTOML)
	_, pend := directURL(t, "n1", "--config", path, "--pool", "mine")
	if pend["join_pool"] != "mine" {
		t.Errorf("pending %v, want join_pool mine", pend)
	}
	_, p, err := oauth.LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	if got := completionJoin(loadOrFail(t, path), p); got != (joinTarget{Pool: "mine"}) {
		t.Errorf("completion joins %+v, want pool mine", got)
	}
	err = cmdLogin([]string{"n2", "--config", path, "--pool", "mine", "--profile", "work", "--direct", "--json", "--no-open"})
	if got := errCode(t, err); got != codeUsage {
		t.Errorf("--pool with --profile: code %q, want usage", got)
	}
}

// The answer to an add or a login names the pool the account went into.
func TestTheVaultedAnswerNamesTheAccountsPool(t *testing.T) {
	path := appWorld(t, sharedTOML)
	if m := vaultedAccountJSON(path, "p1", &vault.Entry{AccountID: "p1"}, false); m["pool_name"] != "mine" {
		t.Errorf("pool_name %v, want mine", m["pool_name"])
	}
	if m := vaultedAccountJSON(path, "loose", &vault.Entry{AccountID: "loose"}, false); m["pool_name"] != nil {
		t.Errorf("pool_name %v for an account in no pool, want null", m["pool_name"])
	}
}

// D39: any pool can be renamed: a [[pool]] in place, with every profile's
// listing of it; a profile's own pool becomes a [[pool]] it lists.
func TestPoolRename(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := poolRename(&buf, path, "team", "crew", true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if p, ok := cfg.PoolNamed("crew"); !ok || !reflect.DeepEqual(p.Accounts, []string{"t1", "t2"}) {
		t.Fatalf("crew: %+v %v", p, ok)
	}
	if _, ok := cfg.PoolNamed("team"); ok {
		t.Error("team is still there")
	}
	if !reflect.DeepEqual(cfg.SubscribersOf("crew"), []string{"default", "work"}) {
		t.Errorf("crew is used by %v, want both profiles", cfg.SubscribersOf("crew"))
	}
	if d, _ := cfg.ProfileNamed("default"); !reflect.DeepEqual(d.PoolNames, []string{"mine", "crew", ""}) {
		t.Errorf("default's pools %v: the order must not change", d.PoolNames)
	}
	if raw := readFile(t, path); !strings.Contains(raw, "# the one both profiles use") {
		t.Error("the comment above the pool was lost: rename in place")
	}

	if err := poolRename(&buf, path, "solo", "lab", true); err != nil {
		t.Fatal(err)
	}
	cfg = loadOrFail(t, path)
	solo := cfg.Profiles[declaredIndex(cfg, "solo")]
	if p, ok := cfg.PoolNamed("lab"); !ok || !reflect.DeepEqual(p.Accounts, []string{"s1"}) ||
		len(solo.Pool) != 0 || !reflect.DeepEqual(solo.Pools, []string{"lab"}) {
		t.Errorf("solo's own pool renamed: lab %+v, solo pool %v pools %v", p, solo.Pool, solo.Pools)
	}

	for _, c := range []struct{ from, to, code string }{
		{"crew", "mine", codeInvalidValue},
		{"crew", "a b", codeInvalidValue},
		{"nowhere", "x", codeNotFound},
	} {
		if got := errCode(t, poolRename(&buf, path, c.from, c.to, true)); got != c.code {
			t.Errorf("rename %s → %s: %q, want %q", c.from, c.to, got, c.code)
		}
	}
}

// D38: any pool can be deleted, its accounts moved where chosen, the
// profiles that listed it no longer listing it.
func TestPoolDeleteMovesItsAccountsAndUnlistsIt(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := poolDeleteTo(&buf, path, "team", joinTarget{Pool: "mine"}, true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if _, ok := cfg.PoolNamed("team"); ok {
		t.Fatal("team is still there")
	}
	if p, _ := cfg.PoolNamed("mine"); !reflect.DeepEqual(p.Accounts, []string{"p1", "t1", "t2"}) {
		t.Errorf("mine has %v, want team's accounts after its own", p.Accounts)
	}
	if d, _ := cfg.ProfileNamed("default"); !reflect.DeepEqual(d.Pools, []string{"mine"}) {
		t.Errorf("default lists %v", d.Pools)
	}
	if w := cfg.Profiles[declaredIndex(cfg, "work")]; len(w.Pools) != 0 || len(w.Pool) != 0 {
		t.Errorf("work lists %v %v, want nothing", w.Pools, w.Pool)
	}
}

func TestPoolDeleteToNoPoolAndAnOwnPool(t *testing.T) {
	path := appWorld(t, sharedTOML)
	var buf bytes.Buffer
	if err := poolDeleteTo(&buf, path, "team", joinTarget{None: true}, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadOrFail(t, path).PoolOf("t1"); ok {
		t.Error("t1 should be in no pool")
	}
	if err := poolDeleteTo(&buf, path, "solo", joinTarget{Pool: "mine"}, true); err != nil {
		t.Fatal(err)
	}
	cfg := loadOrFail(t, path)
	if p, _ := cfg.PoolOf("s1"); p != "mine" {
		t.Errorf("s1 in %q, want mine", p)
	}
	if solo := cfg.Profiles[declaredIndex(cfg, "solo")]; len(solo.Pool) != 0 {
		t.Errorf("solo still has its own pool %v", solo.Pool)
	}
	if got := errCode(t, poolDeleteTo(&buf, path, "mine", joinTarget{Pool: "mine"}, true)); got != codeUsage {
		t.Errorf("into itself: %q, want usage", got)
	}
}

// §3: an account in use in a profile must not land where that profile
// cannot use it.
func TestPoolDeleteRefusesTakingAnAccountFromTheProfileItIsLiveIn(t *testing.T) {
	path := appWorld(t, sharedTOML)
	liveAt("t1", "work")
	var buf bytes.Buffer
	err := poolDeleteTo(&buf, path, "team", joinTarget{Pool: "mine"}, true)
	if got := errCode(t, err); got != codeLive {
		t.Fatalf("got %q (%v), want live", got, err)
	}
	if _, ok := loadOrFail(t, path).PoolNamed("team"); !ok {
		t.Error("a refused delete changed the config")
	}
}
