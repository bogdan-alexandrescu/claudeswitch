package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// The daemon with every outside thing replaced: no keychain, no usage API, no
// transcripts, no state file. Each fake records what it was asked, and the
// tests read those records.

type fakeLive struct{ name string }

func (f fakeLive) Name() string                  { return f.name }
func (f fakeLive) Read() (*keychain.Blob, error) { return nil, errors.New("fake live: not readable") }
func (f fakeLive) Write(*keychain.Blob) error    { return errors.New("fake live: not writable") }

type pollerCall struct {
	op, profile, account string
}

type fakePoller struct {
	st    *state.State
	calls []pollerCall
	lives map[string]keychain.Live
	// attribute is what PollActiveIn finds live in each profile.
	attribute map[string]string
	blind     bool
	cfgSet    *config.Config
}

func (f *fakePoller) Tick(context.Context) { f.calls = append(f.calls, pollerCall{op: "tick"}) }
func (f *fakePoller) PollActiveIn(_ context.Context, prof string) (*state.Account, error) {
	f.calls = append(f.calls, pollerCall{op: "pollactive", profile: prof})
	if id, ok := f.attribute[prof]; ok {
		f.st.Profile(prof).SetActive(id)
		return f.st.Get(id), nil
	}
	return nil, errors.New("fake: nothing attributed")
}
func (f *fakePoller) RefreshCandidatesIn(_ context.Context, _ time.Duration, prof string) int {
	f.calls = append(f.calls, pollerCall{op: "candidates", profile: prof})
	return 0
}
func (f *fakePoller) ApplyRejection(id, window string, resetsAt time.Time) {
	f.calls = append(f.calls, pollerCall{op: "reject", account: id, profile: window})
	a := f.st.Get(id) // as the real poller does: the refusal burns the account
	a.BurntTil, a.BurntWin = resetsAt, window
}
func (f *fakePoller) Blind(time.Duration) (time.Duration, bool) { return 0, f.blind }
func (f *fakePoller) SetLive(prof string, l keychain.Live) {
	if f.lives == nil {
		f.lives = map[string]keychain.Live{}
	}
	f.lives[prof] = l
}

type vaultCall struct {
	op      string
	item    keychain.Live
	account string
}

type fakeVault struct {
	calls        []vaultCall
	needsRefresh map[string]bool
	swapErr      error
	// holds is what each live item holds, keyed "item/account": "yes" or
	// "unknown". Absent means known not to hold it.
	holds   map[string]string
	syncErr map[string]error // by account
	// seatHolds overrides holds for HoldsAccount alone: an item holding the
	// account under a token the vault never saw, which LiveHolds cannot see.
	seatHolds map[string]string
	// swapOpts is what each swap was told about the profile it writes.
	swapOpts []vault.SwapOptions
	// refreshWindow is the window NeedsRefresh was last asked about.
	refreshWindow time.Duration
	// swapHook, when set, runs inside every swap: a test can hold one open.
	swapHook func()
}

func itemKey(item keychain.Live) string {
	if item == keychain.EnvLive() {
		return "env" // never ask the real environment's item its name
	}
	return item.Name()
}

func (f *fakeVault) answer(item keychain.Live, id string) (bool, bool) {
	switch f.holds[itemKey(item)+"/"+id] {
	case "yes":
		return true, true
	case "unknown":
		return false, false
	}
	return false, true
}

func (f *fakeVault) SwapToWith(_ context.Context, item keychain.Live, id, _ string, o vault.SwapOptions) (*vault.SwapResult, error) {
	f.calls = append(f.calls, vaultCall{"swap", item, id})
	f.swapOpts = append(f.swapOpts, o)
	if f.swapHook != nil {
		f.swapHook()
	}
	if f.swapErr != nil {
		return &vault.SwapResult{AccountID: id, RolledBack: true}, f.swapErr
	}
	return &vault.SwapResult{AccountID: id}, nil
}
func (f *fakeVault) Has(string) bool { return true }
func (f *fakeVault) SyncActiveIn(_ context.Context, item keychain.Live, id, _ string) (bool, error) {
	f.calls = append(f.calls, vaultCall{"sync", item, id})
	return false, f.syncErr[id]
}
func (f *fakeVault) LiveHolds(item keychain.Live, id string) (bool, bool) {
	f.calls = append(f.calls, vaultCall{"liveholds", item, id})
	return f.answer(item, id)
}
func (f *fakeVault) HoldsAccount(_ context.Context, item keychain.Live, id, _ string) (bool, bool) {
	f.calls = append(f.calls, vaultCall{"holds", item, id})
	switch f.seatHolds[itemKey(item)+"/"+id] {
	case "yes":
		return true, true
	case "unknown":
		return false, false
	}
	return f.answer(item, id)
}
func (f *fakeVault) NeedsRefresh(id string, w time.Duration) bool {
	f.refreshWindow = w
	return f.needsRefresh[id]
}
func (f *fakeVault) VaultedAt(string) time.Time { return time.Now() }
func (f *fakeVault) RefreshIn(_ context.Context, id, _ string, holder keychain.Live, _ bool) (*vault.Entry, error) {
	f.calls = append(f.calls, vaultCall{"refresh", holder, id})
	return &vault.Entry{AccountID: id}, nil
}

func (f *fakeVault) ops(op string) []vaultCall {
	var out []vaultCall
	for _, c := range f.calls {
		if c.op == op {
			out = append(out, c)
		}
	}
	return out
}

type fakeDet struct {
	root string
	idle bool
	last time.Time
	out  chan detector.Rejection
	// runs and exits count Run's starts and returns.
	runs, exits atomic.Int32
}

func (f *fakeDet) Run(stop <-chan struct{}) error {
	f.runs.Add(1)
	<-stop
	f.exits.Add(1)
	return nil
}
func (f *fakeDet) Rejections() <-chan detector.Rejection { return f.out }
func (f *fakeDet) CurrentDir() string                    { return "" }
func (f *fakeDet) IdleFor(time.Duration) bool            { return f.idle }
func (f *fakeDet) LastActivity() time.Time               { return f.last }
func (f *fakeDet) WorstLatency() time.Duration           { return 0 }

type fakeAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAudit) Write(e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeAudit) kind(k string) []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []audit.Event
	for _, e := range f.events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

type fakeNotifier struct{ sent []string }

func (f *fakeNotifier) Send(key, title, msg string) {
	f.sent = append(f.sent, key+"|"+title+"|"+msg)
}
func (f *fakeNotifier) Switched(from, to, reason, headroom string) {
	f.sent = append(f.sent, "switched|"+from+" → "+to+"|"+reason)
}
func (f *fakeNotifier) Exhausted(recovers string, at time.Time) {
	f.sent = append(f.sent, "exhausted|"+recovers)
}
func (f *fakeNotifier) RefreshExpiring(account string, in time.Duration) {
	f.sent = append(f.sent, "refreshexpiring|"+account)
}

type rig struct {
	d    *daemon
	p    *fakePoller
	v    *fakeVault
	aud  *fakeAudit
	nt   *fakeNotifier
	dets map[string]*fakeDet // by profile
	// items is each profile's live item, as resolve handed it out.
	items map[string]keychain.Live
	logs  *lockedBuffer
}

// lockedBuffer collects log output from any goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (r *rig) prof(name string) *profileLoop {
	for _, il := range r.d.profs {
		if il.name == name {
			return il
		}
	}
	panic("no profile " + name)
}

// newRig builds a daemon over cfg and a state loaded from an empty temporary
// file, so every configured profile is materialised the way Load does it.
func newRig(t *testing.T, cfg *config.Config, live bool) *rig {
	t.Helper()
	return newRigResolving(t, cfg, live, nil)
}

// newRigResolving is newRig where resolving the named profiles fails with
// the given errors.
func newRigResolving(t *testing.T, cfg *config.Config, live bool, resolveErr map[string]error) *rig {
	t.Helper()
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{
		p:     &fakePoller{st: st, attribute: map[string]string{}},
		v:     &fakeVault{needsRefresh: map[string]bool{}},
		aud:   &fakeAudit{},
		nt:    &fakeNotifier{},
		dets:  map[string]*fakeDet{},
		items: map[string]keychain.Live{},
	}
	resolve := func(in config.Profile) (keychain.Live, error) {
		if in.FromEnv {
			return keychain.EnvLive(), nil
		}
		if err := resolveErr[in.Name]; err != nil {
			return nil, err
		}
		l := fakeLive{name: "item-" + in.Name}
		return l, nil
	}
	projects := func(in config.Profile) string {
		if in.FromEnv {
			return ""
		}
		return "/projects/" + in.Name
	}
	r.logs = &lockedBuffer{}
	log := slog.New(slog.NewTextHandler(r.logs, nil))
	newDet := func(root string, _ *slog.Logger) activity {
		det := &fakeDet{root: root, idle: true, out: make(chan detector.Rejection, 128)}
		name := "default"
		if root != "" {
			name = strings.TrimPrefix(root, "/projects/")
		}
		r.dets[name] = det
		return det
	}
	profs := buildProfiles(cfg, log, resolve, projects, newDet)
	for _, il := range profs {
		r.items[il.name] = il.live
	}
	r.d = &daemon{
		cfg: cfg, st: st, p: r.p, v: r.v, aud: r.aud, nt: r.nt, log: log,
		live: live, idleGap: 8 * time.Second, profs: profs,
		save: func() error { return nil }, saveCLI: func() error { return nil },
		resolve: resolve, projects: projects, newDet: newDet,
		itemRef: func(l keychain.Live) (string, string, bool) {
			if l == keychain.EnvLive() {
				return "env", "", true
			}
			return l.Name(), "", true
		},
		ghostItem: func(service, _ string) keychain.Live { return fakeLive{name: service} },
	}
	return r
}

// twoProfiles is default (a, b) and work (w1, w2), with the ordinary
// thresholds from testCfg.
func twoProfiles() *config.Config {
	cfg := testCfg()
	cfg.Priority = []string{"a", "b", "w1", "w2"}
	cfg.Accounts = []config.Account{{ID: "a"}, {ID: "b"}, {ID: "w1"}, {ID: "w2"}}
	cfg.Profiles = []config.Profile{
		{Name: "default", Pool: []string{"a", "b"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1", "w2"}},
	}
	return cfg
}

func put(st *state.State, id string, five float64) {
	a := at(five, 10, time.Now())
	a.ID = id
	st.Accounts[id] = a
}

// A refusal seen in one profile's transcripts belongs to the account live in
// THAT profile. Charging it to the default profile's account would burn an
// account that was never refused and leave the refused one in use.
func TestDaemonRejectionIsChargedToItsOwnProfile(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	for _, id := range []string{"a", "b", "w1", "w2"} {
		put(st, id, 10)
	}

	resets := time.Now().Add(time.Hour)
	r.d.onRejection(context.Background(), r.prof("work"),
		detector.Rejection{Type: detector.FiveHour, ResetsAt: resets})

	var rejected []string
	for _, c := range r.p.calls {
		if c.op == "reject" {
			rejected = append(rejected, c.account)
		}
	}
	if len(rejected) != 1 || rejected[0] != "w1" {
		t.Fatalf("refusal charged to %v, want [w1] (work's live account)", rejected)
	}
	ev := r.aud.kind("rejection")
	if len(ev) != 1 || ev[0].From != "w1" || ev[0].Profile != "work" {
		t.Fatalf("rejection audit = %+v, want From w1, Profile work", ev)
	}
}

// Each profile rotates within its own pool and writes its own live item.
func TestDaemonSwapsWithinThePoolIntoTheProfilesOwnItem(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5) // the most room anywhere, but not in work's pool
	put(st, "w1", 90)
	put(st, "w2", 20)

	for _, il := range r.d.profs {
		r.d.evaluate(context.Background(), il, "poll")
	}

	swaps := r.v.ops("swap")
	if len(swaps) != 1 {
		t.Fatalf("swaps = %+v, want exactly one", swaps)
	}
	if swaps[0].account != "w2" || swaps[0].item != r.items["work"] {
		t.Fatalf("swapped %s into %v, want w2 into work's item %v",
			swaps[0].account, swaps[0].item.Name(), r.items["work"].Name())
	}
	if got := st.Profile("work").Active; got != "w2" {
		t.Errorf("work is now on %q, want w2", got)
	}
	if got := st.Profile("default").Active; got != "a" {
		t.Errorf("default moved to %q; it was never over its trigger", got)
	}
	sw := r.aud.kind("switch")
	if len(sw) != 1 || sw[0].Profile != "work" || sw[0].From != "w1" || sw[0].To != "w2" {
		t.Errorf("switch audit = %+v", sw)
	}
	for _, e := range r.aud.kind("decision") {
		if e.Profile == "" {
			t.Errorf("decision audited without its profile: %+v", e)
		}
	}
	if len(r.nt.sent) != 1 || !strings.Contains(r.nt.sent[0], "work: w1 → w2") {
		t.Errorf("notifications = %q, want the switch named with its profile", r.nt.sent)
	}
}

// §3: an account live in another profile is never written, whatever led the
// policy to choose it. The swap is refused loudly and audited as an error.
func TestDaemonRefusesToInstallAnAccountLiveElsewhere(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("work").SetActive("w1")
	// A manual login put work's w2 into the default profile.
	st.Profile("default").SetActive("w2")
	put(st, "a", 95)
	put(st, "b", 95)
	put(st, "w1", 90)
	put(st, "w2", 20)

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("wrote %+v; w2 is live in the default profile", swaps)
	}
	errs := r.aud.kind("error")
	if len(errs) != 1 || errs[0].Profile != "work" || errs[0].To != "w2" ||
		!strings.Contains(errs[0].Err, "default") {
		t.Fatalf("error audit = %+v, want one naming work, w2 and the default profile", errs)
	}
	if st.Profile("work").Active != "w1" {
		t.Error("work's active account changed although nothing was written")
	}
}

// A profile whose pool is out waits, rather than reaching into another
// profile's pool, and says which profile has run dry.
func TestDaemonNeverRotatesAcrossPools(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 97)
	put(st, "b", 99)
	put(st, "w1", 10)
	put(st, "w2", 5)

	r.d.evaluate(context.Background(), r.prof("default"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("default swapped %+v; its pool has no room and the rest is work's", swaps)
	}
	found := false
	for _, c := range r.p.calls {
		if c.op == "candidates" && c.profile == "default" {
			found = true
		}
	}
	if !found {
		t.Error("the re-read before giving up was not scoped to the default profile")
	}
	if len(r.nt.sent) != 1 || !strings.Contains(r.nt.sent[0], "default") {
		t.Errorf("notifications = %q, want the exhaustion named with its profile", r.nt.sent)
	}
}

// The idle-gap wait and the thresholds are each profile's own.
func TestDaemonIdleGapAndThresholdsArePerProfile(t *testing.T) {
	cfg := twoProfiles()
	cfg.Profiles[1].SwitchAt = 70
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 75) // under the global 85
	put(st, "b", 5)
	put(st, "w1", 75) // over work's own 70
	put(st, "w2", 5)

	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "w2" {
		t.Fatalf("swaps = %+v, want only work moving, on its own threshold", swaps)
	}

	// Now default is over and busy, work's session is idle and over again.
	put(st, "a", 90)
	put(st, "w2", 75)
	put(st, "w1", 5)
	st.Profile("work").LastSwitch = time.Time{} // past its cooldown
	r.dets["default"].idle = false
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	swaps := r.v.ops("swap")
	if len(swaps) != 2 || swaps[1].account != "w1" {
		t.Fatalf("swaps = %+v; a busy default must wait without holding work back", swaps)
	}
	if r.prof("default").wantSwitchSince.IsZero() {
		t.Error("default's wait for an idle gap was not recorded on default")
	}
	if !r.prof("work").wantSwitchSince.IsZero() {
		t.Error("work's switch happened, so it must not be left waiting")
	}
}

// Re-attribution reads each profile's own live item, and the poller is told
// which item belongs to which profile.
func TestDaemonReattributesEachProfileAgainstItsOwnItem(t *testing.T) {
	r := newRig(t, twoProfiles(), false)
	r.p.attribute["default"] = "a"
	r.p.attribute["work"] = "w1"
	for _, id := range []string{"a", "b", "w1", "w2"} {
		put(r.d.st, id, 10)
	}
	r.d.start(context.Background())

	var polled []string
	for _, c := range r.p.calls {
		if c.op == "pollactive" {
			polled = append(polled, c.profile)
		}
	}
	if fmt.Sprint(polled) != "[default work]" {
		t.Fatalf("attributed %v, want each profile once", polled)
	}
	if r.p.lives["work"] != r.items["work"] || r.p.lives["default"] != r.items["default"] {
		t.Fatalf("poller lives = %v", r.p.lives)
	}
	if r.d.st.Profile("work").Active != "w1" || r.d.st.Profile("default").Active != "a" {
		t.Fatalf("profiles = %+v", r.d.st.Profiles)
	}
}

// Vault upkeep: each profile's active entry is re-captured from that
// profile's own live item, and no account live in any profile is refreshed.
func TestDaemonVaultUpkeepFollowsEachProfile(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	for _, id := range []string{"a", "b", "w1", "w2"} {
		r.v.needsRefresh[id] = true
	}

	r.d.maintainVault(context.Background())

	syncs := r.v.ops("sync")
	if len(syncs) != 2 ||
		syncs[0].account != "a" || syncs[0].item != r.items["default"] ||
		syncs[1].account != "w1" || syncs[1].item != r.items["work"] {
		t.Fatalf("syncs = %+v, want a against default's item and w1 against work's", syncs)
	}
	var refreshed []string
	for _, c := range r.v.ops("refresh") {
		if c.item != nil {
			t.Errorf("idle refresh of %s was given a live item to write", c.account)
		}
		refreshed = append(refreshed, c.account)
	}
	if fmt.Sprint(refreshed) != "[b w2]" {
		t.Fatalf("refreshed %v, want only the idle b and w2", refreshed)
	}
	// Every candidate is checked against every profile's item.
	checked := map[string]int{}
	for _, c := range r.v.ops("liveholds") {
		checked[c.account]++
	}
	if checked["b"] != 2 || checked["w2"] != 2 {
		t.Errorf("live checks = %v, want each idle account checked in both profiles", checked)
	}
}

// With no [[profile]] blocks — every current user — the daemon is the
// single-profile daemon it was: one loop named default, the transcripts and
// live credential this process's environment names, every account a
// candidate, notifications without a profile, and audit events that differ
// only by profile=default.
func TestDaemonWithNoProfilesBehavesAsBefore(t *testing.T) {
	cfg := testCfg() // accounts a, b; no profiles
	r := newRig(t, cfg, true)
	st := r.d.st
	r.p.attribute["default"] = "a"
	put(st, "a", 90)
	put(st, "b", 10)

	if len(r.d.profs) != 1 {
		t.Fatalf("%d loops, want one", len(r.d.profs))
	}
	il := r.d.profs[0]
	if il.name != "default" || il.live != keychain.EnvLive() || r.dets["default"].root != "" {
		t.Fatalf("loop = %s, live %v, transcripts %q; want default, the environment's item and root",
			il.name, il.live, r.dets["default"].root)
	}
	if fmt.Sprint(il.pool) != "[a b]" {
		t.Fatalf("pool = %v, want every account", il.pool)
	}
	if il.cfg.SwitchAt != cfg.SwitchAt || il.cfg.HardFloor != cfg.HardFloor {
		t.Fatalf("thresholds changed: %v/%v", il.cfg.SwitchAt, il.cfg.HardFloor)
	}

	r.d.start(context.Background())
	resets := time.Now().Add(time.Hour)
	r.d.onRejection(context.Background(), il, detector.Rejection{Type: detector.FiveHour, ResetsAt: resets})

	wantPoller := []pollerCall{
		{op: "pollactive", profile: "default"},
		{op: "reject", account: "b", profile: detector.FiveHour},
		{op: "candidates", profile: "default"},
	}
	if fmt.Sprint(r.p.calls) != fmt.Sprint(wantPoller) {
		t.Errorf("poller calls = %v\nwant           %v", r.p.calls, wantPoller)
	}
	swaps := r.v.ops("swap")
	if len(swaps) != 1 || swaps[0].account != "b" || swaps[0].item != keychain.EnvLive() {
		t.Fatalf("swaps = %+v, want b into the environment's live item", swaps)
	}

	// The audit trail, field for field, as the single-profile daemon wrote
	// it, plus profile=default.
	type row struct{ Kind, Profile, Decision, From, To, Window string }
	var got []row
	for _, e := range r.aud.events {
		got = append(got, row{e.Kind, e.Profile, e.Decision, e.From, e.To, e.Window})
	}
	want := []row{
		{"decision", "default", "switch", "a", "b", ""},
		{"switch", "default", "", "a", "b", ""},
		{"rejection", "default", "", "b", "", detector.FiveHour},
		{"decision", "default", "wait", "b", "", ""},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit = %v\nwant    %v", got, want)
	}
	dec := r.aud.kind("decision")[0]
	if dec.FiveHour == nil || *dec.FiveHour != 90 || dec.DryRun {
		t.Errorf("first decision = %+v, want the active reading attached and live mode", dec)
	}
	wantNotes := []string{"switched|a → b|", "exhausted|"}
	if len(r.nt.sent) != 2 || !strings.HasPrefix(r.nt.sent[0], wantNotes[0]) ||
		!strings.HasPrefix(r.nt.sent[1], wantNotes[1]) {
		t.Errorf("notifications = %q, want a plain switch then the plain exhaustion", r.nt.sent)
	}
}

// Every state access is on the loop's goroutine; detectors only send. Run
// under -race this fails if any path reads or writes state from elsewhere —
// including a save that inserts a profile found on disk.
func TestDaemonOwnsStateOnOneGoroutine(t *testing.T) {
	r := newRig(t, twoProfiles(), false) // dry run: the active accounts stay put
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	for _, id := range []string{"a", "b", "w1", "w2"} {
		put(st, id, 10)
	}
	// What mergeProfiles does when the disk knows a profile the config
	// does not: insert it.
	saves := 0
	r.d.save = func() error {
		saves++
		st.Profile(fmt.Sprintf("from-disk-%d", saves%3))
		return nil
	}

	const per = 50
	var wg sync.WaitGroup
	for _, det := range r.dets {
		wg.Add(1)
		go func(det *fakeDet) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				det.out <- detector.Rejection{Type: detector.FiveHour, ResetsAt: time.Now().Add(time.Hour)}
			}
		}(det)
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	tick := make(chan time.Time)
	save := make(chan time.Time)
	done := make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, tick, save) }()
	for i := 0; i < 5; i++ {
		tick <- time.Now()
		save <- time.Now()
	}
	wg.Wait()
	// Every rejection must reach the loop before it is told to stop.
	deadline := time.After(10 * time.Second)
	for len(r.aud.kind("rejection")) < 2*per {
		select {
		case <-deadline:
			t.Fatalf("only %d of %d rejections reached the loop", len(r.aud.kind("rejection")), 2*per)
		case tick <- time.Now():
		}
	}
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	byProfile := map[string]int{}
	for _, e := range r.aud.kind("rejection") {
		byProfile[e.Profile+"/"+e.From]++
	}
	if byProfile["default/a"] != per || byProfile["work/w1"] != per {
		t.Errorf("rejections by profile = %v", byProfile)
	}
}

func (f *fakePoller) SetConfig(cfg *config.Config) { f.cfgSet = cfg }
