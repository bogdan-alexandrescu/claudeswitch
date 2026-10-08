package vault

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/cclock"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// Lane 6, I1 and I2: a swap first saves whatever it is about to overwrite,
// and makes its live writes holding Claude Code's credential locks.

const seatA = "ua@oa"

func ms(t time.Time) int64 { return t.UnixMilli() }

// swapWorld is a vault holding a (outgoing) and b (incoming), and an item
// holding liveTok for a.
func swapWorld(t *testing.T, liveTok string, seatOf map[string]string) (*Vault, *fakeEndpoints, map[string]*keychain.Blob, *recordingItem) {
	t.Helper()
	now := time.Now()
	entries := map[string]*keychain.Blob{
		"a": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "a-old", RefreshToken: "r-a-old",
			ExpiresAt: ms(now.Add(time.Hour)), RefreshTokenExpiresAt: ms(now.Add(24 * time.Hour))},
			Meta: &keychain.Meta{AccountID: "a", AccountUUID: "ua", OrgID: "oa", Email: "a@x"}},
		"b": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "b-token", RefreshToken: "r-b",
			ExpiresAt: ms(now.Add(5 * time.Hour))}},
	}
	v, ep := testVaultWith(t, entries, map[string]string{"b-token": "org-b"}, seatOf)
	it := item("default", liveTok)
	it.blob.ClaudeAIOAuth.ExpiresAt = ms(now.Add(8 * time.Hour))
	it.blob.ClaudeAIOAuth.RefreshTokenExpiresAt = ms(now.Add(30 * 24 * time.Hour))
	return v, ep, entries, it
}

func outgoingA() SwapOptions {
	return SwapOptions{Profile: "default", Outgoing: "a", OutgoingSeat: seatA,
		LockWait: 200 * time.Millisecond}
}

// Claude Code refreshed a since the last two-minute sync: the live item holds
// the only valid refresh token. The swap must vault it before overwriting.
func TestSwapCapturesTheOutgoingAccountsRefreshedCredential(t *testing.T) {
	v, _, entries, it := swapWorld(t, "a-new", map[string]string{"a-new": seatA})

	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err != nil {
		t.Fatal(err)
	}
	got := entries["a"]
	if got.ClaudeAIOAuth.AccessToken != "a-new" || got.ClaudeAIOAuth.RefreshToken != "r-a-new" {
		t.Fatalf("a's entry = %+v, want the live credential captured", got.ClaudeAIOAuth)
	}
	if got.Meta == nil || got.Meta.Seat() != seatA || got.Meta.Email != "a@x" {
		t.Errorf("a's annotation lost on capture: %+v", got.Meta)
	}
	if it.blob.ClaudeAIOAuth.AccessToken != "b-token" {
		t.Errorf("live now holds %q, want b", it.blob.ClaudeAIOAuth.AccessToken)
	}
}

// The token the vault already holds is a free match: no identity lookup, and no
// write to the vault.
func TestSwapCaptureIsFreeWhenTheVaultAlreadyHoldsTheLiveToken(t *testing.T) {
	v, ep, entries, it := swapWorld(t, "a-old", map[string]string{"a-old": seatA})
	before := entries["a"]

	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err != nil {
		t.Fatal(err)
	}
	if ep.profiles != 0 {
		t.Errorf("spent %d identity lookups on a token the vault already holds", ep.profiles)
	}
	if entries["a"] != before {
		t.Error("the vault entry was rewritten for nothing")
	}
	if len(ep.recovered) != 0 {
		t.Errorf("recovery copies %v for an attributed token", ep.recovered)
	}
}

// Mirror of the re-add rule: never replace a renewable credential with one
// that cannot be renewed, or with an earlier expiry.
func TestSwapDoesNotCaptureACredentialWorseThanTheVaultedOne(t *testing.T) {
	v, _, entries, it := swapWorld(t, "a-new", map[string]string{"a-new": seatA})
	it.blob.ClaudeAIOAuth.RefreshToken = ""

	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err != nil {
		t.Fatal(err)
	}
	if entries["a"].ClaudeAIOAuth.AccessToken != "a-old" {
		t.Fatalf("a's renewable entry replaced by a credential with no refresh token")
	}

	v, _, entries, it = swapWorld(t, "a-new", map[string]string{"a-new": seatA})
	it.blob.ClaudeAIOAuth.RefreshTokenExpiresAt = ms(time.Now().Add(time.Hour)) // vault: +24h
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err != nil {
		t.Fatal(err)
	}
	if entries["a"].ClaudeAIOAuth.AccessToken != "a-old" {
		t.Fatalf("a's entry replaced by a credential whose refresh token runs out sooner")
	}
}

// A live credential that cannot be attributed is kept, not discarded.
func TestSwapKeepsARecoveryCopyOfAnUnattributedCredential(t *testing.T) {
	v, ep, entries, it := swapWorld(t, "stranger", nil) // the identity lookup fails

	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err != nil {
		t.Fatal(err)
	}
	if entries["a"].ClaudeAIOAuth.AccessToken != "a-old" {
		t.Error("an unattributed credential was filed under the outgoing account")
	}
	if len(ep.recovered) != 1 {
		t.Fatalf("recovery copies = %v, want one", ep.recovered)
	}
	for name, b := range ep.recovered {
		if !strings.HasPrefix(name, "claudeswitch-recovery-default-") {
			t.Errorf("recovery item %q does not name its profile", name)
		}
		if keychainVaultLike(name) {
			t.Errorf("recovery item %q would be read as an account's vault entry", name)
		}
		if b.ClaudeAIOAuth.AccessToken != "stranger" || b.ClaudeAIOAuth.RefreshToken != "r-stranger" {
			t.Errorf("recovery copy %+v", b.ClaudeAIOAuth)
		}
		if len(b.MCPOAuth) != 0 {
			t.Error("mcpOAuth copied into a recovery item")
		}
	}
}

func keychainVaultLike(name string) bool { return strings.HasPrefix(name, "claudeswitch:") }

// A different known seat is that account's credential, filed under it.
func TestSwapFilesALiveCredentialUnderTheAccountItsSeatBelongsTo(t *testing.T) {
	v, ep, entries, it := swapWorld(t, "c-new", map[string]string{"c-new": "uc@oc"})
	entries["c"] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "c-old", RefreshToken: "r-c-old"},
		Meta: &keychain.Meta{AccountID: "c", AccountUUID: "uc", OrgID: "oc"}}
	o := outgoingA()
	o.SeatOwner = func(seat string) string {
		if seat == "uc@oc" {
			return "c"
		}
		return ""
	}
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatal(err)
	}
	if entries["c"].ClaudeAIOAuth.AccessToken != "c-new" {
		t.Errorf("c's entry = %+v, want the live credential", entries["c"].ClaudeAIOAuth)
	}
	if entries["a"].ClaudeAIOAuth.AccessToken != "a-old" {
		t.Error("another seat's credential was filed under the outgoing account")
	}
	if len(ep.recovered) != 0 {
		t.Errorf("recovery copies %v for an attributed credential", ep.recovered)
	}
}

// Recovery copies are bounded: the oldest is replaced.
func TestRecoveryCopiesAreBounded(t *testing.T) {
	// Straight to the recovery step: a full swap per copy would spend the
	// budget's 2.5 s spacing on each.
	v, ep, _, _ := swapWorld(t, "unused", nil)
	for i := 0; i < MaxRecoveryCopies+3; i++ {
		cred := &keychain.OAuth{AccessToken: "stranger-" + string(rune('a'+i))}
		if err := v.keepRecovery(cred, capturePlan{}, outgoingA(), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if len(ep.recovered) != MaxRecoveryCopies {
		t.Fatalf("%d recovery copies, want %d", len(ep.recovered), MaxRecoveryCopies)
	}
	have := map[string]bool{}
	for _, b := range ep.recovered {
		have[b.ClaudeAIOAuth.AccessToken] = true
	}
	last := "stranger-" + string(rune('a'+MaxRecoveryCopies+2))
	if !have[last] || have["stranger-a"] {
		t.Errorf("kept %v; want the newest kept and the oldest replaced", have)
	}
}

// If the outgoing credential cannot be saved, nothing is overwritten.
func TestSwapIsAbortedWhenTheCaptureCannotBeWritten(t *testing.T) {
	v, _, _, it := swapWorld(t, "a-new", map[string]string{"a-new": seatA})
	oldW := writeEntry
	writeEntry = func(string, *keychain.Blob) error { return errors.New("keychain says no") }
	defer func() { writeEntry = oldW }()

	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err == nil {
		t.Fatal("swapped over a credential it could not save")
	}
	if len(it.writes) != 0 {
		t.Fatalf("live item written %v", it.writes)
	}
}

// lockedItem is an item that knows Claude Code's lock dir and checks, at every
// write, that both locks are held.
type lockedItem struct {
	*recordingItem
	dir        string
	unlockedAt []string
}

func (l *lockedItem) LockDir() string { return l.dir }
func (l *lockedItem) Write(b *keychain.Blob) error {
	p, g := cclock.Paths(l.dir)
	if !exists(p) || !exists(g) {
		l.unlockedAt = append(l.unlockedAt, b.ClaudeAIOAuth.AccessToken)
	}
	return l.recordingItem.Write(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func lockDir(t *testing.T) string {
	d := filepath.Join(t.TempDir(), ".claude")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// Snapshot, write and rollback happen under the locks; the verifying usage
// call does not.
func TestSwapWritesAndRollsBackHoldingClaudeCodesLocks(t *testing.T) {
	v, ep, _, rec := swapWorld(t, "a-old", nil)
	ep.orgOf["b-token"] = "org-somebody-else" // force a rollback
	it := &lockedItem{recordingItem: rec, dir: lockDir(t)}
	var lockedDuringUsage []string
	ep.during = func(r *http.Request) {
		p, g := cclock.Paths(it.dir)
		if exists(p) || exists(g) {
			lockedDuringUsage = append(lockedDuringUsage, r.URL.Path)
		}
	}

	res, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if err == nil || res == nil || !res.RolledBack {
		t.Fatalf("got %+v, %v; want a rollback", res, err)
	}
	if strings.Join(rec.writes, ",") != "b-token,a-old" {
		t.Fatalf("writes = %v, want the swap then the rollback", rec.writes)
	}
	if len(it.unlockedAt) != 0 {
		t.Errorf("written without Claude Code's locks: %v", it.unlockedAt)
	}
	if len(lockedDuringUsage) != 0 {
		t.Errorf("network calls made holding the lock: %v", lockedDuringUsage)
	}
	if p, g := cclock.Paths(it.dir); exists(p) || exists(g) {
		t.Error("a lock was left behind")
	}
}

// Claude Code holding the lock past the bound: give up this tick, write
// nothing, and say so in a way the daemon can tell from a failure.
func TestSwapGivesUpWhileClaudeCodeHoldsTheLock(t *testing.T) {
	v, _, _, rec := swapWorld(t, "a-old", nil)
	it := &lockedItem{recordingItem: rec, dir: lockDir(t)}
	p, _ := cclock.Paths(it.dir)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("got %v, want ErrLockBusy", err)
	}
	if len(rec.writes) != 0 {
		t.Fatalf("wrote %v while Claude Code held the lock", rec.writes)
	}
}

// Refreshing a live account writes its item under the locks too.
func TestRefreshInWritesTheLiveItemHoldingTheLocks(t *testing.T) {
	entries := map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w1-token", RefreshToken: "r-w1"}},
	}
	v := testVault(t, entries, map[string]string{"renewed": "org-w"})
	it := &lockedItem{recordingItem: item("work", "w1-token"), dir: lockDir(t)}
	if _, err := v.RefreshIn(context.Background(), "w1", "", it, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(it.writes, ",") != "renewed" || len(it.unlockedAt) != 0 {
		t.Fatalf("writes %v, unlocked %v", it.writes, it.unlockedAt)
	}
}
