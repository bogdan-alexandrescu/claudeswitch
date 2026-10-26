package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
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

// Chrome is offered first when it is installed, as the browser most people
// have; the rest keep the order they always had.
func TestChromeIsOfferedFirst(t *testing.T) {
	has := map[string]bool{"Safari": true, "Google Chrome": true, "Brave Browser": true, "Microsoft Edge": true}
	got := installedBrowsers(func(app string) bool { return has[app] })
	if want := []string{"Chrome", "Safari", "Brave", "Edge"}; !slices.Equal(got, want) {
		t.Errorf("offered %q, want %q", got, want)
	}
}

// Nobody should have to type "Google Chrome": a short name opens its
// application, in any case. Full names keep working, and a name the list
// does not know reaches `open -a` as typed.
func TestABrowserIsTypedByItsShortName(t *testing.T) {
	for typed, app := range map[string]string{
		"Chrome": "Google Chrome", "chrome": "Google Chrome", "Google Chrome": "Google Chrome",
		"brave": "Brave Browser", "Brave Browser": "Brave Browser",
		"Edge": "Microsoft Edge", "microsoft edge": "Microsoft Edge",
		"Safari": "Safari", "firefox": "Firefox",
		"Chrome Beta": "Chrome Beta",
	} {
		if got := browserApp(typed); got != app {
			t.Errorf("%q opens %q, want %q", typed, got, app)
		}
	}
}

func TestAShortBrowserNameOpensItsApplication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the login opens a named browser with macOS's open -a")
	}
	testshim.ResetLaunches()
	if err := openBrowser(io.Discard, "chrome")("https://example.test/authorize"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"open", "-a", "Google Chrome", "https://example.test/authorize"}}
	if got := testshim.Launches(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("launches = %q\nwant       %q", got, want)
	}
}

// Setup's further accounts need a browser the person is NOT signed into, as
// its own text says, so it suggests Safari over Chrome, the one most people
// are signed into: Enter there would bring back the account already vaulted.
func TestSetupSuggestsABrowserNotUsuallySignedInto(t *testing.T) {
	for _, c := range []struct {
		installed []string
		want      string
	}{
		{[]string{"Chrome", "Safari", "Brave"}, "Safari"},
		{[]string{"Chrome", "Brave", "Edge"}, "Brave"},
		{[]string{"Chrome"}, "Chrome"},
		{nil, ""},
	} {
		if got := setupBrowser(c.installed); got != c.want {
			t.Errorf("setupBrowser(%q) = %q, want %q", c.installed, got, c.want)
		}
	}
}

// The message names the application that opened, not the name as typed.
func TestTheBrowserMessageNamesTheApplication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the login opens a named browser with macOS's open -a")
	}
	testshim.ResetLaunches()
	var out strings.Builder
	if err := openBrowser(&out, "chrome")("https://example.test/authorize"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "opened in Google Chrome") {
		t.Errorf("said %q, want it to name Google Chrome", out.String())
	}
}

// `--browser edge` on the two-step login opens Microsoft Edge too.
func TestATwoStepLoginOpensAShortNamedBrowser(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, strings.Replace(poolConfig, `name = "default"`, `name = "home"`, 1)))
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oauth.ClearPending() })
	testshim.ResetLaunches()
	if err := loginDirect(cfg, st, nil, "w2", "", false, oauth.Extra{}, "edge", "work", joinTarget{}); err != nil {
		t.Fatal(err)
	}
	got := testshim.Launches()
	if len(got) != 1 || !slices.Equal(got[0][:3], []string{"open", "-a", "Microsoft Edge"}) {
		t.Errorf("launches = %q, want one open -a \"Microsoft Edge\"", got)
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
