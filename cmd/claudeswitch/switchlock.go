package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The switch lock (D34, docs/PROFILES.md §10): one machine-wide lock held
// from the §3 check through the live write and its verify, by the daemon's
// swap and by every command that puts an account into a live credential
// (`use`, switching back after a login, a seed). With pools shared between
// profiles, two writers could each pass §3 for one account a moment apart
// and both write it; under one lock the second check sees the first write.

// codeSwitchBusy is the refusal when another writer held the switch lock
// for the whole wait: nothing was changed, and trying again shortly works.
const codeSwitchBusy = "switch_busy"

var (
	// acquireSwitchLock takes the lock. A seam, so a test can act as the
	// writer that finished while a command waited.
	acquireSwitchLock = state.AcquireSwitchLock
	// cliSwitchWait is how long a command waits for the lock before
	// refusing.
	cliSwitchWait = state.SwitchLockWait
	// daemonSwitchWait is how long the daemon waits. Short: it runs every
	// profile's decisions on one goroutine, and a swap it cannot make now it
	// decides again on the next tick.
	daemonSwitchWait = 2 * time.Second
)

// takeSwitchLock is a command's wait for the switch lock, refused with
// switch_busy when it runs out. what says what was not done.
func takeSwitchLock(what string) (*state.Lock, error) {
	l, err := acquireSwitchLock(cliSwitchWait)
	if errors.Is(err, state.ErrSwitchBusy) {
		return nil, &appError{Code: codeSwitchBusy, Err: err,
			Message: fmt.Sprintf("another claudeswitch command or the daemon is switching an account and did not "+
				"finish within %s, so nothing was changed: %s", cliSwitchWait, what),
			Hint: "run the same command again in a moment"}
	}
	if err != nil {
		return nil, fmt.Errorf("could not take the switch lock, so nothing was changed: %w", err)
	}
	return l, nil
}

// rereadState is state as it is now that the switch lock is held: what this
// command loaded before waiting may miss a swap another writer made
// meanwhile, and the §3 check must not. A file that cannot be read again
// leaves st, with a note; the check still asks every live item.
func rereadState(cfg *config.Config, st *state.State) *state.State {
	fresh, err := state.Load(st.Path(), cfg.ProfileNames()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not read state again before checking: %v\n", err)
		return st
	}
	return fresh
}
