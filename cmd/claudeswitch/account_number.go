package main

import (
	"fmt"
	"strconv"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// listedIDs is every configured account's id, in the list's order, which is
// the order accounts are first numbered in.
func listedIDs(cfg *config.Config) []string {
	accounts := listedAccounts(cfg)
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	return ids
}

// isAccountNumber reports whether an argument is written as a number. Digits
// only: strconv alone would also take "+2".
func isAccountNumber(arg string) bool {
	if arg == "" {
		return false
	}
	for _, c := range arg {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resolveAccountArg turns the account a person typed into its id. A name is
// returned as it came. All-digit names are legal, and an account really
// called "2" was reachable by that name before numbers existed, so an exact
// name, in the config or only in the vault, beats a number.
func resolveAccountArg(cfg *config.Config, arg string) (string, error) {
	if !isAccountNumber(arg) || configured(cfg, arg) || vault.New(logger(false)).Has(arg) {
		return arg, nil
	}
	numbers, err := state.AccountNumbers(listedIDs(cfg))
	if err != nil {
		return "", fmt.Errorf("could not read the account numbers: %w", err)
	}
	if n, err := strconv.Atoi(arg); err == nil {
		for id, num := range numbers {
			if num == n {
				return id, nil
			}
		}
	}
	return "", appErr(codeNotFound, "`cs list` shows each account's number", "no account numbered %s", arg)
}
