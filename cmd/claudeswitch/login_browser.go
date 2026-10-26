package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// Signing in at a terminal, start to finish in one process.
//
// `login --direct` stopped after printing the URL, and the code the browser
// showed had to be pasted into a second command (`login --code`). That split
// exists for callers with nobody at a terminal — the app, scripts — and stays
// for them. At a terminal it was the one awkward step left after setup: setup
// asked for everything in turn, adding an account later did not. Here the
// browser comes back to a listener on this machine, as Claude Code's own
// login does (oauth.Callback), so there is nothing to paste either.

// exchangeCode trades an authorization code for tokens. A seam, so tests
// never reach the token endpoint.
var exchangeCode = func(ctx context.Context, f *oauth.AuthFlow, code string) (*oauth.Tokens, error) {
	return oauth.NewClient().Exchange(ctx, f, code)
}

// errNoListener means the browser cannot be brought back to this machine,
// and the code will have to be pasted after all.
var errNoListener = errors.New("cannot listen for the browser here")

// loginWait bounds how long a login waits for the browser.
const loginWait = 15 * time.Minute

// chooseLoginName asks what to call the account being added.
func chooseLoginName(prompt func(q, def string) string) (string, error) {
	id := strings.TrimSpace(prompt("  Name it", ""))
	if err := checkNewAccountID(id); err != nil {
		return "", err
	}
	return id, nil
}

// chooseLoginPool asks which pool a new account joins, when the config does
// not settle it: it does for an account it already has, with no profiles,
// and with a profile named "default", which takes new accounts (D6). The
// answer is a pool as --pool names one. "" when there is nothing to ask.
func chooseLoginPool(cfg *config.Config, id string, prompt func(q, def string) string) string {
	if hasAccount(cfg, id) || len(cfg.Profiles) == 0 {
		return ""
	}
	if _, ok := cfg.ProfileNamed(config.DefaultProfile); ok {
		return ""
	}
	var pools []string
	for _, in := range cfg.Profiles {
		if len(in.Pools) == 0 {
			pools = append(pools, in.Name) // its own pool (D35)
		}
	}
	for _, p := range cfg.Pools {
		pools = append(pools, p.Name)
	}
	if len(pools) == 0 {
		return ""
	}
	return strings.TrimSpace(prompt("  Pool it joins ("+strings.Join(pools, ", ")+")", pools[0]))
}

// chooseLoginBrowser asks which browser to sign in with, from those
// installed; "" for the default browser.
func chooseLoginBrowser(installed []string, prompt func(q, def string) string) string {
	if len(installed) == 0 {
		return ""
	}
	return strings.TrimSpace(prompt("  Open in which browser ("+strings.Join(installed, ", ")+")", installed[0]))
}

// browserLogin signs in through the browser and returns the tokens: it
// listens on this machine, opens the authorize URL with open, waits for the
// browser to come back with the code, and exchanges it. Nothing is stored.
// It fails with errNoListener when it cannot listen, before anything starts.
func browserLogin(ctx context.Context, w io.Writer, extra oauth.Extra, open func(url string) error) (*oauth.Tokens, error) {
	cb, err := oauth.ListenCallback()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoListener, err)
	}
	defer cb.Close()
	flow, err := oauth.BeginAuthCallback(cb, extra)
	if err != nil {
		return nil, err
	}
	if err := open(flow.URL); err != nil {
		fmt.Fprintf(w, "    %v\n", err)
	}
	// The URL carries this login's state: with it and the port, anyone
	// could hand the listener a code of their own, so it is not for sharing.
	fmt.Fprintf(w, "    If no browser opened, or the wrong one did, open this in the right one\n"+
		"    (it is this login's alone: do not share it or show it on a screen share):\n\n%s\n\n", flow.URL)
	fmt.Fprintf(w, "    waiting for the browser to come back… (Ctrl-C to stop)\n")
	code, err := cb.Wait(ctx, flow)
	if err != nil {
		return nil, err
	}
	xctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return exchangeCode(xctx, flow, code)
}

// openBrowser opens a URL in the named browser application, or the default
// browser for "". It says which, so a login lands in the browser meant.
func openBrowser(w io.Writer, browser string) func(string) error {
	return func(u string) error {
		var cmd *exec.Cmd
		switch {
		case runtime.GOOS != "darwin":
			cmd = exec.Command("xdg-open", u)
		case browser != "":
			cmd = exec.Command("open", "-a", browserApp(browser), u)
		default:
			cmd = exec.Command("open", u)
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s did not open: %w", nonEmpty(browser, "the default browser"), err)
		}
		fmt.Fprintf(w, "    opened in %s — sign in as the account you want, and approve\n",
			nonEmpty(browser, "your default browser"))
		return nil
	}
}

// loginHere is `login --direct` at a terminal, finished in this process: the
// browser comes back here with the code, which is exchanged and vaulted with
// the same checks as a pasted one (finishDirectLogin). Nothing touches the
// live session. errNoListener means it could not start, and nothing did.
func loginHere(cfg *config.Config, st *state.State, v *vault.Vault, id, wantOrg string, extra oauth.Extra,
	browser, pool string, join joinTarget) error {
	fmt.Println()
	explainDirect(cfg, id, wantOrg, extra, pool)
	fmt.Println()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, loginWait)
	defer cancel()
	tok, err := browserLogin(ctx, os.Stdout, extra, openBrowser(os.Stdout, browser))
	switch {
	case errors.Is(err, errNoListener):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("the browser did not come back within %s; nothing was stored, "+
			"and your session is untouched", loginWait)
	case errors.Is(err, context.Canceled):
		return errors.New("stopped; nothing was stored, and your session is untouched")
	case err != nil:
		return fmt.Errorf("%w\n  (nothing was stored; your session is untouched)", err)
	}
	_, err = finishDirectLogin(cfg, st, v, oauth.Pending{AccountID: id, OrgID: wantOrg, Org: extra.Org,
		Profile: pool, JoinPool: join.Pool, NoPool: join.None}, tok)
	return err
}

// signInHere is setup's sign-in for one more account: the browser comes back
// with the code, or, when nothing can listen here, the code is pasted as it
// always was. Ctrl-C skips the account; nothing is stored either way.
func signInHere(ctx context.Context, extra oauth.Extra, browser string) (*oauth.Tokens, error) {
	sctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	sctx, cancel := context.WithTimeout(sctx, loginWait)
	defer cancel()
	tok, err := browserLogin(sctx, os.Stdout, extra, openBrowser(os.Stdout, browser))
	switch {
	case errors.Is(err, context.Canceled):
		return nil, errors.New("skipped; nothing stored")
	case !errors.Is(err, errNoListener):
		return tok, err
	}

	fmt.Printf("    %v — paste the code instead\n", err)
	flow, err := oauth.BeginAuthWith("", extra)
	if err != nil {
		return nil, err
	}
	if browser != "" && exec.Command("open", "-a", browserApp(browser), flow.URL).Run() == nil {
		fmt.Printf("    opened in %s — sign in as the account you want, approve it\n", browser)
	} else {
		fmt.Printf("\n%s\n\n", flow.URL)
	}
	code := ask("  Paste the code", "")
	if code == "" {
		return nil, errors.New("skipped; nothing stored")
	}
	xctx, xcancel := context.WithTimeout(ctx, 60*time.Second)
	defer xcancel()
	return exchangeCode(xctx, flow, code)
}
