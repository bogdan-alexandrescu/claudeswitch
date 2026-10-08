package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 15, second round: the owner's live use and the lane review.

// An account no pool lists belongs to default only by D6. Moving it out of
// default is adding it to the other pool: default's pool has nothing to
// remove (the owner's "profile default's pool does not list aieng-claude1").
func TestPoolMovesAnAccountDefaultHoldsOnlyByD6(t *testing.T) {
	path := appWorld(t, profilesTOML) // a2 is listed nowhere
	var buf bytes.Buffer
	if err := profilePool(&buf, path, "default", "remove", "a2", "work", true); err != nil {
		t.Fatalf("remove --to of an implicit default account: %v", err)
	}
	cfg := loadOrFail(t, path)
	if owner, _ := cfg.ProfileOf("a2"); owner != "work" {
		t.Fatalf("a2 is in %q, want work", owner)
	}
	if m := decodeJSON(t, buf.Bytes()); m["profile"] != "work" || m["changed"] != true {
		t.Errorf("answered %v", m)
	}
	if text := readFile(t, path); strings.Contains(text, `pool = ["a1", "a2"]`) {
		t.Error("default's pool was given the account it was asked to drop")
	}

	// Without --to it is still the usage refusal: it is in default anyway.
	path = appWorld(t, profilesTOML)
	if got := errCode(t, profilePool(&buf, path, "default", "remove", "a2", "", true)); got != codeUsage {
		t.Errorf("implicit, no --to: %q", got)
	}
	// And the live check still applies to the profile it leaves (§3).
	accountLiveIn = func(_ *config.Config, _ *state.State, id string, consider func(string, bool) bool) (string, bool) {
		if id == "a2" && consider("default", false) {
			return "default", true
		}
		return "", false
	}
	if got := errCode(t, profilePool(&buf, path, "default", "remove", "a2", "work", true)); got != codeLive {
		t.Errorf("live in default: %q", got)
	}
}

// The refusal reads cleanly in the app: no `profile "default"'s`.
func TestPoolRefusalWording(t *testing.T) {
	path := appWorld(t, profilesTOML)
	var buf bytes.Buffer
	err := profilePool(&buf, path, "work", "remove", "a1", "", true)
	if got := errCode(t, err); got != codeNotFound {
		t.Fatalf("not listed: %q", got)
	}
	if strings.Contains(err.Error(), `"'s`) {
		t.Errorf("stray quote before 's: %v", err)
	}
	err = profilePool(&buf, path, "work", "add", "a1", "", true)
	if strings.Contains(err.Error(), `"'s`) {
		t.Errorf("stray quote before 's: %v", err)
	}
}

// profile list --json says which accounts a pool lists, so the app knows
// which belong to default only by D6.
func TestProfileListSaysWhatEachPoolLists(t *testing.T) {
	path := appWorld(t, profilesTOML)
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return nil, errors.New("not in tests") }
	var buf bytes.Buffer
	if err := profileListJSON(&buf, path); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	for _, p := range m["profiles"].([]any) {
		pm := p.(map[string]any)
		listed, _ := pm["listed"].([]any)
		switch pm["name"] {
		case "default":
			if len(listed) != 1 || listed[0] != "a1" || len(pm["pool"].([]any)) != 2 {
				t.Errorf("default: listed %v, pool %v", pm["listed"], pm["pool"])
			}
		case "work":
			if len(listed) != 1 || listed[0] != "w1" {
				t.Errorf("work: listed %v", pm["listed"])
			}
		}
	}
}

// Account delete forgets what state recorded about the account's identity.
func TestAccountDeleteDropsTheRecordedEmailAndPlan(t *testing.T) {
	path := appWorld(t, profilesTOML)
	acctSeams.has = func(id string) bool { return id == "w1" }
	acctSeams.deleteVault = func(string) error { return nil }
	st, _ := state.Load("")
	st.SetEmail("w1", "w1@example.com")
	st.SetPlan("w1", "Max 5x")
	st.SetPlan("a1", "Pro")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "w1", true, true); err != nil {
		t.Fatal(err)
	}
	back, _ := state.Load("")
	if back.EmailOf("w1") != "" || back.PlanOf("w1") != "" {
		t.Errorf("w1's email %q and plan %q stayed", back.EmailOf("w1"), back.PlanOf("w1"))
	}
	if back.PlanOf("a1") != "Pro" {
		t.Error("another account's plan went too")
	}
}

// Lane 15 review M2: a profile whose live item never resolved, removed while
// the daemon runs, still guards the account recorded live there — by name,
// as an offline edit would.
func TestDaemonReloadGuardsByNameWhenTheRemovedItemNeverResolved(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	il := r.prof("work")
	il.live, il.unresolved = nil, errors.New("lookup timed out")
	r.d.st.Profile("work").SetActive("w1")

	rewrite(t, path, strings.Replace(reloadTwoProfiles, "\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1))
	r.d.reload(context.Background())

	gs := r.d.st.GhostList()
	if len(gs) != 1 || gs[0].Account != "w1" || gs[0].Service != "" || gs[0].Why != state.GhostRemoved {
		t.Fatalf("ghosts %+v, want w1 guarded by name", gs)
	}
	if holder, _ := liveElsewhereOf(context.Background(), r.d.v, r.d.cfg, r.d.st, "default",
		r.d.ghostTargets(), "w1"); holder == "" {
		t.Error("w1 may be installed elsewhere: the by-name ghost does not guard it")
	}
}

// The same for a re-pointed profile: its new loop must not release the
// by-name ghost of its own old, never-resolved item.
func TestDaemonReloadGuardsByNameWhenTheRepointedItemNeverResolved(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	il := r.prof("work")
	il.live, il.unresolved = nil, errors.New("lookup timed out")
	r.d.st.Profile("work").SetActive("w1")

	rewrite(t, path, strings.Replace(reloadTwoProfiles, "~/.claude-work", "~/.claude-work2", 1))
	r.d.reload(context.Background())

	gs := r.d.st.GhostList()
	if len(gs) != 1 || gs[0].Account != "w1" || gs[0].Service != "" || gs[0].Why != state.GhostRepointed {
		t.Fatalf("ghosts %+v, want w1 guarded by name", gs)
	}
	// The CLI honours it too, though work is still declared.
	if got := allGhosts(r.d.cfg, r.d.st); len(got) != 1 || got[0].Account != "w1" {
		t.Errorf("the CLI sees ghosts %+v", got)
	}
}
