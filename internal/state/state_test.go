package state

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func withUtil(five, seven float64) *usage.Usage {
	return &usage.Usage{
		FiveHour: usage.Window{Utilization: &five},
		SevenDay: usage.Window{Utilization: &seven},
	}
}

func TestUnknownIsNeverAvailable(t *testing.T) {
	a := &Account{ID: "work"}
	if got := a.Availability(0); got != Unknown {
		t.Fatalf("account with no reading: got %q, want %q", got, Unknown)
	}
}

func TestBurntBeatsAHealthyReading(t *testing.T) {
	a := &Account{ID: "work", Last: withUtil(3, 3), LastAt: time.Now(),
		BurntTil: time.Now().Add(time.Hour), BurntWin: "seven_day"}
	if got := a.Availability(0); got != Burnt {
		t.Fatalf("a refused account must stay burnt regardless of an older reading: got %q", got)
	}
}

func TestBurntExpires(t *testing.T) {
	a := &Account{ID: "work", Last: withUtil(10, 10), LastAt: time.Now(),
		BurntTil: time.Now().Add(-time.Minute)}
	if got := a.Availability(0); got != Available {
		t.Fatalf("past resetsAt the account is usable again: got %q", got)
	}
}

func TestReserveUsesTheWorseWindow(t *testing.T) {
	// Personal reserve is 70. Five-hour is fine but weekly is over: the account
	// must be held back, because weekly is what bites.
	a := &Account{ID: "personal", Last: withUtil(12, 74), LastAt: time.Now()}
	if got := a.Availability(70); got != Reserved {
		t.Fatalf("weekly over reserve: got %q, want %q", got, Reserved)
	}
	a2 := &Account{ID: "personal", Last: withUtil(12, 68), LastAt: time.Now()}
	if got := a2.Availability(70); got != Available {
		t.Fatalf("under reserve on both windows: got %q, want %q", got, Available)
	}
}

func TestStale(t *testing.T) {
	a := &Account{ID: "x", Last: withUtil(1, 1), LastAt: time.Now().Add(-10 * time.Minute)}
	if !a.Stale(5 * time.Minute) {
		t.Fatal("a ten-minute-old reading is stale at a five-minute bound")
	}
}

// Active is an observation, and both the daemon and a CLI swap can establish it.
// Merging by ownership meant a `claudeswitch use` was silently reverted by the
// daemon's older belief; recency is the rule.
func TestActiveMergesByRecencyNotByOwner(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	// The daemon establishes "personal" and saves.
	daemon, _ := Load(path)
	daemon.SetActive("personal")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	// A CLI swap to "work" happens afterwards.
	cli, _ := Load(path)
	cli.SetActive("work")
	cli.ActiveAt = daemon.ActiveAt.Add(time.Second)
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(path)
	if got.Active != "work" {
		t.Fatalf("the newer observation must win: got %q, want work", got.Active)
	}

	// Now the daemon, still believing "personal" from before, saves again. Its
	// belief is older, so it must not clobber the swap.
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(path)
	if got.Active != "work" {
		t.Fatalf("a stale daemon save reverted a newer swap: got %q, want work", got.Active)
	}
}

func TestDaemonObservationWinsWhenItIsNewer(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	cli, _ := Load(path)
	cli.SetActive("work")
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}

	daemon, _ := Load(path)
	daemon.SetActive("personal") // observed later, e.g. after an external login
	daemon.ActiveAt = cli.ActiveAt.Add(time.Second)
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(path)
	if got.Active != "personal" {
		t.Fatalf("got %q, want personal", got.Active)
	}
}

// A record whose organization disagrees with the id's configured organization
// describes a DIFFERENT account. Reusing an id after a rename left exactly such
// a record, and the policy engine read an exhausted account as having headroom.
// A record whose seat disagrees with the id's configured seat describes a
// DIFFERENT quota pool. Comparing organizations alone cannot catch this: two
// colleagues share an organization and have entirely separate quota, and a
// record filed under the wrong name is acted on as though it were right
// (observed 2026-09-10 — two accounts showed identical numbers while actually
// at 100% and 75%).
func TestReconcileDropsRecordsForTheWrongSeat(t *testing.T) {
	const org = "shared-org"
	s := &State{Accounts: map[string]*Account{
		"alice": {ID: "alice", OrgID: org, Seat: "alice@" + org},
		"bob":   {ID: "bob", OrgID: org, Seat: "alice@" + org}, // wrong: this is alice's reading
		"solo":  {ID: "solo", OrgID: "other", Seat: "carol@other"},
	}}
	pinned := map[string]string{
		"alice": "alice@" + org,
		"bob":   "bob@" + org,
		"solo":  "carol@other",
	}
	dropped := s.Reconcile(pinned)
	if _, still := s.Accounts["bob"]; still {
		t.Fatal("a record holding a colleague's reading must be discarded")
	}
	if len(s.Accounts) != 2 {
		t.Fatalf("the correct records must survive: %v", s.Accounts)
	}
	if len(dropped) != 1 {
		t.Fatalf("the discard should be reported, got %v", dropped)
	}
}

// Records written before seats were recorded carry only an organization. They
// are kept when the organization is consistent with the pin, since that is the
// most that can be checked, and dropping them would discard good data.
func TestReconcileKeepsPreSeatRecordsWithAConsistentOrganization(t *testing.T) {
	s := &State{Accounts: map[string]*Account{
		"alice": {ID: "alice", OrgID: "org-1"},
		"stray": {ID: "stray", OrgID: "org-9"},
	}}
	s.Reconcile(map[string]string{
		"alice": "alice@org-1",
		"stray": "stray@org-1",
	})
	if _, ok := s.Accounts["alice"]; !ok {
		t.Error("a record whose organization matches the pin should be kept")
	}
	if _, ok := s.Accounts["stray"]; ok {
		t.Error("a record whose organization contradicts the pin should go")
	}
}

func TestReconcileDropsUnconfiguredAccountsAndClearsActive(t *testing.T) {
	s := &State{Active: "gone", Accounts: map[string]*Account{
		"gone": {ID: "gone", OrgID: "abc"},
		"kept": {ID: "kept", OrgID: "def"},
	}}
	s.Reconcile(map[string]string{"kept": "def"})
	if _, still := s.Accounts["gone"]; still {
		t.Fatal("an account no longer in the config must not linger")
	}
	if s.Active != "" {
		t.Fatalf("Active must not point at a dropped account, got %q", s.Active)
	}
}

func TestReconcileKeepsUnpinnedAccounts(t *testing.T) {
	s := &State{Accounts: map[string]*Account{"x": {ID: "x", OrgID: "whatever"}}}
	s.Reconcile(map[string]string{"x": ""}) // configured, but no org pinned
	if _, ok := s.Accounts["x"]; !ok {
		t.Fatal("an account with no pinned organization cannot be checked, so keep it")
	}
}

// A merge must not resurrect what this process deliberately removed. Without
// this, `forget` and `rename` appeared to work and were undone on the next save.
func TestDroppedAccountsAreNotRestoredByAMerge(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	seed, _ := Load(path)
	seed.Get("stale").OrgID = "abc"
	seed.Get("keep").OrgID = "def"
	if err := seed.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	cli, _ := Load(path)
	cli.Drop("stale")
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}

	got, _ := Load(path)
	if _, back := got.Accounts["stale"]; back {
		t.Fatal("the merge restored a record that was deliberately dropped")
	}
	if _, ok := got.Accounts["keep"]; !ok {
		t.Fatal("unrelated records must survive")
	}
}

// A reading is a lower bound. Under heavy use utilization moved 8 points in 3
// minutes (2026-09-10), so acting on a four-minute-old figure can miss the
// trigger entirely.
func TestProjectedAllowsForWhatHasBeenSpentSinceTheReading(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      withUtil(40, 10),
		LastAt:    now.Add(-3 * time.Minute),
		PrevWorst: 31,
		PrevAt:    now.Add(-6 * time.Minute),
	}
	// 40 - 31 = 9 points over 3 minutes = 3%/min.
	if got := a.BurnRate(); got < 2.9 || got > 3.1 {
		t.Fatalf("burn rate: got %.2f, want ~3.0", got)
	}
	// Three minutes since the reading at 3%/min ≈ 49%.
	if got := a.Projected(now); got < 48 || got > 50 {
		t.Fatalf("projected: got %.1f, want ~49", got)
	}
}

func TestProjectedIsTheReadingWhenNoRateIsKnown(t *testing.T) {
	now := time.Now()
	a := &Account{Last: withUtil(40, 10), LastAt: now.Add(-5 * time.Minute)}
	if got := a.Projected(now); got != 40 {
		t.Fatalf("with no previous reading the projection must be the reading itself, got %.1f", got)
	}
}

// A falling figure means a window reset, not negative burn. Treating it as a
// rate would project utilization downward and manufacture headroom.
func TestFallingUtilizationIsNotNegativeBurn(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      withUtil(2, 1),
		LastAt:    now.Add(-time.Minute),
		PrevWorst: 95,
		PrevAt:    now.Add(-2 * time.Minute),
	}
	if got := a.BurnRate(); got != 0 {
		t.Fatalf("a reset must yield no burn rate, got %.2f", got)
	}
	if got := a.Projected(now); got != 2 {
		t.Fatalf("projection must not drift below the reading, got %.1f", got)
	}
}

func TestProjectedIsCappedAt100(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      withUtil(97, 50),
		LastAt:    now.Add(-30 * time.Minute),
		PrevWorst: 50,
		PrevAt:    now.Add(-40 * time.Minute),
	}
	if got := a.Projected(now); got != 100 {
		t.Fatalf("projection must cap at 100, got %.1f", got)
	}
}

// The failure this prevents: believing an account is exhausted from a reading
// that describes a window which has since refilled. With every account ruled out
// that way, there is nothing to rotate to and the tool fails at its one job.
func TestReadingExpiresWithTheWindowItDescribes(t *testing.T) {
	now := time.Now()
	reset := now.Add(-5 * time.Minute) // the window rolled over five minutes ago

	exhausted := &Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: f(100), ResetsAt: &reset},
			SevenDay: usage.Window{Utilization: f(20), ResetsAt: &reset},
		},
		LastAt: now.Add(-20 * time.Minute), // read before the reset
	}
	if !exhausted.ExpiredAt(now) {
		t.Fatal("a reading taken before the window reset no longer describes anything")
	}
	if got := exhausted.AvailabilityAt(0, now); got != Unknown {
		t.Fatalf("got %q, want %q — an expired reading is not evidence of exhaustion", got, Unknown)
	}
}

// A reading taken after the reset describes the current window and stands.
func TestReadingAfterTheResetIsStillGood(t *testing.T) {
	now := time.Now()
	reset := now.Add(-5 * time.Minute)
	a := &Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: f(30), ResetsAt: &reset},
			SevenDay: usage.Window{Utilization: f(10), ResetsAt: &reset},
		},
		LastAt: now.Add(-time.Minute), // read after the reset
	}
	if a.ExpiredAt(now) {
		t.Fatal("a reading taken after the reset describes the current window")
	}
	if got := a.AvailabilityAt(0, now); got != Available {
		t.Fatalf("got %q, want %q", got, Available)
	}
}

// An account is worth re-reading the moment its window turns over, rather than
// whenever the idle schedule next comes round.
func TestRepollAtIsTheWindowReset(t *testing.T) {
	now := time.Now()
	reset := now.Add(20 * time.Minute)
	a := &Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: f(100), ResetsAt: &reset},
			SevenDay: usage.Window{Utilization: f(20), ResetsAt: &reset},
		},
		LastAt: now,
	}
	at := a.RepollAt()
	if at.Before(reset) {
		t.Fatalf("re-poll at %v is before the reset at %v", at, reset)
	}
	if at.After(reset.Add(time.Minute)) {
		t.Fatalf("re-poll at %v is long after the reset — the headroom is wasted", at)
	}
}

func TestRepollAtIsUnsetWithoutAResetTime(t *testing.T) {
	a := &Account{Last: &usage.Usage{FiveHour: usage.Window{Utilization: f(50)}}, LastAt: time.Now()}
	if !a.RepollAt().IsZero() {
		t.Fatal("with no reset time there is no moment worth waiting for")
	}
}

func f(v float64) *float64 { return &v }
