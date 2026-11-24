package main

import (
	"flag"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// reauthWorld is sharedTOML with t1 and t2 needing sign-in and t1's email
// recorded; p1 is healthy.
func reauthWorld(t *testing.T) string {
	t.Helper()
	path := appWorld(t, sharedTOML)
	st, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("t1").LastErr = "this account needs an interactive login (`claude` then /login): invalid_grant"
	st.Get("t2").RefreshExpiry = time.Now().Add(-time.Hour)
	st.Get("p1")
	st.SetEmail("t1", "t1@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

// `cs reauth <name>` is `cs login <name> --direct` for an account that
// already exists, asking the browser for that account's own address.
func TestReauthIsADirectLogin(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{{ID: "t1"}, {ID: "t2"}}}
	st := &state.State{Emails: map[string]string{"t1": "t1@example.com"}}

	got, err := reauthLoginArgs(cfg, st, "t1", []string{"--browser", "Safari"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"t1", "--direct", "--email", "t1@example.com", "--browser", "Safari"}
	if !slices.Equal(got, want) {
		t.Errorf("login args = %q, want %q", got, want)
	}

	// An address given on the command line is the one asked for.
	got, _ = reauthLoginArgs(cfg, st, "t1", []string{"--email", "other@example.com"})
	if want := []string{"t1", "--direct", "--email", "other@example.com"}; !slices.Equal(got, want) {
		t.Errorf("with --email: login args = %q, want %q", got, want)
	}

	// No recorded address: no hint, the browser decides.
	got, _ = reauthLoginArgs(cfg, st, "t2", nil)
	if want := []string{"t2", "--direct"}; !slices.Equal(got, want) {
		t.Errorf("no email: login args = %q, want %q", got, want)
	}
}

// A name that is not an account is refused, never added: a typo must not
// file a new account. Adding one is `cs login`'s job.
func TestReauthRefusesAnUnknownName(t *testing.T) {
	path := reauthWorld(t)
	out, code := runCLI(t, "reauth", "nobody", "--config", path)
	if code != 1 || !strings.Contains(out, `no account "nobody"`) || !strings.Contains(out, "cs login nobody") {
		t.Errorf("reauth nobody: exit %d\n%s", code, out)
	}
}

// With no name it lists what needs signing in, each with its command.
func TestReauthWithNoNameListsWhatNeedsIt(t *testing.T) {
	path := reauthWorld(t)
	out, code := runCLI(t, "reauth", "--config", path)
	if code != 0 {
		t.Fatalf("reauth: exit %d\n%s", code, out)
	}
	for _, want := range []string{"cs reauth t1", "cs reauth t2"} {
		if !strings.Contains(out, want) {
			t.Errorf("reauth does not offer %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "reauth p1") {
		t.Errorf("reauth offers a healthy account:\n%s", out)
	}
}

func TestReauthWithNothingToDo(t *testing.T) {
	path := appWorld(t, sharedTOML)
	out, code := runCLI(t, "reauth", "--config", path)
	if code != 0 || !strings.Contains(out, "no account needs signing in") {
		t.Errorf("reauth: exit %d\n%s", code, out)
	}
}

func TestReauthHelp(t *testing.T) {
	out, _, code := runCLISplit(t, "reauth", "--help")
	if code != 0 || !strings.Contains(out, "usage: claudeswitch reauth [<id>]") {
		t.Errorf("reauth --help: exit %d\n%s", code, out)
	}
	out, _, _ = runCLISplit(t, "help")
	if !strings.Contains(out, "\n  reauth ") {
		t.Errorf("help does not list reauth:\n%s", out)
	}
}

// The listed commands keep the config they were listed from, or the name
// they offer would not be found.
func TestReauthListKeepsTheConfig(t *testing.T) {
	path := reauthWorld(t)
	out, _ := runCLI(t, "reauth", "--config", path)
	if want := "cs reauth t1 --config " + shellQuote(path); !strings.Contains(out, want) {
		t.Errorf("reauth does not offer %q:\n%s", want, out)
	}
}

func TestReauthListJSON(t *testing.T) {
	path := reauthWorld(t)
	out, _, code := runCLISplit(t, "reauth", "--json", "--config", path)
	got := decodeJSON(t, []byte(out))
	need, _ := got["needs_sign_in"].([]any)
	if code != 0 || len(need) != 2 || need[0] != "t1" || need[1] != "t2" {
		t.Errorf("reauth --json: exit %d\n%s", code, out)
	}

	path = appWorld(t, sharedTOML)
	out, _, _ = runCLISplit(t, "reauth", "--json", "--config", path)
	if need, ok := decodeJSON(t, []byte(out))["needs_sign_in"].([]any); !ok || len(need) != 0 {
		t.Errorf("nothing to do: want an empty list\n%s", out)
	}
}

// A flag reaches login as it was given, false included: --no-open=false must
// not arrive as --no-open.
func TestReauthPassesFlagsAsGiven(t *testing.T) {
	fs := flag.NewFlagSet("reauth", flag.ContinueOnError)
	fs.String("browser", "", "")
	fs.Bool("no-open", false, "")
	fs.Bool("json", false, "")
	if err := fs.Parse([]string{"--no-open=false", "--browser", "Safari", "--json"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"--browser", "Safari", "--json=true", "--no-open=false"}
	if got := givenFlags(fs); !slices.Equal(got, want) {
		t.Errorf("givenFlags = %q, want %q", got, want)
	}
}
