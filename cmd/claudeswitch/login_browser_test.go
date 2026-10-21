package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
)

// `cs login` with no name, at a terminal, asks for one rather than refusing:
// adding an account after setup should be as easy as setup's own loop.
func TestALoginWithoutANameAsksForOne(t *testing.T) {
	var asked string
	got, err := chooseLoginName(func(q, def string) string { asked = q; return "  lab-4  " })
	if err != nil || got != "lab-4" {
		t.Fatalf("got %q, %v; want lab-4", got, err)
	}
	if !strings.Contains(asked, "Name") {
		t.Errorf("asked %q; want it to ask for a name", asked)
	}
}

func TestALoginNameMustBeAUsableName(t *testing.T) {
	for _, typed := range []string{"", "two words"} {
		if got, err := chooseLoginName(func(string, string) string { return typed }); err == nil {
			t.Errorf("typed %q: got %q, want a refusal", typed, got)
		}
	}
}

// A new account has to land in some pool. With no profile named "default"
// the command line used to refuse and ask for --profile; at a terminal it
// asks instead, offering the first pool.
func TestANewAccountIsAskedWhichPoolItJoins(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)))
	var asked, offered string
	got := chooseLoginPool(cfg, "w2", func(q, def string) string { asked, offered = q, def; return "work" })
	if got != "work" {
		t.Errorf("got %q, want what was typed", got)
	}
	if offered != "home" {
		t.Errorf("offered %q, want the first pool, home", offered)
	}
	for _, name := range []string{"home", "work"} {
		if !strings.Contains(asked, name) {
			t.Errorf("asked %q; want it to list %s", asked, name)
		}
	}
}

func TestNoPoolIsAskedWhenTheConfigDecides(t *testing.T) {
	noDefault := loadOrFail(t, writeConfig(t, strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)))
	for _, c := range []struct {
		name string
		cfg  string
		id   string
	}{
		{"an account the config already has", "", "w1"},
		{"a profile named default takes new accounts", poolConfig, "w2"},
		{"no profiles at all", baseConfig, "w2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := noDefault
			if c.cfg != "" {
				cfg = loadOrFail(t, writeConfig(t, c.cfg))
			}
			if got := chooseLoginPool(cfg, c.id, noPrompt(t)); got != "" {
				t.Errorf("got %q, want no pool chosen here", got)
			}
		})
	}
}

// Which person signs in is whoever the browser is signed in as, so the
// browser is the real choice: asked, as setup asks, from those installed.
func TestTheBrowserIsChosenFromThoseInstalled(t *testing.T) {
	var offered string
	got := chooseLoginBrowser([]string{"Safari", "Firefox"}, func(q, def string) string { offered = def; return "Firefox" })
	if got != "Firefox" || offered != "Safari" {
		t.Errorf("got %q offering %q; want Firefox, offering Safari", got, offered)
	}
	if got := chooseLoginBrowser(nil, noPrompt(t)); got != "" {
		t.Errorf("with none installed got %q, want the default browser", got)
	}
}

// The whole login in one process: open the browser, take the code from its
// redirect, exchange it. Nothing to paste, and no second command.
func TestABrowserLoginNeedsNoPaste(t *testing.T) {
	var exchanged, redirect string
	restore := exchangeCode
	exchangeCode = func(_ context.Context, f *oauth.AuthFlow, code string) (*oauth.Tokens, error) {
		exchanged = code
		redirect = queryOf(t, f.URL).Get("redirect_uri")
		return &oauth.Tokens{AccessToken: "a", RefreshToken: "r"}, nil
	}
	t.Cleanup(func() { exchangeCode = restore })

	// The browser: approves, and is sent back to the redirect with the code.
	browser := func(authURL string) error {
		q := queryOf(t, authURL)
		back := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			c := &http.Client{Timeout: 5 * time.Second}
			if resp, err := c.Get(back); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := browserLogin(ctx, io.Discard, oauth.Extra{}, browser)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "a" || exchanged != "the-code" {
		t.Errorf("got %+v exchanging %q; want the tokens for the-code", tok, exchanged)
	}
	if !strings.HasPrefix(redirect, "http://localhost:") {
		t.Errorf("redirect_uri %q; want this machine's listener", redirect)
	}
}

func queryOf(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// Nobody at the terminal (the app, a script): nothing may prompt, so a login
// with no name stays the usage error it was, and says how to get the prompt.
func TestALoginWithNoNameAndNobodyToAskIsAUsageError(t *testing.T) {
	restore := promptsOff
	promptsOff = true
	t.Cleanup(func() { promptsOff = restore })
	err := cmdLogin(nil)
	var ae *appError
	if !errors.As(err, &ae) || ae.Code != codeUsage {
		t.Fatalf("got %v, want a usage error", err)
	}
	if !strings.Contains(ae.Hint, "at a terminal") {
		t.Errorf("hint %q; want it to say a terminal asks for the name", ae.Hint)
	}
}
