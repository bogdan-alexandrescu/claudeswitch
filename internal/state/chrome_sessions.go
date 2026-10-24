package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// A ChromeSession is which account a Claude Code session's last Claude in
// Chrome call ran as (owner decision 2026-10-23, instead of moving tabs
// between Chrome profiles). When the session's next call finds another
// account live, the plugin's hook tells Claude that its earlier tabs stayed
// in the other account's Chrome profile, once.
type ChromeSession struct {
	Session string    `json:"session"`
	Profile string    `json:"profile"`
	Account string    `json:"account"`
	At      time.Time `json:"at"`
}

// ChromeSessionKeep is how many sessions the book remembers, the most
// recent first.
const ChromeSessionKeep = 200

// chromeSessionRefresh is how stale a record may get before a call by the
// same account writes it again: the book is rewritten when the account
// changes, and otherwise only often enough to keep the order by recency.
const chromeSessionRefresh = time.Hour

const chromeSessionsLockName = "chrome-sessions.lock"

// ChromeSessionsPath is the book's file, beside state.json, a file of its own
// for the reason the artifact book is: every session's hook writes it.
func ChromeSessionsPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "chrome-sessions.json")
}

type chromeSessionBook struct {
	Version  int             `json:"version"`
	Sessions []ChromeSession `json:"sessions"`
}

func readChromeSessions() ([]ChromeSession, error) {
	b, err := os.ReadFile(ChromeSessionsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var book chromeSessionBook
	if err := json.Unmarshal(b, &book); err != nil {
		return nil, err
	}
	return book.Sessions, nil
}

// NoteChromeSession records a Claude in Chrome call and returns the
// session's record from before it, if there was one.
func NoteChromeSession(c ChromeSession) (ChromeSession, bool, error) {
	lk, err := blockingLock(lockPath(chromeSessionsLockName))
	if err != nil {
		return ChromeSession{}, false, err
	}
	defer lk.Release()
	all, err := readChromeSessions()
	if err != nil {
		all = nil // an unreadable book is started again rather than blocking every call
	}
	i := slices.IndexFunc(all, func(x ChromeSession) bool { return x.Session == c.Session })
	var prev ChromeSession
	if i >= 0 {
		prev = all[i]
		if prev.Account == c.Account && prev.Profile == c.Profile && c.At.Sub(prev.At) < chromeSessionRefresh {
			return prev, true, nil
		}
		all = slices.Delete(all, i, i+1)
	}
	all = append([]ChromeSession{c}, all...)
	slices.SortStableFunc(all, func(x, y ChromeSession) int { return y.At.Compare(x.At) })
	if len(all) > ChromeSessionKeep {
		all = all[:ChromeSessionKeep]
	}
	b, err := json.MarshalIndent(chromeSessionBook{Version: 1, Sessions: all}, "", "  ")
	if err != nil {
		return prev, i >= 0, err
	}
	path := ChromeSessionsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return prev, i >= 0, err
	}
	return prev, i >= 0, os.Rename(tmp, path)
}
