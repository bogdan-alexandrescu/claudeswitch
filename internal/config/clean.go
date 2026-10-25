package config

import (
	"fmt"
	"time"
)

// Cleaning up after Claude (owner decisions 2026-10-25, IMPROVEMENTS
// "Cleaning up after Claude"). `cs clean` works by hand whatever these say;
// clean turns on the daemon's runs, every clean_every.
const (
	DefaultCleanEvery            = 24 * time.Hour
	DefaultCleanAfter            = 72 * time.Hour
	DefaultCleanTranscriptsAfter = 30 * 24 * time.Hour
	// MinCleanEvery keeps the daemon from scanning every repository more
	// than once an hour.
	MinCleanEvery = time.Hour
)

// CleanOn is clean: whether the daemon cleans up on a schedule. Off unless
// turned on.
func (c *Config) CleanOn() bool { return c.Clean != nil && *c.Clean }

// CleanInterval is clean_every, or its default.
func (c *Config) CleanInterval() time.Duration {
	if c.CleanEvery == nil {
		return DefaultCleanEvery
	}
	return c.CleanEvery.Duration
}

// CleanWindow is clean_after: how long something must have gone unchanged
// before it can go.
func (c *Config) CleanWindow() time.Duration {
	if c.CleanAfter == nil {
		return DefaultCleanAfter
	}
	return c.CleanAfter.Duration
}

// CleanTranscripts is clean_transcripts_after; zero keeps them.
func (c *Config) CleanTranscripts() time.Duration {
	if c.CleanTranscriptsAfter == nil {
		return DefaultCleanTranscriptsAfter
	}
	return c.CleanTranscriptsAfter.Duration
}

// CleanKinds are the kinds' own settings: worktrees and branches, lane
// clones and temp files on unless turned off, merged branches on GitHub off
// unless turned on.
func (c *Config) CleanKinds() (worktrees, clones, temp, remote bool) {
	on := func(b *bool, def bool) bool {
		if b == nil {
			return def
		}
		return *b
	}
	return on(c.CleanWorktrees, true), on(c.CleanClones, true), on(c.CleanTemp, true),
		on(c.CleanRemoteBranches, false)
}

func (c *Config) validateClean() error {
	if e := c.CleanInterval(); e < MinCleanEvery {
		return fmt.Errorf("clean_every must be at least %s, got %s", MinCleanEvery, e)
	}
	if a := c.CleanWindow(); a < 0 {
		return fmt.Errorf("clean_after cannot be negative, got %s", a)
	}
	if t := c.CleanTranscripts(); t < 0 {
		return fmt.Errorf("clean_transcripts_after cannot be negative (0 keeps them), got %s", t)
	}
	return nil
}
