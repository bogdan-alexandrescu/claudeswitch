package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// --- 1. a reload that drops the active account ---

// The trace: Reconcile clears st.Default().Active when its record is dropped, and Decide
// with no active account picks the best eligible one and says Switch. On its
// own that is right for a daemon that has never seen a live account; after a
// reload it would swap away from a credential that is still live and working.
func TestDecideWithNoActiveAccountSwitches(t *testing.T) {
	now := time.Now()
	d := policy.Decide(policy.Input{Cfg: testCfg(), Now: now,
		St: &state.State{Accounts: map[string]*state.Account{"b": at(5, 5, now)}}})
	if d.Kind != policy.Switch {
		t.Fatalf("documenting Decide: with no active account it switches, got %v", d)
	}
}

func TestReloadThatDropsTheActiveAccountHoldsInsteadOfSwapping(t *testing.T) {
	now := time.Now()
	next := testCfg()
	next.Accounts = []config.Account{{ID: "b"}}
	next.Priority = []string{"b"}
	st := &state.State{Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "a"}}, Accounts: map[string]*state.Account{
		"a": at(40, 10, now), "b": at(5, 5, now)}}

	var h activeHold
	before := st.Default().Active
	st.Reconcile(pinnedOf(next))
	h.afterReload(before, st.Default().Active)
	if st.Default().Active != "" {
		t.Fatalf("precondition: Reconcile clears the dropped active account, got %q", st.Default().Active)
	}

	d := h.gate(policy.Decide(policy.Input{Cfg: next, St: st, Now: now}), st.Default().Active, next)
	if d.Kind == policy.Switch {
		t.Fatalf("a reload alone must not swap the live credential: %v", d)
	}
	if !strings.Contains(d.Reason, `"a"`) {
		t.Errorf("the hold must name the account that left the config: %q", d.Reason)
	}

	// Still unattributed after a re-poll: still holding.
	st.Default().SetActive(state.Unattributed)
	if d := h.gate(policy.Decision{Kind: policy.Switch, Target: "b"}, st.Default().Active, next); d.Kind == policy.Switch {
		t.Error("an unattributed live credential is still no reason to swap")
	}

	// Re-attributed to a configured account: back to normal.
	st.Default().SetActive("b")
	want := policy.Decision{Kind: policy.Switch, Target: "x", Reason: "r"}
	if d := h.gate(want, st.Default().Active, next); d != want {
		t.Errorf("once attributed, decisions pass through; got %v", d)
	}
	st.Default().Active = ""
	if d := h.gate(want, st.Default().Active, next); d != want {
		t.Error("the hold ends once lifted; it is not a permanent veto")
	}
}

func TestAReloadThatKeepsTheActiveAccountDoesNotHold(t *testing.T) {
	var h activeHold
	st := &state.State{Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "b"}}}
	h.afterReload("b", "b")
	want := policy.Decision{Kind: policy.Switch, Target: "a"}
	if d := h.gate(want, st.Default().Active, testCfg()); d != want {
		t.Errorf("got %v", d)
	}
}

// --- 2. re-adding must not replace a better credential with a worse one ---

func cred(refresh string, access, refreshExp time.Time) *keychain.OAuth {
	// Distinct credentials have distinct access tokens; identical ones do not.
	o := &keychain.OAuth{AccessToken: fmt.Sprint("a-", refresh, access.UnixMilli()),
		RefreshToken: refresh, ExpiresAt: access.UnixMilli()}
	if !refreshExp.IsZero() {
		o.RefreshTokenExpiresAt = refreshExp.UnixMilli()
	}
	return o
}

func TestReAddRefusesALiveCredentialWorseThanTheVaultedOne(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		vaulted, live *keychain.OAuth
		want          string
	}{
		{"live has no refresh token", cred("r", now.Add(time.Hour), time.Time{}),
			cred("", now.Add(8*time.Hour), time.Time{}), "refresh token"},
		{"vaulted refresh lasts longer", cred("r", now.Add(time.Hour), now.Add(30*24*time.Hour)),
			cred("r2", now.Add(time.Hour), now.Add(24*time.Hour)), "refresh token expires"},
		{"vaulted access lasts longer", cred("r", now.Add(8*time.Hour), time.Time{}),
			cred("r2", now.Add(time.Hour), time.Time{}), "access token expires"},
	} {
		why := liveIsWorse(tc.vaulted, tc.live)
		if why == "" || !strings.Contains(why, tc.want) {
			t.Errorf("%s: got %q, want it to mention %q", tc.name, why, tc.want)
		}
	}
}

func TestReAddAcceptsALiveCredentialAsGoodOrBetter(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		vaulted, live *keychain.OAuth
	}{
		{"newer live", cred("r", now.Add(time.Hour), now.Add(24*time.Hour)),
			cred("r2", now.Add(8*time.Hour), now.Add(30*24*time.Hour))},
		{"the same credential", cred("r", now.Add(time.Hour), now.Add(24*time.Hour)),
			cred("r", now.Add(time.Hour), now.Add(24*time.Hour))},
		{"vaulted could never renew", cred("", now.Add(time.Hour), time.Time{}),
			cred("r", now.Add(8*time.Hour), time.Time{})},
		{"nothing vaulted", nil, cred("r", now.Add(time.Hour), time.Time{})},
	} {
		if why := liveIsWorse(tc.vaulted, tc.live); why != "" {
			t.Errorf("%s: refused with %q", tc.name, why)
		}
	}
}

func TestStaleReAddRefusalOffersForce(t *testing.T) {
	err := staleReAddError("work-b", "the live credential has no refresh token")
	for _, want := range []string{"work-b", "Nothing was stored", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
}

// --- 3. config writes ---

func TestConfigEditsKeepTheFileMode(t *testing.T) {
	path := writeConfig(t, baseConfig)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("appendAccount changed the mode to %v", fi.Mode().Perm())
	}
	path2 := writeConfig(t, unpinnedConfig)
	_ = os.Chmod(path2, 0o640)
	if err := pinAccount(path2, "fresh", "p", "o"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path2); fi.Mode().Perm() != 0o640 {
		t.Errorf("pinAccount changed the mode to %v", fi.Mode().Perm())
	}
}

// A config kept in a dotfiles repo is usually a symlink. Renaming over the
// link replaced it with a plain file and left the repo copy stale.
func TestConfigEditsWriteThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.toml")
	_ = os.MkdirAll(filepath.Dir(target), 0o700)
	if err := os.WriteFile(target, []byte(unpinnedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := appendAccount(link, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	if err := pinAccount(link, "fresh", "p", "o"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink must survive the edit: %v %v", fi, err)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), `id           = "work-b"`) || !strings.Contains(string(got), `account_uuid = "p"`) {
		t.Errorf("the edits must land in the target:\n%s", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("no temporary files may be left beside the target: %v", entries)
	}
}

const multiLinePriority = `switch_at = 85

priority = [
  "work-a",
]

[[account]]
id           = "work-a"
account_uuid = "person-1"
org_id       = "org-1"
`

// The priority edit only understands a one-line list. When it cannot add the
// id, saying "last in priority" would be a claim about the file that is false.
func TestRecordSeatSaysWhenItCouldNotNameTheAccountInPriority(t *testing.T) {
	path := writeConfig(t, multiLinePriority)
	msg, err := recordSeat(loadOrFail(t, path), "work-b", gotSeat("p3", "o3"), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "last in priority") {
		t.Errorf("the id was not added to priority, so the message must not say it was: %q", msg)
	}
	if !strings.Contains(msg, "priority") || !strings.Contains(msg, "work-b") {
		t.Errorf("it must say the priority list needs the id by hand: %q", msg)
	}
	if !hasAccount(loadOrFail(t, path), "work-b") {
		t.Error("the block must still be written")
	}
}

// --- 4. hot polling default ---

// GROUND_TRUTH §42: about 25 calls per account per burst, recovering in 10-15
// minutes. A 20-second hot cadence drained that in about 8 minutes.
func TestHotPollingDefaultsToAMinute(t *testing.T) {
	path := writeConfig(t, "switch_at = 85\n")
	if got := loadOrFail(t, path).PollHot.Duration; got != time.Minute {
		t.Errorf("default poll_hot = %v, want 1m", got)
	}
}
