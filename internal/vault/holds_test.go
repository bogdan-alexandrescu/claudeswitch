package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

func vaulted(token, seat string) *keychain.Blob {
	acct, org, _ := strings.Cut(seat, "@")
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: token},
		Meta: &keychain.Meta{AccountUUID: acct, OrgID: org}}
}

// LiveHolds tells "the item holds nothing" apart from "the item could not be
// read". A logged-out profile has no item, and that must not make every
// account look live: that stopped idle refreshes in every pool.
func TestLiveHoldsSeparatesMissingFromUnreadable(t *testing.T) {
	v := testVault(t, map[string]*keychain.Blob{"x": vaulted("x-token", "")}, nil)
	for _, c := range []struct {
		name              string
		item              *recordingItem
		wantHolds, wantOK bool
	}{
		{"missing item", &recordingItem{name: "gone", readErr: fmt.Errorf("absent: %w", keychain.ErrNotFound)}, false, true},
		{"unreadable item", &recordingItem{name: "locked", readErr: errors.New("keychain timed out")}, false, false},
		{"holds it", item("b", "x-token"), true, true},
		{"holds another", item("b", "y-token"), false, true},
	} {
		holds, known := v.LiveHolds(c.item, "x")
		if holds != c.wantHolds || known != c.wantOK {
			t.Errorf("%s: LiveHolds = (%v, %v), want (%v, %v)", c.name, holds, known, c.wantHolds, c.wantOK)
		}
	}
	// IsLiveIn keeps its cautious answer for the unreadable case only.
	if v.IsLiveIn(&recordingItem{readErr: fmt.Errorf("absent: %w", keychain.ErrNotFound)}, "x") {
		t.Error("a missing item is not holding x")
	}
	if !v.IsLiveIn(&recordingItem{readErr: errors.New("timed out")}, "x") {
		t.Error("an unreadable item must still count as possibly live")
	}
}

// HoldsAccount is the check before a live write. A profile where someone
// signed in to X by hand holds a token we never vaulted, so the token alone
// cannot say; the seat behind it can.
func TestHoldsAccountRecognisesAFreshLoginBySeat(t *testing.T) {
	entries := map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")}
	v, ep := testVaultWith(t, entries, nil, map[string]string{
		"x-fresh-login": "ux@org",
		"y-token":       "uy@org",
	})
	ctx := context.Background()

	if holds, known := v.HoldsAccount(ctx, item("b", "x-vaulted"), "x", "ux@org"); !holds || !known {
		t.Errorf("same token: (%v, %v), want held", holds, known)
	}
	if ep.profiles != 0 {
		t.Errorf("a token match needs no call, made %d", ep.profiles)
	}
	if holds, known := v.HoldsAccount(ctx, item("b", "x-fresh-login"), "x", "ux@org"); !holds || !known {
		t.Errorf("fresh login as x: (%v, %v), want held", holds, known)
	}
	// The seat comes from the vault entry when the config pins none.
	if holds, known := v.HoldsAccount(ctx, item("b", "x-fresh-login"), "x", ""); !holds || !known {
		t.Errorf("fresh login, seat from the vault: (%v, %v), want held", holds, known)
	}
	if holds, known := v.HoldsAccount(ctx, item("b", "y-token"), "x", "ux@org"); holds || !known {
		t.Errorf("another account: (%v, %v), want not held, known", holds, known)
	}
	missing := &recordingItem{readErr: fmt.Errorf("absent: %w", keychain.ErrNotFound)}
	if holds, known := v.HoldsAccount(ctx, missing, "x", "ux@org"); holds || !known {
		t.Errorf("missing item: (%v, %v), want not held, known", holds, known)
	}
	// Anything that cannot be settled is unknown, which the caller refuses on.
	if _, known := v.HoldsAccount(ctx, &recordingItem{readErr: errors.New("timed out")}, "x", "ux@org"); known {
		t.Error("an unreadable item must be unknown")
	}
	if _, known := v.HoldsAccount(ctx, item("b", "no-profile"), "x", "ux@org"); known {
		t.Error("a token whose seat cannot be read must be unknown")
	}
}
