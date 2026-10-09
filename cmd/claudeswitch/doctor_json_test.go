package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// IMPROVEMENTS F12: `doctor --json` is {checks: [{name, status, message,
// fix}]}, every check doctor prints, with fix one of the known actions.

type doctorJSONOut struct {
	Checks []map[string]any `json:"checks"`
	Failed *int             `json:"failed"`
}

func runDoctorJSONScenario(t *testing.T, sc doctorScenario) doctorJSONOut {
	t.Helper()
	got, err := runScenario(t, sc, func(cfgPath string, deep bool) (string, error) {
		var b bytes.Buffer
		err := doctorJSON(&b, cfgPath, deep)
		return b.String(), err
	})
	// The report is the answer: failed checks are in it, not in the exit.
	if err != nil {
		t.Fatalf("doctor --json returned %v", err)
	}
	var out doctorJSONOut
	dec := json.NewDecoder(strings.NewReader(got))
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if dec.More() {
		t.Fatalf("more than one JSON object:\n%s", got)
	}
	knownFix := func(v any) bool {
		if v == nil {
			return true
		}
		s, _ := v.(string)
		return s == "daemon restart" || s == "statusline install" || s == "keychain allow" ||
			(strings.HasPrefix(s, "signin ") && len(s) > len("signin "))
	}
	for _, c := range out.Checks {
		for _, k := range []string{"name", "status", "message", "fix"} {
			if _, ok := c[k]; !ok {
				t.Errorf("check lacks %q: %v", k, c)
			}
		}
		switch c["status"] {
		case "ok", "warn", "fail":
		default:
			t.Errorf("status %v: %v", c["status"], c)
		}
		if !knownFix(c["fix"]) {
			t.Errorf("unknown fix %v: %v", c["fix"], c)
		}
	}
	return out
}

func findCheck(out doctorJSONOut, name, account string) map[string]any {
	for _, c := range out.Checks {
		if c["name"] == name && (account == "" || c["account"] == account) {
			return c
		}
	}
	return nil
}

func scenarioNamed(name string) doctorScenario {
	for _, sc := range doctorScenarios() {
		if sc.name == name {
			return sc
		}
	}
	panic(name)
}

func TestDoctorJSONHealthy(t *testing.T) {
	out := runDoctorJSONScenario(t, scenarioNamed("healthy"))
	if out.Failed == nil || *out.Failed != 0 {
		t.Errorf("failed = %v", out.Failed)
	}
	for _, c := range out.Checks {
		if c["status"] == "fail" {
			t.Errorf("a failure with nothing broken: %v", c)
		}
	}
	if c := findCheck(out, "daemon", ""); c == nil || c["status"] != "ok" || c["fix"] != nil {
		t.Errorf("daemon: %v", c)
	}
	sl := findCheck(out, "status line", "")
	if sl == nil || sl["status"] != "warn" || sl["fix"] != "statusline install" || sl["level"] != "info" {
		t.Errorf("status line not set: %v", sl)
	}
	if c := findCheck(out, "ghosts", ""); c == nil || c["status"] != "warn" ||
		len(c["details"].([]any)) != 1 {
		t.Errorf("ghosts: %v", c)
	}
	var profiles []any
	for _, c := range out.Checks {
		if c["name"] == "profile" {
			profiles = append(profiles, c["profile"])
		}
	}
	if len(profiles) != 2 || profiles[0] != "default" || profiles[1] != "work" {
		t.Errorf("profile checks name their profile: %v", profiles)
	}
	if c := findCheck(out, "config", ""); c == nil || c["status"] != "ok" ||
		!strings.HasSuffix(c["message"].(string), "config.toml") {
		t.Errorf("config: %v", c)
	}
}

func TestDoctorJSONBroken(t *testing.T) {
	old := doctorGOOS
	t.Cleanup(func() { doctorGOOS = old })
	doctorGOOS = "darwin"
	out := runDoctorJSONScenario(t, scenarioNamed("broken"))
	if out.Failed == nil || *out.Failed != 4 {
		t.Errorf("failed = %v, want the 4 the text counts", out.Failed)
	}
	if c := findCheck(out, "daemon", ""); c == nil || c["status"] != "fail" || c["fix"] != "daemon restart" {
		t.Errorf("an older daemon: %v", c)
	}
	cred := findCheck(out, "credentials", "")
	if cred == nil || cred["status"] != "fail" || cred["fix"] != "keychain allow" ||
		cred["details"].([]any)[0] != "keychain says no" {
		t.Errorf("unreadable live credential: %v", cred)
	}
	if c := findCheck(out, "vault entries", ""); c == nil || c["status"] != "fail" || c["fix"] != nil {
		t.Errorf("vault entries: %v", c)
	}
	if c := findCheck(out, "transcripts", ""); c == nil || c["status"] != "fail" {
		t.Errorf("transcripts: %v", c)
	}

	// On Linux the live credential is a file: no keychain to allow.
	doctorGOOS = "linux"
	out = runDoctorJSONScenario(t, scenarioNamed("broken"))
	if c := findCheck(out, "credentials", ""); c == nil || c["fix"] != nil {
		t.Errorf("linux credentials fix: %v", c)
	}
}

func TestDoctorJSONVerifyNamesTheAccountToSignIn(t *testing.T) {
	out := runDoctorJSONScenario(t, scenarioNamed("verify"))
	for id, want := range map[string]string{"a1": "fail", "a2": "fail", "a3": "ok", "a4": "warn"} {
		c := findCheck(out, "credential", id)
		if c == nil || c["status"] != want {
			t.Errorf("credential %s: %v, want %s", id, c, want)
			continue
		}
		fix := any("signin " + id)
		if want == "ok" {
			fix = nil
		}
		if c["fix"] != fix {
			t.Errorf("credential %s fix = %v, want %v", id, c["fix"], fix)
		}
	}
	if c := findCheck(out, "credentials", ""); c == nil {
		t.Error("no credentials check")
	}
	// The verifying… header is not a check.
	for _, c := range out.Checks {
		if strings.Contains(c["message"].(string), "verifying") {
			t.Errorf("the header row became a check: %v", c)
		}
	}
}

// Each vaulted account's refresh token: within F5's 5 days, gone, or
// expired is a warning whose fix is signing that account in.
func TestDoctorJSONRefreshTokens(t *testing.T) {
	sc := scenarioNamed("healthy")
	base := sc.setup
	now := time.Now()
	sc.setup = func(t *testing.T, w *doctorWorld, d *doctorDeps) {
		base(t, w, d)
		d.vaultEntry = func(id string) (*keychain.OAuth, bool) {
			switch id {
			case "a1":
				return &keychain.OAuth{RefreshToken: "r", RefreshTokenExpiresAt: now.Add(2 * 24 * time.Hour).UnixMilli()}, true
			case "a2":
				return &keychain.OAuth{}, true
			case "a3":
				return &keychain.OAuth{RefreshToken: "r", RefreshTokenExpiresAt: now.Add(30 * 24 * time.Hour).UnixMilli()}, true
			}
			return nil, false
		}
	}
	out := runDoctorJSONScenario(t, sc)
	for id, want := range map[string]string{"a1": "warn", "a2": "warn", "a3": "ok"} {
		c := findCheck(out, "refresh token", id)
		if c == nil || c["status"] != want {
			t.Errorf("refresh token %s: %v, want %s", id, c, want)
			continue
		}
		if want == "warn" && c["fix"] != "signin "+id {
			t.Errorf("refresh token %s fix = %v", id, c["fix"])
		}
	}

	// The text form shows the same accounts, as before.
	got, _ := runScenario(t, sc, func(cfgPath string, deep bool) (string, error) {
		var b strings.Builder
		err := runDoctor(&b, cfgPath, deep)
		return b.String(), err
	})
	if !strings.Contains(got, "└ a2                 access unknown · refresh token NONE — cannot be renewed") {
		t.Errorf("text:\n%s", got)
	}
}
