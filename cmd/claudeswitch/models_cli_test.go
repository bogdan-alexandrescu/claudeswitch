package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// IMPROVEMENTS I6 and I8 in the status line and the JSON outputs.

func addModelLimit(u *usage.Usage, name string, pct float64, active bool) {
	n, p := name, pct
	r := time.Now().Add(2 * 24 * time.Hour)
	u.Limits = append(u.Limits, usage.Limit{Kind: "weekly_scoped", Group: "weekly", Severity: "normal",
		Percent: &p, ResetsAt: &r, IsActive: active,
		Scope: &usage.LimitScope{Model: &usage.LimitModel{DisplayName: &n}}})
}

func statuslineWith(t *testing.T, pct float64, active bool) string {
	t.Helper()
	_, cfg, st := pinWorld(t, 40)
	addModelLimit(st.Get("a").Last, "Modelname", pct, active)
	return captureStdout(t, func() error { return statuslineBody(cfg, st) })
}

// The status line stays short: a model limit appears only when it is the
// binding one or at the weekly trigger.
func TestStatuslineShowsAModelLimitOnlyWhenItMatters(t *testing.T) {
	if got := statuslineWith(t, 12, false); strings.Contains(got, "Modelname") {
		t.Errorf("a quiet model limit must stay off the line:\n%s", got)
	}
	if got := statuslineWith(t, 96, false); !strings.Contains(got, "Modelname 96%") {
		t.Errorf("a model limit at the weekly trigger (95) must be on the line:\n%s", got)
	}
	if got := statuslineWith(t, 50, true); !strings.Contains(got, "Modelname 50%") {
		t.Errorf("the binding model limit must be on the line:\n%s", got)
	}
}

func TestStatuslineNeverShowsPace(t *testing.T) {
	got := statuslineWith(t, 96, false)
	if strings.Contains(got, "pace") || strings.Contains(got, "unused") {
		t.Errorf("the compact status line must not carry the pace view:\n%s", got)
	}
}

func whyAccounts(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	return back.Accounts
}

func TestWhyJSONCarriesPaceAndModelLimits(t *testing.T) {
	_, cfg, st := pinWorld(t, 40)
	addModelLimit(st.Get("a").Last, "Modelname", 12, false)
	accts := whyAccounts(t, whyJSON(cfg, st, time.Now(), ""))
	var a, b map[string]any
	for _, m := range accts {
		switch m["id"] {
		case "a":
			a = m
		case "b":
			b = m
		}
	}
	if a == nil || b == nil {
		t.Fatalf("accounts: %v", accts)
	}
	pace, ok := a["weekly_pace"].(map[string]any)
	if !ok {
		t.Fatalf("no weekly_pace on a: %v", a)
	}
	for _, k := range []string{"expected", "actual", "at_reset", "unused_at_reset", "resets_at"} {
		if _, ok := pace[k]; !ok {
			t.Errorf("weekly_pace lacks %q: %v", k, pace)
		}
	}
	ml, ok := a["model_limits"].([]any)
	if !ok || len(ml) != 1 {
		t.Fatalf("model_limits on a = %v", a["model_limits"])
	}
	one := ml[0].(map[string]any)
	if one["model"] != "Modelname" || one["percent"] != 12.0 || one["counted"] != false {
		t.Errorf("model_limits[0] = %v", one)
	}
	if _, present := b["model_limits"]; present {
		t.Errorf("an account with no model limits gets no model_limits key: %v", b)
	}
}
