package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/cclogin"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// identityPathOf is the .claude.json Claude Code in profile in reads, which
// records the login it believes it has (P1). Paths only, no keychain.
func identityPathOf(in config.Profile) string {
	var p ccdir.Paths
	var err error
	if in.FromEnv {
		p, err = ccdir.FromEnv()
	} else {
		p, err = ccdir.For(in.Dir)
	}
	if err != nil {
		return ""
	}
	return p.GlobalConfig
}

// activeOf answers, for a profile name, the account recorded live in it;
// "" for every profile without a state to read it from.
func activeOf(cfg *config.Config, st *state.State) func(string) string {
	return func(name string) string {
		if st == nil {
			return ""
		}
		for _, v := range profileViews(cfg, st, "") {
			if v.in.Name == name {
				return v.ist.Active
			}
		}
		return ""
	}
}

// loginOf compares the login Claude Code in profile in records with the
// account live there (P3). known is false when nothing can be said: no
// .claude.json, no recorded login, or no seat configured for the account.
// The seat is a person in an organization, so the same person recorded in
// another organization differs too.
func loginOf(cfg *config.Config, in config.Profile, active string) (rec cclogin.Login, differs, known bool) {
	seat := cfg.SeatOf(active)
	if active == "" || seat == "" {
		return cclogin.Login{}, false, false
	}
	rec, err := cclogin.Read(identityPathOf(in))
	if err != nil || rec.AccountUUID == "" {
		return cclogin.Login{}, false, false
	}
	person, org, _ := strings.Cut(seat, "@")
	differs = rec.AccountUUID != person || (org != "" && rec.OrgUUID != "" && rec.OrgUUID != org)
	return rec, differs, true
}

// loginName is a recorded login as short as the status line needs it: the
// email's name part, or the account uuid's first eight characters.
func loginName(l cclogin.Login) string {
	if name, _, ok := strings.Cut(l.Email, "@"); ok && name != "" {
		return name
	}
	return shortID(l.AccountUUID)
}

// syncLoginFrom writes the login a profile answer (raw) describes into the
// .claude.json at path, and returns the recorded login before and after:
// `whoami --sync`, which has just made that read and needs no second one.
func syncLoginFrom(path string, raw []byte, now time.Time) (before, after cclogin.Login, err error) {
	prev, err := cclogin.Block(path)
	if err != nil {
		return cclogin.Login{}, cclogin.Login{}, err
	}
	before, _ = cclogin.Read(path)
	block, err := cclogin.FromProfile(raw, prev, now)
	if err != nil {
		return before, cclogin.Login{}, err
	}
	if err := cclogin.Write(path, block); err != nil {
		return before, cclogin.Login{}, err
	}
	after, err = cclogin.Read(path)
	return before, after, err
}

// whoamiSync is `whoami --sync`: the profile just read becomes the login
// Claude Code in that profile records (P1), and what changed is said.
func whoamiSync(in config.Profile, pr *usage.Profile, perr error) {
	path := identityPathOf(in)
	if perr != nil {
		fmt.Printf("  recorded login  not changed: the profile could not be read (%v)\n", perr)
		return
	}
	before, after, err := syncLoginFrom(path, pr.Raw, time.Now())
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Printf("  recorded login  none to change: Claude Code has not run in this profile (%s)\n", path)
	case err != nil:
		fmt.Printf("  recorded login  not changed: %v\n", err)
	case before.AccountUUID == after.AccountUUID && before.OrgUUID == after.OrgUUID:
		fmt.Printf("  recorded login  %s, refreshed\n", after.Email)
	default:
		fmt.Printf("  recorded login  %s → %s: Claude Code is now logged in as the account it uses\n",
			nonEmpty(before.Email, "none"), after.Email)
	}
}
