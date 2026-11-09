package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func TestAnAccountHasNoLimitOrPickPriorityUnlessItSetsOne(t *testing.T) {
	c := mustLoad(t, profileAccounts)
	if c.ForAccount("personal") != c {
		t.Error("an account with no limit of its own must see the config unchanged")
	}
	if a := c.Accounts[0]; a.IgnoreWeeklyGTE != 0 || a.PickPriority != 0 {
		t.Errorf("defaults: %+v", a)
	}
}

func TestAnAccountsOwnLimitBeatsItsProfiles(t *testing.T) {
	c := &Config{SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 99,
		Accounts: []Account{{ID: "held", IgnoreWeeklyGTE: 65}, {ID: "plain"}},
		Profiles: []Profile{{Name: "work", SwitchAtWeekly: 90}}}
	p := c.ForProfile("work")

	for _, tc := range []struct {
		id, window string
		want       float64
	}{
		{"held", usage.SevenDayKey, 65},
		{"held", usage.FiveHourKey, 85}, // the week only
		{"plain", usage.SevenDayKey, 90},
		{"nobody", usage.SevenDayKey, 90},
	} {
		if got := p.ForAccount(tc.id).TriggerFor(tc.window); got != tc.want {
			t.Errorf("%s %s: %v, want %v", tc.id, tc.window, got, tc.want)
		}
	}
	if p.ForAccount("held").HardFloor != 99 {
		t.Error("an account's limit must leave the hard floor alone")
	}
	if p.SwitchAtWeekly != 90 {
		t.Error("ForAccount must not change the config it copies")
	}
}

func TestAccountLimitsAndPickPrioritySurviveAWrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		Cooldown:      Duration{10 * time.Minute},
		MaxSwitchWait: Duration{90 * time.Second},
		RefreshWindow: Duration{time.Hour},
		RefreshProbe:  Duration{24 * time.Hour},
		Priority:      []string{"work", "personal"},
		Accounts: []Account{{ID: "work", AccountUUID: "seat-1", OrgID: "org-1"},
			{ID: "personal", IgnoreWeeklyGTE: 65, PickPriority: 2, AccountUUID: "seat-2", OrgID: "org-2"}}}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("what we wrote does not load: %v", err)
	}
	if a := out.Accounts[1]; a.IgnoreWeeklyGTE != 65 || a.PickPriority != 2 {
		t.Errorf("lost in the write: %+v", a)
	}
	if a := out.Accounts[0]; a.IgnoreWeeklyGTE != 0 || a.PickPriority != 0 {
		t.Errorf("appeared in the write: %+v", a)
	}
}

func TestAccountLimitsAreValidated(t *testing.T) {
	for _, line := range []string{"ignore_weekly_gte = 101", "ignore_weekly_gte = -1", "pick_priority = -1"} {
		key := strings.Fields(line)[0]
		_, err := loadString(t, "[[account]]\nid = \"held\"\n"+line+"\n")
		if err == nil || !strings.Contains(err.Error(), "held") || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", line, err)
		}
	}
}
