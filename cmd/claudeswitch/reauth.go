package main

import (
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// cmdReauth signs an existing account in again: `cs login <id> --direct`,
// asking the browser for the address the account was last signed in as. It
// never adds an account, so a typo cannot file a new one. With no name it
// lists the accounts that need it.
func cmdReauth(args []string) error {
	fs := flag.NewFlagSet("reauth", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	fs.String("browser", "", "open the sign-in in this browser (as for login)")
	fs.String("email", "", "the address to ask the browser for (default: the one recorded for the account)")
	fs.Bool("no-open", false, "do not open a browser, only print the URL; finish with: login <id> --code <code>")
	fs.Bool("json", false, "machine-readable output (as login --direct --json)")
	positional := parseInterleaved(fs, args)
	if len(positional) > 1 {
		return appErr(codeUsage, "", "usage: claudeswitch reauth [<id>]")
	}
	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		printNeedsReauth(cfg, st, time.Now())
		return nil
	}
	// Everything given but the name goes to login as it was given.
	var rest []string
	fs.Visit(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			rest = append(rest, "--"+f.Name)
			return
		}
		rest = append(rest, "--"+f.Name, f.Value.String())
	})
	loginArgs, err := reauthLoginArgs(cfg, st, positional[0], rest)
	if err != nil {
		return err
	}
	return cmdLogin(loginArgs)
}

// reauthLoginArgs is the login that signs account id in again.
func reauthLoginArgs(cfg *config.Config, st *state.State, id string, rest []string) ([]string, error) {
	if !hasAccount(cfg, id) {
		return nil, appErr(codeNotFound, fmt.Sprintf("to add a new account: cs login %s", id),
			"no account %q to sign in again", id)
	}
	out := []string{id, "--direct"}
	asked := slices.ContainsFunc(rest, func(a string) bool {
		return a == "--email" || strings.HasPrefix(a, "--email=")
	})
	if email := st.Emails[id]; email != "" && !asked {
		out = append(out, "--email", email)
	}
	return append(out, rest...), nil
}

// printNeedsReauth lists every account that needs signing in, each with the
// command that does it.
func printNeedsReauth(cfg *config.Config, st *state.State, now time.Time) {
	var need []string
	for _, a := range listedAccounts(cfg) {
		if st.Accounts[a.ID].NeedsSignIn(now) {
			need = append(need, a.ID)
		}
	}
	if len(need) == 0 {
		fmt.Println("no account needs signing in")
		return
	}
	fmt.Println("these accounts need signing in:")
	for _, id := range need {
		fmt.Printf("  cs reauth %s\n", id)
	}
}
