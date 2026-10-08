package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// A profile whose item could not be resolved holds nothing only when the
// resolution said "no such item" (D9, not logged in). A lookup that failed or
// timed out says nothing, and the safe side of that is not writing.
func TestDaemonUnresolvedProfileRefusesUnlessItHasNoItem(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		refused bool
	}{
		{"not logged in", fmt.Errorf("no Claude Code credential found: %w", keychain.ErrNotFound), false},
		{"lookup timed out", errors.New("keychain lookup timed out after 30s"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRigResolving(t, twoProfiles(), true, map[string]error{"default": c.err})
			overWorkPool(r)
			r.d.evaluate(context.Background(), r.prof("work"), "poll")
			swaps := r.v.ops("swap")
			if c.refused && len(swaps) != 0 {
				t.Fatalf("wrote %+v although default's item could not be looked up", swaps)
			}
			if !c.refused && (len(swaps) != 1 || swaps[0].account != "w2") {
				t.Fatalf("swaps = %+v; a profile with no item holds nothing", swaps)
			}
		})
	}
}

// A refusal that persists is reported once — audit row, log line and
// notification — not on every twenty-second tick.
func TestDaemonPersistentRefusalIsReportedOnce(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	overWorkPool(r)
	r.d.st.Profile("default").SetActive("a")
	r.v.holds = map[string]string{"item-default/w2": "yes"}

	for i := 0; i < 4; i++ {
		r.d.evaluate(context.Background(), r.prof("work"), "poll")
	}
	if errs := r.aud.kind("error"); len(errs) != 1 {
		t.Errorf("%d refusal rows over four ticks, want 1", len(errs))
	}
	if n := strings.Count(r.logs.String(), "REFUSED A SWAP"); n != 1 {
		t.Errorf("%d refusal log lines, want 1", n)
	}
	if len(r.nt.sent) != 1 {
		t.Errorf("notifications = %q, want one", r.nt.sent)
	}

	// The evidence changes: that is new, and said again.
	r.v.holds = map[string]string{"item-default/w2": "unknown"}
	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if errs := r.aud.kind("error"); len(errs) != 2 {
		t.Errorf("%d refusal rows after the reason changed, want 2", len(errs))
	}
}

// The cross-pool error is said once per distinct finding, not every sync.
func TestDaemonCrossPoolErrorIsReportedOnce(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	r.d.st.Profile("work").SetActive("w1")
	il := r.prof("default")
	r.d.noteCrossPool(il, "w2", "a")
	r.d.noteCrossPool(il, "w2", "a")
	if n := strings.Count(r.logs.String(), "another profile's pool"); n != 1 {
		t.Fatalf("%d cross-pool lines for one finding, want 1", n)
	}
	r.d.noteCrossPool(il, "w1", "a") // a different account, and live in work too
	if n := strings.Count(r.logs.String(), "another profile's pool"); n != 2 {
		t.Fatalf("%d cross-pool lines after a new finding, want 2", n)
	}
	if n := strings.Count(r.logs.String(), "live in two profiles"); n != 1 {
		t.Fatalf("%d two-profile lines, want 1", n)
	}
	r.d.noteCrossPool(il, "a", "a") // back in its own pool: clears
	r.d.noteCrossPool(il, "w2", "a")
	if n := strings.Count(r.logs.String(), "another profile's pool"); n != 3 {
		t.Fatalf("%d cross-pool lines after it cleared and recurred, want 3", n)
	}
}
