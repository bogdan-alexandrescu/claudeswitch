package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

type optsRecorder struct{ opts []vault.SwapOptions }

func (o *optsRecorder) SwapToWith(_ context.Context, _ keychain.Live, id, _ string, opts vault.SwapOptions) (*vault.SwapResult, error) {
	o.opts = append(o.opts, opts)
	return &vault.SwapResult{AccountID: id}, nil
}

// Lane 7: switching back after a login is a swap like any other, and must
// tell the capture who owns what — the outgoing and target seats, and the
// seat-to-account lookup — as the daemon and `use` do. Without them a live
// credential the vault does not already hold goes to a recovery slot even
// when its seat names a configured account.
func TestRestoreActiveTellsTheSwapTheSeats(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{
		{ID: "home", AccountUUID: "u-home", OrgID: "o-home"},
		{ID: "new", AccountUUID: "u-new", OrgID: "o-new"},
	}}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), "work")
	if err != nil {
		t.Fatal(err)
	}
	st.Profile("work").SetActive("new")
	rec := &optsRecorder{}

	restoreActive(rec, cfg, st, "work", fakeLive{name: "item-work"}, "home")

	if len(rec.opts) != 1 {
		t.Fatalf("swaps = %d, want 1", len(rec.opts))
	}
	o := rec.opts[0]
	if o.Profile != "work" || o.Outgoing != "new" {
		t.Errorf("profile %q outgoing %q", o.Profile, o.Outgoing)
	}
	if o.OutgoingSeat != "u-new@o-new" || o.TargetSeat != "u-home@o-home" {
		t.Errorf("seats: outgoing %q target %q", o.OutgoingSeat, o.TargetSeat)
	}
	if o.SeatOwner == nil || o.SeatOwner("u-home@o-home") != "home" {
		t.Error("no seat-to-account lookup")
	}
	if st.Profile("work").Active != "home" {
		t.Errorf("active after restore = %q", st.Profile("work").Active)
	}
}
