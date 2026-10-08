package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// D20 for `login --direct` (owner decision, lane 7): the profile whose pool a
// new account joins is decided when the login starts, carried through the
// pending-login record, and applied when `login --code` finishes it.
func TestDirectLoginChoosesThePoolUpFront(t *testing.T) {
	withDefault := loadOrFail(t, writeConfig(t, poolConfig))
	noDefault := loadOrFail(t, writeConfig(t, strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)))
	plain := loadOrFail(t, writeConfig(t, baseConfig))

	for _, c := range []struct {
		name      string
		cfg       *config.Config
		id, prof  string
		want      string
		wantError string
	}{
		{"named profile", withDefault, "w2", "work", "work", ""},
		{"named profile, no default", noDefault, "w2", "work", "work", ""},
		{"no flag, default declared: joins default as before", withDefault, "w2", "", "", ""},
		{"no flag, no default: refused, asking for --profile", noDefault, "w2", "", "", "--profile"},
		{"unknown profile", noDefault, "w2", "nowhere", "", "nowhere"},
		{"no profiles at all", plain, "w2", "", "", ""},
		{"an account the config has: no pool edit", noDefault, "w1", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := directPool(c.cfg, c.id, c.prof)
			if c.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantError) {
					t.Fatalf("got %q, %v; want an error mentioning %q", got, err, c.wantError)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestDirectLoginCarriesTheProfileToItsCompletion(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)))
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oauth.ClearPending() })
	if err := loginDirect(cfg, st, nil, "w2", "", "work", false, oauth.Extra{}, "", "work"); err != nil {
		t.Fatal(err)
	}
	_, pend, err := oauth.LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	if pend.Profile != "work" {
		t.Fatalf("pending login records profile %q, want work", pend.Profile)
	}
	if got := completionPool(cfg, pend); got != "work" {
		t.Errorf("completion joins %q's pool, want work's", got)
	}
	// The pool a completion joins is the one the person named, and the edit
	// it makes loads even with no default profile.
	if _, err := recordSeat(cfg, "w2", "work", gotSeat("p2", "o2"), completionPool(cfg, pend)); err != nil {
		t.Fatalf("completing into work's pool: %v", err)
	}
	if owner, _ := loadOrFail(t, cfg.Path).ProfileOf("w2"); owner != "work" {
		t.Errorf("w2 in %q's pool", owner)
	}
}

// A profile removed from the config between the start and the end of a
// login is not written into; the account gets no pool entry.
func TestDirectLoginCompletionIgnoresAProfileNoLongerDeclared(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, poolConfig))
	if got := completionPool(cfg, oauth.Pending{AccountID: "w2", Profile: "gone"}); got != "" {
		t.Errorf("completion would edit the pool of undeclared profile %q", got)
	}
	if got := completionPool(cfg, oauth.Pending{AccountID: "w2"}); got != "" {
		t.Errorf("no profile recorded, but pool %q", got)
	}
}
