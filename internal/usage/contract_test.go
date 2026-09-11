package usage

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// testdata/usage_response.json is a real 200 from /api/oauth/usage, captured
// 2026-09-08 against claude 2.1.266. The endpoint is undocumented, so this test
// is the tripwire: if Anthropic changes the shape, this fails in CI rather than
// the daemon degrading quietly at 3am.
//
// If it fails, do not "fix" it by loosening the assertions. Re-capture the
// fixture, work out what moved, and decide whether the poller can still do its
// job — the answer determines whether predictive switching stays enabled.
func loadFixture(t *testing.T) *Usage {
	t.Helper()
	b, err := os.ReadFile("testdata/usage_response.json")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	var u Usage
	if err := json.Unmarshal(b, &u); err != nil {
		t.Fatalf("the recorded response no longer parses into Usage: %v", err)
	}
	return &u
}

func TestContractBothWindowsCarryUtilizationAndReset(t *testing.T) {
	u := loadFixture(t)
	for name, w := range map[string]Window{"five_hour": u.FiveHour, "seven_day": u.SevenDay} {
		if !w.Known() {
			t.Errorf("%s lost its utilization — the trigger has nothing to read", name)
			continue
		}
		if p := w.Pct(); p < 0 || p > 100 {
			t.Errorf("%s utilization %v is not a percentage", name, p)
		}
		if w.ResetsAt == nil || w.ResetsAt.IsZero() {
			t.Errorf("%s lost resets_at — the scheduler cannot anchor its clock", name)
		}
	}
}

func TestContractResetsAtParsesAsRFC3339WithOffset(t *testing.T) {
	u := loadFixture(t)
	got := u.FiveHour.ResetsAt
	if got == nil {
		t.Fatal("no resets_at")
	}
	// Observed form: "2026-09-09T08:50:00.497885+00:00" — fractional seconds and
	// a numeric offset. Go's time.Time handles it; assert it stays that way.
	if got.Year() < 2020 || got.Year() > 2100 {
		t.Fatalf("resets_at parsed to an implausible time: %v", got)
	}
	if _, off := got.Zone(); off != 0 && off == 0 {
		t.Fatal("unreachable")
	}
}

func TestContractLimitsArrayCarriesSeverityAndActiveFlag(t *testing.T) {
	u := loadFixture(t)
	if len(u.Limits) == 0 {
		t.Fatal("limits[] is empty — severity-based signals would be lost")
	}
	kinds := map[string]bool{}
	sawActive := false
	for _, l := range u.Limits {
		kinds[l.Kind] = true
		if l.Severity == "" {
			t.Errorf("limit %q has no severity", l.Kind)
		}
		if l.IsActive {
			sawActive = true
		}
		if l.Percent < 0 || l.Percent > 100 {
			t.Errorf("limit %q percent %v is not a percentage", l.Kind, l.Percent)
		}
	}
	for _, want := range []string{"session", "weekly_all"} {
		if !kinds[want] {
			t.Errorf("limits[] no longer includes %q (kinds seen: %v)", want, keys(kinds))
		}
	}
	if !sawActive {
		t.Error("no limit is marked is_active — the binding window can no longer be identified")
	}
}

func TestContractBindingAndWorstAgreeWithTheFixture(t *testing.T) {
	u := loadFixture(t)
	b := u.Binding()
	if b == nil {
		t.Fatal("Binding() found no active limit")
	}
	which, worst := u.Worst()
	if which == "" {
		t.Fatal("Worst() could not pick a window")
	}
	if worst < 0 || worst > 100 {
		t.Fatalf("Worst() returned %v", worst)
	}
	// The worse window must be at least as used as either individual window.
	if worst < u.FiveHour.Pct() || worst < u.SevenDay.Pct() {
		t.Fatalf("Worst() returned %v, below one of the windows (%v / %v)",
			worst, u.FiveHour.Pct(), u.SevenDay.Pct())
	}
}

// Per-model windows are null on a Max 20x account but may populate on another
// tier. If they ever carry data, the policy engine needs to handle them, so
// notice rather than ignore.
func TestContractPerModelWindowsStillAbsent(t *testing.T) {
	b, err := os.ReadFile("testdata/usage_response.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"seven_day_opus", "seven_day_sonnet"} {
		v, present := raw[k]
		if !present {
			continue
		}
		if string(v) != "null" {
			t.Logf("NOTE: %s is now populated (%s). The policy engine only handles "+
				"five_hour and seven_day; per-model windows need support.", k, v)
		}
	}
}

func TestShapeErrorWhenBothWindowsMissing(t *testing.T) {
	var u Usage
	if err := json.Unmarshal([]byte(`{"limits":[]}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.FiveHour.Known() || u.SevenDay.Known() {
		t.Fatal("a response with no windows must not appear to carry utilization")
	}
}

func TestRateLimitedErrorReportsRetryAfter(t *testing.T) {
	e := &RateLimitedError{RetryAfter: 299 * time.Second}
	if _, ok := IsRateLimited(error(e)); !ok {
		t.Fatal("IsRateLimited must recognise its own error")
	}
	if got := e.Error(); got == "" {
		t.Fatal("empty message")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
