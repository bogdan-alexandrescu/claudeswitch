package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

type recoveryCalls struct {
	restored   []string
	cleared    []string
	identified int
}

func recoveryTestDeps(items []vault.RecoveryItem, c *recoveryCalls) recoveryDeps {
	return recoveryDeps{
		list: func([]string) []vault.RecoveryItem { return items },
		identify: func(_ context.Context, it vault.RecoveryItem) (*usage.Profile, error) {
			c.identified++
			pr := &usage.Profile{}
			pr.Account.UUID, pr.Organization.UUID, pr.Account.Email = "u1", "o1", "w1@example.com"
			return pr, nil
		},
		restore: func(_ context.Context, slot, account, seat string, force bool) (*vault.Entry, error) {
			c.restored = append(c.restored, slot+">"+account+"@"+seat+map[bool]string{true: "!", false: ""}[force])
			return &vault.Entry{AccountID: account, AccountUUID: "u1", OrgID: "o1"}, nil
		},
		clear: func(slot string) error {
			c.cleared = append(c.cleared, slot)
			return nil
		},
	}
}

func recoveryCfg() *config.Config {
	return &config.Config{
		Accounts: []config.Account{{ID: "w1", AccountUUID: "u1", OrgID: "o1"}, {ID: "free"}},
		Profiles: []config.Profile{{Name: "default", Pool: []string{"free"}},
			{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1"}}},
	}
}

func someItems() []vault.RecoveryItem {
	now := time.Now()
	return []vault.RecoveryItem{
		{Slot: "work-2", Profile: "work", N: 2, KeptAt: now.Add(-3 * time.Hour), Seat: "u1@o1",
			Expiry: now.Add(-time.Hour), RefreshExpiry: now.Add(20 * 24 * time.Hour), HasRefresh: true},
		{Slot: "default-1", Profile: "default", N: 1, KeptAt: now.Add(-26 * time.Hour)},
		{Slot: "work-3", Profile: "work", N: 3, Err: errors.New("keychain timed out")},
	}
}

// I1a: the list names each slot, its profile, when it was kept, the seat
// and the configured account that seat belongs to, and whether it can be
// renewed. It makes no network call unless asked to identify.
func TestRecoveryListShowsWhatEachSlotHolds(t *testing.T) {
	c := &recoveryCalls{}
	var b bytes.Buffer
	if err := runRecovery(&b, recoveryCfg(), nil, recoveryOpts{}, recoveryTestDeps(someItems(), c)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"work-2", "default-1", "work-3", "w1", "3h", "unreadable", "renewable"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if c.identified != 0 {
		t.Errorf("listing identified %d credentials without --identify", c.identified)
	}
	if !strings.Contains(out, "cs recovery restore") {
		t.Errorf("list does not say how to restore:\n%s", out)
	}
}

func TestRecoveryListIdentifiesOnlyWhenAsked(t *testing.T) {
	c := &recoveryCalls{}
	var b bytes.Buffer
	if err := runRecovery(&b, recoveryCfg(), nil, recoveryOpts{identify: true}, recoveryTestDeps(someItems(), c)); err != nil {
		t.Fatal(err)
	}
	// The unreadable slot is not identified: there is nothing to send.
	if c.identified != 2 {
		t.Errorf("identified %d, want 2", c.identified)
	}
	if !strings.Contains(b.String(), "w1@example.com") {
		t.Errorf("identified email not shown:\n%s", b.String())
	}
}

func TestRecoveryListWithNothingKept(t *testing.T) {
	var b bytes.Buffer
	if err := runRecovery(&b, recoveryCfg(), nil, recoveryOpts{}, recoveryTestDeps(nil, &recoveryCalls{})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "no recovery copies") {
		t.Errorf("empty list:\n%s", b.String())
	}
}

// restore verifies against the account's pinned seat; an account the config
// does not have is refused before the vault is touched.
func TestRecoveryRestorePassesTheConfiguredSeat(t *testing.T) {
	c := &recoveryCalls{}
	var b bytes.Buffer
	deps := recoveryTestDeps(someItems(), c)
	if err := runRecovery(&b, recoveryCfg(), []string{"restore", "work-2", "w1"}, recoveryOpts{}, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.restored, ",") != "work-2>w1@u1@o1" {
		t.Errorf("restore calls = %v", c.restored)
	}
	if err := runRecovery(&b, recoveryCfg(), []string{"restore", "work-2", "nobody"}, recoveryOpts{}, deps); err == nil {
		t.Error("restored under an account the config does not have")
	}
	if err := runRecovery(&b, recoveryCfg(), []string{"restore", "work-2", "w1"}, recoveryOpts{force: true}, deps); err != nil {
		t.Fatal(err)
	}
	if c.restored[len(c.restored)-1] != "work-2>w1@u1@o1!" {
		t.Errorf("--force not passed: %v", c.restored)
	}
}

// clear deletes what may be the only copy of a credential, so it asks; with
// nobody to ask it needs --yes.
func TestRecoveryClearAsksFirst(t *testing.T) {
	c := &recoveryCalls{}
	deps := recoveryTestDeps(someItems(), c)
	var b bytes.Buffer
	if err := runRecovery(&b, recoveryCfg(), []string{"clear", "work-2"}, recoveryOpts{}, deps); err == nil {
		t.Error("cleared with nobody to confirm and no --yes")
	}
	deps.confirm = func(string) bool { return false }
	if err := runRecovery(&b, recoveryCfg(), []string{"clear", "work-2"}, recoveryOpts{}, deps); err == nil {
		t.Error("cleared after the answer was no")
	}
	if len(c.cleared) != 0 {
		t.Fatalf("cleared %v", c.cleared)
	}
	deps.confirm = func(string) bool { return true }
	if err := runRecovery(&b, recoveryCfg(), []string{"clear", "work-2"}, recoveryOpts{}, deps); err != nil {
		t.Fatal(err)
	}
	deps.confirm = nil
	if err := runRecovery(&b, recoveryCfg(), []string{"clear", "default-1"}, recoveryOpts{yes: true}, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.cleared, ",") != "work-2,default-1" {
		t.Errorf("cleared %v", c.cleared)
	}
}

// runWith is run with the doctor's seams adjusted by mod.
func (w *doctorWorld) runWith(t *testing.T, mod func(*doctorDeps)) (string, error) {
	t.Helper()
	old := doctorSeams
	t.Cleanup(func() { doctorSeams = old })
	d := doctorDeps{
		readLive: w.readLive, fetchUsage: w.fetch, resolve: w.resolve,
		duplicates: w.dup, verifyOne: w.verify,
		vaulted: func(*config.Config) int { return 0 },
	}
	mod(&d)
	doctorSeams = d
	var b bytes.Buffer
	err := runDoctor(&b, w.cfgPath, false)
	return b.String(), err
}

// doctor mentions non-empty slots as a warning: how many, and the oldest.
func TestDoctorWarnsAboutRecoveryCopies(t *testing.T) {
	w := newDoctorWorld(t, doctorBaseConfig)
	out, err := w.runWith(t, func(d *doctorDeps) {
		d.recovery = func(*config.Config) []vault.RecoveryItem { return someItems() }
	})
	if err != nil {
		t.Fatalf("recovery copies are a warning, not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn] recovery") || !strings.Contains(out, "3 ") ||
		!strings.Contains(out, "26h") || !strings.Contains(out, "cs recovery") {
		t.Errorf("doctor recovery line:\n%s", out)
	}
	out, _ = w.runWith(t, func(d *doctorDeps) {
		d.recovery = func(*config.Config) []vault.RecoveryItem { return nil }
	})
	if strings.Contains(out, "[warn] recovery") {
		t.Errorf("warned with nothing kept:\n%s", out)
	}
}
