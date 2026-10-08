package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// setup generates the config rather than templating it, because the seat pins
// cannot be written in advance — a seat uuid is only knowable after signing in.
// So what it writes must load back exactly.
func TestWrittenConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	in := &Config{
		SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		Cooldown:      Duration{10 * time.Minute},
		MaxSwitchWait: Duration{90 * time.Second},
		RefreshWindow: Duration{time.Hour},
		RefreshProbe:  Duration{24 * time.Hour},
		Priority:      []string{"work", "personal"},
		Accounts: []Account{
			{ID: "work", AccountUUID: "seat-1", OrgID: "org-1",
				Comment: "someone@example.com · Max 20x"},
			{ID: "personal", Reserve: 70,
				AccountUUID: "seat-2", OrgID: "org-2"},
		},
	}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("what we wrote does not load: %v", err)
	}
	if out.SwitchAt != 85 || out.HardFloor != 96 {
		t.Errorf("thresholds lost: %v / %v", out.SwitchAt, out.HardFloor)
	}
	if out.Cooldown.Duration != 10*time.Minute || out.MaxSwitchWait.Duration != 90*time.Second {
		t.Errorf("durations lost: %v / %v", out.Cooldown, out.MaxSwitchWait)
	}
	if out.RefreshWindow.Duration != time.Hour || out.RefreshProbe.Duration != 24*time.Hour {
		t.Errorf("refresh policy lost: %v / %v", out.RefreshWindow, out.RefreshProbe)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("accounts lost: %d", len(out.Accounts))
	}
	// The pins are the point: without them the wrong-account guard cannot work.
	if out.Accounts[0].Seat() != "seat-1@org-1" {
		t.Errorf("seat pin lost: %q", out.Accounts[0].Seat())
	}
	if out.Accounts[1].Reserve != 70 {
		t.Errorf("reserve lost: %v", out.Accounts[1].Reserve)
	}
	if len(out.Priority) != 2 || out.Priority[0] != "work" {
		t.Errorf("priority lost: %v", out.Priority)
	}
}

func TestWrittenConfigIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		RefreshWindow: Duration{time.Hour}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config should be 0600, got %o", fi.Mode().Perm())
	}
}

// `cs config` changes a setting by loading the file, setting one field and
// writing it all back. Anything Write leaves out is therefore deleted by any
// change at all — poll_active was set, reported as written, and gone
// (observed 2026-09-16). This fills every field the file can carry and
// requires all of it to survive.
func TestWriteKeepsEverySetting(t *testing.T) {
	on, off := true, false
	margin, polls, workMargin := 0.0, 5, 20.0
	hotReserve, unseen := 0, 4.5 // 0 is a value, not "unset": no hot reserve
	in := &Config{
		SwitchAt: 80, SwitchAtWeekly: 95, HardFloor: 99, SwitchWhen: "immediate",
		HotThreshold:       88,
		LandingMargin:      &margin,
		BlindFailoverPolls: &polls,
		Models:             []string{"Modelname"},
		Cooldown:           Duration{7 * time.Minute},
		MaxSwitchWait:      Duration{45 * time.Second},
		AutoRefresh:        &off,
		RefreshWindow:      Duration{2 * time.Hour},
		RefreshProbe:       Duration{12 * time.Hour},
		PollActive:         Duration{2 * time.Minute},
		HotReserve:         &hotReserve,
		UnseenCallsPerHour: &unseen,
		PollHot:            Duration{time.Minute},
		PollIdle:           Duration{15 * time.Minute},
		APIBudget:          10,
		Priority:           []string{"work", "personal"},
		Accounts: []Account{
			{ID: "work", AccountUUID: "seat-1",
				OrgID: "org-1", Reserve: 60, Enabled: &on},
			{ID: "personal", AccountUUID: "seat-2",
				OrgID: "org-2", Reserve: 70, Enabled: &off},
		},
		Profiles: []Profile{
			{Name: "work", Dir: "~/.claude-work", Pool: []string{"work"},
				SwitchAt: 75, SwitchAtWeekly: 90, HardFloor: 97, LandingMargin: &workMargin,
				Models: []string{"Othermodel"}, Chrome: "Profile 1"},
			{Name: "default", Pool: []string{"personal"}}, // no dir: CLAUDE_CONFIG_DIR unset
		},
	}
	requireEveryFieldSet(t, reflect.ValueOf(*in), "Config")
	for i, a := range in.Accounts {
		requireEveryFieldSet(t, reflect.ValueOf(a), fmt.Sprintf("Accounts[%d]", i))
	}
	requireEveryFieldSet(t, reflect.ValueOf(in.Profiles[0]), "Profiles[0]")

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("what we wrote does not load: %v\n%s", err, raw)
	}
	out.Path, out.Hash = "", "" // where it was read from, and its content hash
	// Which keys the file carried is bookkeeping for the next Write, not a
	// setting; it is compared by TestWriteDoesNotPinDefaults instead.
	out.set = nil
	if !reflect.DeepEqual(in, out) {
		raw, _ := os.ReadFile(path)
		t.Errorf("settings changed on the way through the file:\n in: %+v\nout: %+v\n%s", *in, *out, raw)
	}
}

// requireEveryFieldSet fails for any field the file carries that the fixture
// left zero. A field added later is then untested until someone sets it here,
// which is the prompt to make Write handle it.
func requireEveryFieldSet(t *testing.T, v reflect.Value, where string) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if tag := f.Tag.Get("toml"); tag == "" || tag == "-" {
			continue
		}
		switch f.Name {
		case "Accounts":
			continue // checked element by element
		case "Label":
			continue // retired: Load refuses a config that sets it
		}
		if v.Field(i).IsZero() {
			t.Errorf("%s.%s is zero in the fixture; set it so Write is tested on it", where, f.Name)
		}
	}
}
