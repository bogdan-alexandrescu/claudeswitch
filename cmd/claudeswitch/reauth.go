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
	asJSON := fs.Bool("json", false, "machine-readable output (as login --direct --json)")
	positional := parseInterleaved(fs, args)
	if len(positional) > 1 {
		return appErr(codeUsage, "", "usage: claudeswitch reauth [<id>]")
	}
	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		need := needsReauth(cfg, st, time.Now())
		if *asJSON {
			return emitJSON(map[string]any{"needs_sign_in": need})
		}
		printNeedsReauth(need, *cfgPath)
		return nil
	}
	loginArgs, err := reauthLoginArgs(cfg, st, positional[0], givenFlags(fs))
	if err != nil {
		return err
	}
	return cmdLogin(loginArgs)
}

// givenFlags is every flag given on the command line, to pass on to login.
func givenFlags(fs *flag.FlagSet) []string {
	var rest []string
	fs.Visit(func(f *flag.Flag) {
		// A boolean keeps its value: --no-open=false is not --no-open.
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			rest = append(rest, "--"+f.Name+"="+f.Value.String())
			return
		}
		rest = append(rest, "--"+f.Name, f.Value.String())
	})
	return rest
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

// needsReauth is every account that needs signing in, in list order.
func needsReauth(cfg *config.Config, st *state.State, now time.Time) []string {
	need := []string{}
	for _, a := range listedAccounts(cfg) {
		if st.Accounts[a.ID].NeedsSignIn(now) {
			need = append(need, a.ID)
		}
	}
	return need
}

// printNeedsReauth lists the accounts that need signing in, each with the
// command that does it, under the config they were read from.
func printNeedsReauth(need []string, cfgPath string) {
	tail := ""
	if cfgPath != "" {
		tail = " --config " + shellQuote(cfgPath)
	}
	if len(need) == 0 {
		fmt.Println("no account needs signing in")
		return
	}
	fmt.Println("these accounts need signing in:")
	for _, id := range need {
		fmt.Printf("  cs reauth %s%s\n", id, tail)
	}
}
