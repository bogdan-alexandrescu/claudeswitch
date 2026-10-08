package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// overWorkPool sets work up to want w2: w1 over its trigger, w2 with room.
func overWorkPool(r *rig) {
	st := r.d.st
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 10)
	put(st, "w1", 90)
	put(st, "w2", 20)
}

// §3 is checked against the live items, not only state. Someone signed in to
// w2 by hand in the default profile; state has not caught up (it still says
// a). The swap of w2 into work is refused and nothing is written.
func TestDaemonRefusesWhenAnotherProfilesItemHoldsTheTarget(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	overWorkPool(r)
	r.d.st.Profile("default").SetActive("a") // stale
	r.v.holds = map[string]string{"item-default/w2": "yes"}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("wrote %+v; default's item holds w2", swaps)
	}
	errs := r.aud.kind("error")
	if len(errs) != 1 || errs[0].Profile != "work" || errs[0].To != "w2" ||
		!strings.Contains(errs[0].Err, "default") {
		t.Fatalf("error audit = %+v", errs)
	}
	// Only the other profile's item is asked; work's own is the one being written.
	for _, c := range r.v.ops("holds") {
		if c.item == r.items["work"] {
			t.Error("asked the target profile's own item")
		}
	}
}

// An item that cannot be read might hold the target, so the swap is refused:
// the safe side of an unknown is not writing.
func TestDaemonRefusesWhenAnotherProfilesItemIsUnreadable(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	overWorkPool(r)
	r.d.st.Profile("default").SetActive("a")
	r.v.holds = map[string]string{"item-default/w2": "unknown"}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("wrote %+v while default's item could not be read", swaps)
	}
	if errs := r.aud.kind("error"); len(errs) != 1 || !strings.Contains(errs[0].Err, "could not") {
		t.Fatalf("error audit = %+v, want one saying the item could not be checked", errs)
	}
}

// When every other item is known not to hold the target, the swap goes ahead.
func TestDaemonSwapsWhenNoOtherItemHoldsTheTarget(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	overWorkPool(r)
	r.d.st.Profile("default").SetActive("a")

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "w2" {
		t.Fatalf("swaps = %+v, want w2 into work", swaps)
	}
	if holds := r.v.ops("holds"); len(holds) != 1 || holds[0].item != r.items["default"] {
		t.Fatalf("checked %+v, want default's item once", holds)
	}
}

// Idle refresh: an item known to hold an account skips it; an unreadable item
// skips everything it might hold; a missing item (a logged-out profile)
// holds nothing and stops nobody's refresh.
func TestDaemonIdleRefreshSkipsOnlyWhatMayBeLive(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	for _, id := range []string{"a", "b", "w1", "w2"} {
		r.v.needsRefresh[id] = true
	}
	// work's item holds b (state has not caught up), and default's cannot
	// say about w2. Missing items answer "known, not held", the fake's default.
	r.v.holds = map[string]string{"item-work/b": "yes", "item-default/w2": "unknown"}

	r.d.maintainVault(context.Background())
	if refreshed := r.v.ops("refresh"); len(refreshed) != 0 {
		t.Fatalf("refreshed %+v; b is live in work and w2 may be live in default", refreshed)
	}

	r.v.holds = nil // both items known to hold neither: one profile logged out, say
	r.v.calls = nil
	r.d.maintainVault(context.Background())
	var refreshed []string
	for _, c := range r.v.ops("refresh") {
		refreshed = append(refreshed, c.account)
	}
	if fmt.Sprint(refreshed) != "[b w2]" {
		t.Fatalf("refreshed %v, want b and w2", refreshed)
	}
}

// With no profiles and no accounts the daemon says so, as it always did: a
// "no accounts configured" wait and the exhaustion notice. Only a declared
// profile's empty pool (D13) is passed over in silence.
func TestDaemonWithNoAccountsStillSaysSo(t *testing.T) {
	cfg := testCfg()
	cfg.Accounts, cfg.Priority = nil, nil
	r := newRig(t, cfg, true)

	r.d.evaluate(context.Background(), r.prof("default"), "poll")

	dec := r.aud.kind("decision")
	if len(dec) != 1 || dec[0].Decision != "wait" || !strings.Contains(dec[0].Reason, "no accounts") {
		t.Fatalf("decisions = %+v, want the no-accounts wait", dec)
	}
	if len(r.nt.sent) != 1 || !strings.HasPrefix(r.nt.sent[0], "exhausted|") {
		t.Fatalf("notifications = %q", r.nt.sent)
	}

	declared := twoProfiles()
	declared.Profiles = append(declared.Profiles, config.Profile{Name: "spare", Dir: "~/.claude-spare"})
	r2 := newRig(t, declared, true)
	r2.d.evaluate(context.Background(), r2.prof("spare"), "poll")
	if len(r2.aud.events) != 0 || len(r2.nt.sent) != 0 {
		t.Fatalf("an empty declared pool produced %+v / %q", r2.aud.events, r2.nt.sent)
	}
}

// The vault sync finding the live credential belongs to an account from
// another profile's pool records that truth and says so as an error, as
// re-attribution does.
func TestDaemonForeignCredentialFromAnotherPoolIsFlagged(t *testing.T) {
	cfg := twoProfiles()
	for i := range cfg.Accounts {
		cfg.Accounts[i].AccountUUID = "u-" + cfg.Accounts[i].ID
		cfg.Accounts[i].OrgID = "org"
	}
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	r.v.syncErr = map[string]error{"a": &vault.ForeignCredentialError{AccountID: "a", GotOrg: "u-w2@org"}}

	r.d.maintainVault(context.Background())

	if got := st.Profile("default").Active; got != "w2" {
		t.Fatalf("default's active = %q, want the truth, w2", got)
	}
	logs := r.logs.String()
	if !strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, "another profile's pool") ||
		!strings.Contains(logs, "pool_of=work") {
		t.Fatalf("no cross-pool error logged:\n%s", logs)
	}
}
