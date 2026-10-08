package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// IMPROVEMENTS I6: limits[] carries per-model weekly limits as
// {"kind":"weekly_scoped","scope":{"model":{"display_name":...}}}
// (GROUND_TRUTH §42). They are parsed, persisted and classified; anything this
// code does not recognise is unknown, never "fine".

func parseUsage(t *testing.T, s string) *Usage {
	t.Helper()
	var u Usage
	if err := json.Unmarshal([]byte(s), &u); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &u
}

const scopedReading = `{
  "five_hour": {"utilization": 20, "resets_at": "2026-09-09T08:00:00Z"},
  "seven_day": {"utilization": 40, "resets_at": "2026-09-12T08:00:00Z"},
  "limits": [
    {"kind": "session", "group": "session", "percent": 20, "severity": "normal", "resets_at": "2026-09-09T08:00:00Z", "scope": null, "is_active": true},
    {"kind": "weekly_all", "group": "weekly", "percent": 40, "severity": "normal", "resets_at": "2026-09-12T08:00:00Z", "scope": null, "is_active": false},
    {"kind": "weekly_scoped", "group": "weekly", "percent": 97, "severity": "warning", "resets_at": "2026-09-11T08:00:00Z",
     "scope": {"model": {"id": null, "display_name": "Modelname"}, "surface": null}, "is_active": false},
    {"kind": "weekly_scoped", "group": "weekly", "percent": null, "severity": "normal", "resets_at": null,
     "scope": {"model": {"id": null, "display_name": "Othermodel"}, "surface": null}, "is_active": false},
    {"kind": "weekly_scoped", "group": "weekly", "percent": 5, "severity": "normal", "resets_at": null,
     "scope": {"model": null, "surface": {"name": "somewhere"}}, "is_active": false},
    {"kind": "monthly_new", "group": "monthly", "percent": 12, "severity": "normal", "resets_at": null, "scope": null, "is_active": false}
  ]
}`

func TestLimitsClassifyKnownKindsAndPerModelWeekly(t *testing.T) {
	u := parseUsage(t, scopedReading)
	want := []LimitClass{LimitSession, LimitWeekly, LimitModelWeekly, LimitModelWeekly, LimitUnknown, LimitUnknown}
	if len(u.Limits) != len(want) {
		t.Fatalf("parsed %d limits, want %d", len(u.Limits), len(want))
	}
	for i, l := range u.Limits {
		if got := l.Class(); got != want[i] {
			t.Errorf("limits[%d] (%s) classified %v, want %v", i, l.Kind, got, want[i])
		}
	}
	if got := u.Limits[2].ModelName(); got != "Modelname" {
		t.Errorf("ModelName() = %q, want the scope's display_name", got)
	}
}

// A percent the API sent as null, or did not send, is unknown. Decoding it
// into a float64 made it 0 — the most reassuring figure there is.
func TestALimitWithoutAPercentIsUnknownNotZero(t *testing.T) {
	u := parseUsage(t, scopedReading)
	if l := u.Limits[3]; l.Known() {
		t.Errorf("a null percent reads as known (%v)", l.Pct())
	}
	if l := u.Limits[2]; !l.Known() || l.Pct() != 97 {
		t.Errorf("percent 97 read back as known=%v %v", l.Known(), l.Pct())
	}
	u = parseUsage(t, `{"five_hour":{"utilization":1},"limits":[{"kind":"session","group":"session"}]}`)
	if u.Limits[0].Known() {
		t.Error("a missing percent reads as known")
	}
}

func TestModelWeeklyListsOnlyPerModelEntries(t *testing.T) {
	u := parseUsage(t, scopedReading)
	got := u.ModelWeekly()
	if len(got) != 2 || got[0].ModelName() != "Modelname" || got[1].ModelName() != "Othermodel" {
		t.Fatalf("ModelWeekly() = %+v", got)
	}
	unk := u.UnknownLimits()
	if len(unk) != 2 || unk[0].Kind != "weekly_scoped" || unk[1].Kind != "monthly_new" {
		t.Fatalf("UnknownLimits() = %+v", unk)
	}
	if d := unk[0].Describe(); !strings.Contains(d, "weekly_scoped") {
		t.Errorf("an unknown scope's description must name its kind, got %q", d)
	}
}

// state.json stores the Usage as Go encodes it; the scope must survive the
// round trip (the macOS app reads scope.model.display_name from it), and so
// must a null percent.
func TestLimitsSurviveTheStateRoundTrip(t *testing.T) {
	u := parseUsage(t, scopedReading)
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"display_name":"Modelname"`) {
		t.Errorf("scope.model.display_name was not persisted:\n%s", b)
	}
	back := parseUsage(t, string(b))
	if len(back.Limits) != len(u.Limits) {
		t.Fatalf("round trip lost limits: %d → %d", len(u.Limits), len(back.Limits))
	}
	for i := range u.Limits {
		if back.Limits[i].Class() != u.Limits[i].Class() || back.Limits[i].Known() != u.Limits[i].Known() ||
			back.Limits[i].ModelName() != u.Limits[i].ModelName() {
			t.Errorf("limits[%d] changed in the round trip: %+v → %+v", i, u.Limits[i], back.Limits[i])
		}
	}
}

func TestWithModelsEmptyIsTheReadingUnchanged(t *testing.T) {
	u := parseUsage(t, scopedReading)
	eff, from, unreadable := u.WithModels(nil)
	if eff != u || from != "" || unreadable != "" {
		t.Fatalf("no configured models must leave the reading alone, got %p/%q/%q", eff, from, unreadable)
	}
}

// A counted model's weekly limit stands in for the weekly window when it is
// the higher of the two, with its own reset.
func TestWithModelsCountsAHigherModelLimitAsTheWeeklyWindow(t *testing.T) {
	u := parseUsage(t, scopedReading)
	eff, from, unreadable := u.WithModels([]string{"modelname"}) // matched case-insensitively
	if unreadable != "" {
		t.Fatalf("unreadable = %q", unreadable)
	}
	if from != "Modelname" {
		t.Fatalf("from = %q, want the model that set the weekly figure", from)
	}
	if eff.SevenDay.Pct() != 97 {
		t.Errorf("effective weekly = %v, want the model's 97", eff.SevenDay.Pct())
	}
	if eff.SevenDay.ResetsAt == nil || !eff.SevenDay.ResetsAt.Equal(time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("effective weekly reset = %v, want the model limit's", eff.SevenDay.ResetsAt)
	}
	if u.SevenDay.Pct() != 40 {
		t.Error("WithModels modified the reading it was given")
	}
}

func TestWithModelsKeepsTheWeeklyWindowWhenItIsHigher(t *testing.T) {
	u := parseUsage(t, strings.Replace(scopedReading, `"percent": 97`, `"percent": 10`, 1))
	eff, from, _ := u.WithModels([]string{"Modelname"})
	if from != "" || eff.SevenDay.Pct() != 40 {
		t.Errorf("a model under the weekly window must not replace it: from=%q weekly=%v", from, eff.SevenDay.Pct())
	}
}

// An account that has no limit for a counted model is held to its weekly
// window alone; one that reports the limit without a figure is unknown.
func TestWithModelsAbsentIsNoLimitButUnreadableIsUnknown(t *testing.T) {
	u := parseUsage(t, scopedReading)
	if _, from, unreadable := u.WithModels([]string{"Absent"}); from != "" || unreadable != "" {
		t.Errorf("a model the account has no limit for: from=%q unreadable=%q", from, unreadable)
	}
	if _, _, unreadable := u.WithModels([]string{"Othermodel"}); unreadable != "Othermodel" {
		t.Errorf("a counted model with a null percent must be reported unreadable, got %q", unreadable)
	}
}
