package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// The headline says what happens next, which depends on the daemon: only a
// live one rotates. A pin turns rotation off, and claude.ai work holds it.

func headlineOf(t *testing.T, o Options) (string, string) {
	t.Helper()
	var b bytes.Buffer
	Status(&b, o)
	out := b.String()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "live at ") {
			return line, out
		}
	}
	t.Fatalf("no headline:\n%s", out)
	return "", out
}

func TestTheHeadlineSaysAPinTurnsRotationOff(t *testing.T) {
	cfg, st := judgedWorld()
	st.Profiles["default"].Pinned = "live"
	dec := policy.Decide(policy.Input{Cfg: cfg, St: st, Now: time.Now(), Pinned: "live"})
	head, out := headlineOf(t, Options{Cfg: cfg, St: st, DaemonOwns: true, Decision: &dec})
	if !strings.Contains(head, "pinned") || strings.Contains(head, "before it rotates") {
		t.Errorf("a pinned profile does not rotate, and the headline must say it is pinned:\n%s", out)
	}
}

func TestTheHeadlineSaysWhatTheDaemonWillDo(t *testing.T) {
	cfg, st := judgedWorld()
	switchTo := &policy.Decision{Kind: policy.Switch, Target: "idle", Reason: "over"}
	for _, tc := range []struct {
		name       string
		owns, live bool
		dec        *policy.Decision
		want, not  string
	}{
		{"live", true, true, switchTo, "rotating to idle", "would"},
		{"dry-run", true, false, switchTo, "would rotate to idle", "rotating to"},
		{"no daemon", false, false, switchTo, "would rotate to idle", "rotating to"},
		{"held", true, true, &policy.Decision{Kind: policy.Switch, Target: "idle", Held: "claude.ai work 2m ago",
			HeldUntil: time.Now().Add(3 * time.Minute)}, "waiting to rotate to idle", "· rotating to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st.DaemonLive = tc.live
			head, out := headlineOf(t, Options{Cfg: cfg, St: st, DaemonOwns: tc.owns, Decision: tc.dec})
			if !strings.Contains(head, tc.want) || strings.Contains(head, tc.not) {
				t.Errorf("want %q (and not %q) in the headline:\n%s", tc.want, tc.not, out)
			}
		})
	}
}

// Piped output has no brand header, so the footer is the only place that
// says whether anything acts on what the view shows.
func TestEveryFooterSaysWhatTheDaemonIsDoing(t *testing.T) {
	cfg, st := judgedWorld()
	for _, tc := range []struct {
		name       string
		owns, live bool
		want       string
	}{
		{"live", true, true, "daemon live"},
		{"dry-run", true, false, "daemon in dry-run"},
		{"stopped", false, false, "no daemon running"},
	} {
		st.DaemonLive = tc.live
		for _, detail := range []bool{false, true} {
			var b bytes.Buffer
			Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: tc.owns, Detail: detail})
			if !strings.Contains(b.String(), tc.want) {
				t.Errorf("%s, detail %v: the footer lacks %q:\n%s", tc.name, detail, tc.want, b.String())
			}
		}
		var b bytes.Buffer
		SharedFooter(&b, Options{Cfg: cfg, St: st, DaemonOwns: tc.owns})
		if !strings.Contains(b.String(), tc.want) {
			t.Errorf("%s: the shared footer lacks %q:\n%s", tc.name, tc.want, b.String())
		}
	}
}
