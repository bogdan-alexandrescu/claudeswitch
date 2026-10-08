package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// doctorWorld is a machine on which every doctor check passes, with nothing
// reaching the keychain, the network or the person's own files. Each test
// breaks one thing.
type doctorWorld struct {
	cfgPath  string
	readLive func() (*keychain.Blob, error)
	fetch    func(ctx context.Context, token string) (*usage.Usage, error)
	resolve  func(config.Profile) (profile.Resolved, error)
	dup      func(*config.Config, *state.State) string
	verify   func(id string) (vaulted bool, pr *usage.Profile, err error)
}

func newDoctorWorld(t *testing.T, cfgBody string) *doctorWorld {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	dir := filepath.Join(h, ".claude")
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(cred, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return &doctorWorld{
		cfgPath: cfgPath,
		readLive: func() (*keychain.Blob, error) {
			return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok"}}, nil
		},
		fetch: func(context.Context, string) (*usage.Usage, error) { return &usage.Usage{}, nil },
		resolve: func(in config.Profile) (profile.Resolved, error) {
			return profile.Resolved{Name: in.Name, Dir: dir, CredentialFile: cred,
				Service: "Claude Code-credentials", Pool: in.Pool}, nil
		},
		dup: func(*config.Config, *state.State) string { return "" },
		verify: func(string) (bool, *usage.Profile, error) {
			return true, &usage.Profile{}, nil
		},
	}
}

func unsetenvT(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

func (w *doctorWorld) run(t *testing.T, deep bool) (string, error) {
	t.Helper()
	old := doctorSeams
	t.Cleanup(func() { doctorSeams = old })
	doctorSeams = doctorDeps{
		readLive: w.readLive, fetchUsage: w.fetch, resolve: w.resolve,
		duplicates: w.dup, verifyOne: w.verify,
		vaulted: func(*config.Config) int { return 0 },
	}
	var b bytes.Buffer
	err := runDoctor(&b, w.cfgPath, deep)
	return b.String(), err
}

// No accounts, so nothing anywhere asks the vault for an entry.
const doctorBaseConfig = "auto_refresh = false\n"

func TestDoctorPassesWhenNothingFails(t *testing.T) {
	// A warning (hard_floor under switch_at_weekly) must not fail it.
	w := newDoctorWorld(t, doctorBaseConfig+"switch_at = 80\nswitch_at_weekly = 95\nhard_floor = 90\n")
	out, err := w.run(t, false)
	if err != nil {
		t.Fatalf("doctor failed with nothing broken: %v\n%s", err, out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Fatalf("a FAIL line without a failing exit:\n%s", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Fatalf("setup: expected a warning line:\n%s", out)
	}
}

func TestDoctorRateLimitIsNotAFailure(t *testing.T) {
	w := newDoctorWorld(t, doctorBaseConfig)
	w.fetch = func(context.Context, string) (*usage.Usage, error) {
		return nil, &usage.RateLimitedError{RetryAfter: time.Minute}
	}
	if out, err := w.run(t, false); err != nil {
		t.Fatalf("a rate limit is a warning, not a failure: %v\n%s", err, out)
	}
}

// D14: every FAIL line fails the exit, not only the profile checks.
func TestDoctorExitsNonZeroOnAnyFail(t *testing.T) {
	cases := []struct {
		name   string
		cfg    string
		deep   bool
		break_ func(w *doctorWorld)
	}{
		{name: "config", break_: func(w *doctorWorld) { w.cfgPath = filepath.Join(filepath.Dir(w.cfgPath), "missing.toml") }},
		{name: "config does not parse", break_: func(w *doctorWorld) {
			os.WriteFile(w.cfgPath, []byte("switch_at = [\n"), 0o600)
		}},
		{name: "credentials", break_: func(w *doctorWorld) {
			w.readLive = func() (*keychain.Blob, error) { return nil, errors.New("keychain says no") }
		}},
		{name: "usage api", break_: func(w *doctorWorld) {
			w.fetch = func(context.Context, string) (*usage.Usage, error) { return nil, errors.New("500") }
		}},
		{name: "vault entries", break_: func(w *doctorWorld) {
			w.dup = func(*config.Config, *state.State) string { return "a and b hold the same credential" }
		}},
		{name: "poll cadence", cfg: "api_budget = 2\n[[account]]\nid = \"a\"\n[[account]]\nid = \"b\"\n"},
		{name: "transcripts", break_: func(w *doctorWorld) {
			os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".claude", "projects"))
		}},
		{name: "profiles", break_: func(w *doctorWorld) {
			w.resolve = func(in config.Profile) (profile.Resolved, error) {
				return profile.Resolved{Name: in.Name}, errors.New("no item")
			}
		}},
		{name: "--verify", deep: true, cfg: "[[account]]\nid = \"a\"\n", break_: func(w *doctorWorld) {
			w.verify = func(string) (bool, *usage.Profile, error) { return true, nil, errors.New("401") }
		}},
		{name: "--verify wrong account", deep: true,
			cfg: "[[account]]\nid = \"a\"\naccount_uuid = \"u1\"\norg_id = \"o1\"\n",
			break_: func(w *doctorWorld) {
				w.verify = func(string) (bool, *usage.Profile, error) {
					pr := &usage.Profile{}
					pr.Account.UUID, pr.Organization.UUID = "u2", "o1"
					return true, pr, nil
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newDoctorWorld(t, doctorBaseConfig+c.cfg)
			if c.break_ != nil {
				c.break_(w)
			}
			out, err := w.run(t, c.deep)
			if !strings.Contains(out, "[FAIL]") {
				t.Fatalf("setup: no FAIL line printed:\n%s", out)
			}
			if err == nil {
				t.Fatalf("doctor printed a FAIL and exited zero:\n%s", out)
			}
		})
	}
}
