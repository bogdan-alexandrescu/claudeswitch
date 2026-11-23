package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// deadAccount is a state file holding account a as the daemon left it once
// its refresh token was spentRefresh: a needs-login error and an expired refresh
// token.
func deadAccount(t *testing.T, lastErr string) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 11, 23, 0, 51, 44, 0, time.UTC)
	st, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := st.Get("a")
	a.LastErr = lastErr
	a.RefreshExpiry = now.Add(-4 * 24 * time.Hour)
	a.TokenExpiry = now.Add(-4 * 24 * time.Hour)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path, now
}

func signedIn(now time.Time) vault.Entry {
	return vault.Entry{AccountID: "a", Email: "a@example.com", Plan: "Max team", OrgID: "org-a",
		Expiry: now.Add(8 * time.Hour), RefreshExpiry: now.Add(30 * 24 * time.Hour)}
}

const spentRefresh = "this account needs an interactive login (`claude` then /login): " +
	"the stored refresh token expired on 2026-11-19"

// A sign-in that was vaulted is the remedy for "needs sign-in", so the
// account must stop saying it at once, not at the daemon's next poll
// (reported 2026-11-23: `cs list` still said needs sign-in after `cs login`).
func TestRecordVaultedClearsNeedsSignIn(t *testing.T) {
	path, now := deadAccount(t, spentRefresh)
	st, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Get("a").NeedsSignIn(now) {
		t.Fatal("setup: the account should need a sign-in")
	}

	recordVaulted(st, "a", signedIn(now))

	a := st.Get("a")
	if a.NeedsSignIn(now) {
		t.Errorf("still needs sign-in after a vaulted sign-in: last_error %q", a.LastErr)
	}
	if got := accountState(a, 0, now); got == acctNeedsLogin {
		t.Errorf("account list state = %q after a vaulted sign-in", got)
	}
	if !a.TokenExpiry.Equal(now.Add(8 * time.Hour)) {
		t.Errorf("token expiry = %v, want the new access token's", a.TokenExpiry)
	}
	if a.OrgID != "org-a" || !a.RefreshExpiry.Equal(now.Add(30*24*time.Hour)) {
		t.Errorf("org %q refresh %v: not recorded", a.OrgID, a.RefreshExpiry)
	}
	if st.Emails["a"] != "a@example.com" || st.Plans["a"] != "Max team" {
		t.Errorf("email %q plan %q: not recorded", st.Emails["a"], st.Plans["a"])
	}
}

// The login saves through the CLI's merge, which keeps the daemon's record
// when its reading is newer. A failed poll writes no reading, so the
// cleared error must survive the save.
func TestRecordVaultedSurvivesTheSave(t *testing.T) {
	path, now := deadAccount(t, spentRefresh)
	st, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	recordVaulted(st, "a", signedIn(now))
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if a := again.Get("a"); a.NeedsSignIn(now) {
		t.Errorf("on disk the account still needs sign-in: last_error %q", a.LastErr)
	}
}

// Only the credential's error is answered by a sign-in; any other one is
// left for the next poll to settle.
func TestRecordVaultedKeepsOtherErrors(t *testing.T) {
	path, now := deadAccount(t, "usage request timed out")
	st, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	recordVaulted(st, "a", signedIn(now))
	if got := st.Get("a").LastErr; got != "usage request timed out" {
		t.Errorf("last_error = %q, want the timeout kept", got)
	}
}
