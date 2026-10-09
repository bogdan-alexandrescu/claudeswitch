package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// IMPROVEMENTS F12: `doctor --json` was added without changing a byte of the
// text doctor prints, nor its exit status. These goldens were written from
// the code before --json; CLAUDESWITCH_UPDATE_GOLDEN=1 rewrites them, which
// must only ever be done on purpose. The profile rows differ by platform
// (Linux checks a credential file, macOS names a keychain item), so each
// platform has its own files.

const doctorGoldenConfig = `switch_at = 80
switch_at_weekly = 95
hard_floor = 90

[[account]]
id = "a1"
account_uuid = "u1"
org_id = "o1"

[[account]]
id = "a2"

[[account]]
id = "a3"

[[profile]]
name = "default"
pool = ["a1", "a2"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["a3"]
`

type doctorScenario struct {
	name string
	cfg  string
	deep bool
	// running makes daemonRunning true, with this daemon version recorded
	// (this binary is 0.5.2).
	running string
	setup   func(t *testing.T, w *doctorWorld, d *doctorDeps)
}

func doctorScenarios() []doctorScenario {
	return []doctorScenario{
		{name: "healthy", cfg: doctorGoldenConfig, running: "0.5.2", setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
			d.recovery = func(*config.Config) []vault.RecoveryItem {
				return []vault.RecoveryItem{{Slot: "r1", KeptAt: time.Now().Add(-2*time.Hour - time.Minute)}}
			}
			d.ghosts = func() []*state.Ghost {
				return []*state.Ghost{{Profile: "old", Account: "a2", Why: "removed"}}
			}
		}},
		{name: "broken", cfg: doctorGoldenConfig, running: "0.5.1", setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
			d.readLive = func() (*keychain.Blob, error) { return nil, errors.New("keychain says no") }
			d.duplicates = func(*config.Config, *state.State) string { return "a1 and a2 hold the same credential" }
			os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".claude", "projects"))
		}},
		{name: "usage-limited", cfg: "auto_refresh = false\n", setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
			d.fetchUsage = func(context.Context, string) (*usage.Usage, error) {
				return nil, &usage.RateLimitedError{RetryAfter: time.Minute}
			}
			writeStatusline(t)
		}},
		{name: "usage-failed", cfg: "auto_refresh = false\napi_budget = 2\n[[account]]\nid = \"a\"\n[[account]]\nid = \"b\"\n",
			setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
				d.fetchUsage = func(context.Context, string) (*usage.Usage, error) { return nil, errors.New("500") }
			}},
		{name: "no-config", setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
			w.cfgPath = filepath.Join(filepath.Dir(w.cfgPath), "missing.toml")
		}},
		{name: "verify", cfg: doctorGoldenConfig + "\n[[account]]\nid = \"a4\"\n", deep: true,
			setup: func(t *testing.T, w *doctorWorld, d *doctorDeps) {
				d.verifyOne = func(id string) (bool, *usage.Profile, error) {
					pr := &usage.Profile{}
					pr.Account.Email = id + "@example.com"
					switch id {
					case "a1":
						pr.Account.UUID, pr.Organization.UUID = "u2", "o1"
					case "a2":
						return true, nil, errors.New("401 Unauthorized")
					case "a4":
						return false, nil, nil
					}
					return true, pr, nil
				}
			}},
	}
}

// writeStatusline sets claudeswitch's status line in ~/.claude/settings.json.
func writeStatusline(t *testing.T) {
	t.Helper()
	p := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	body := `{"statusLine":{"type":"command","command":"claudeswitch statusline"}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runScenario runs one scenario through run, which is runDoctor or its
// JSON form; it returns what run printed, normalised, and its error.
func runScenario(t *testing.T, sc doctorScenario, run func(cfgPath string, deep bool) (string, error)) (string, error) {
	t.Helper()
	w := newDoctorWorld(t, sc.cfg)
	if sc.cfg == "" {
		os.Remove(w.cfgPath)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	oldSeams, oldRunning, oldBuild := doctorSeams, daemonRunning, currentBuild
	t.Cleanup(func() { doctorSeams, daemonRunning, currentBuild = oldSeams, oldRunning, oldBuild })
	currentBuild = func() buildStamp { return buildStamp{Version: "0.5.2"} }
	d := doctorDeps{
		readLive: w.readLive, fetchUsage: w.fetch, resolve: w.resolve,
		duplicates: w.dup, verifyOne: w.verify,
		vaulted: func(*config.Config) int { return 2 },
		// Nothing vaulted unless a scenario says so: a nil vaultEntry reads
		// the real vault, which is the macOS keychain off Linux.
		vaultEntry: func(string) (*keychain.OAuth, bool) { return nil, false },
	}
	daemonRunning = func() bool { return sc.running != "" }
	if sc.running != "" {
		st, _ := state.Load("")
		st.DaemonVersion = sc.running
		if err := st.SaveAs(state.OwnerDaemon); err != nil {
			t.Fatal(err)
		}
	}
	if sc.setup != nil {
		sc.setup(t, w, &d)
	}
	doctorSeams = d
	out, err := run(w.cfgPath, sc.deep)
	out = strings.ReplaceAll(out, filepath.Dir(w.cfgPath), "$CFGDIR")
	out = strings.ReplaceAll(out, os.Getenv("HOME"), "$HOME")
	out = strings.ReplaceAll(out, keychain.Backend, "$BACKEND")
	return out, err
}

func TestDoctorTextGolden(t *testing.T) {
	for _, sc := range doctorScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			got, err := runScenario(t, sc, func(cfgPath string, deep bool) (string, error) {
				var b strings.Builder
				err := runDoctor(&b, cfgPath, deep)
				return b.String(), err
			})
			exit := "exit: 0\n"
			if err != nil {
				exit = "exit: 1 (" + err.Error() + ")\n"
			}
			checkDoctorGolden(t, sc.name, got+exit)
		})
	}
}

func checkDoctorGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "doctor", name+"."+runtime.GOOS+".golden")
	if os.Getenv("CLAUDESWITCH_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("no %s golden for %s yet", name, runtime.GOOS)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("doctor %s output changed.\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}
