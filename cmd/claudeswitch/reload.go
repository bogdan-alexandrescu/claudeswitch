package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// configReloader re-reads the daemon's config when it changes on disk.
//
// The daemon read its config once, at startup, so an account added while it ran
// — which is now what `login` and `add` do by themselves — stayed invisible
// until someone thought to restart it (2026-10-07).
//
// reload is called from the daemon's main loop, never from the watcher
// goroutine: the config, the poller and the state are all owned by that one
// goroutine, and handing it a new config is the only safe way to change one.
type configReloader struct {
	path    string
	cur     *config.Config
	lastErr string
	log     *slog.Logger
}

func newConfigReloader(path string, cur *config.Config, log *slog.Logger) *configReloader {
	if path == "" {
		path = cur.Path
	}
	return &configReloader{path: path, cur: cur, log: log}
}

// reload loads the config again. On success it returns the new config and what
// changed (nil when nothing did). On failure it returns the config already in
// force and nil, and warns — once per distinct error, since an editor saving a
// half-typed line produces the same failure on every save.
func (r *configReloader) reload() (*config.Config, []string) {
	next, err := config.Load(r.path)
	if err != nil {
		// Load hands back defaults alongside a missing-file error; never adopt
		// them, or a deleted config would empty the daemon of accounts.
		if msg := err.Error(); msg != r.lastErr {
			r.lastErr = msg
			r.log.Warn("config changed but cannot be loaded; keeping the previous one",
				"path", r.path, "err", err)
		}
		return r.cur, nil
	}
	r.lastErr = ""
	// Scope and [project] lines are ignored (lane 16): say so once more
	// only when an edit changed what is left of them.
	if w := next.LegacyWarning(); w != "" && w != r.cur.LegacyWarning() {
		r.log.Warn(w)
	}
	changes := configChanges(r.cur, next)
	r.cur = next
	if len(changes) > 0 {
		r.log.Info("config reloaded", "path", r.path, "changes", strings.Join(changes, "; "))
	}
	return next, changes
}

// configChanges describes the difference between two configs in the terms a
// person would check: which accounts came and went, which were re-pinned, and
// which thresholds moved.
func configChanges(old, next *config.Config) []string {
	var out []string
	oldAcc := map[string]config.Account{}
	for _, a := range old.Accounts {
		oldAcc[a.ID] = a
	}
	newAcc := map[string]config.Account{}
	for _, a := range next.Accounts {
		newAcc[a.ID] = a
	}
	var added, removed, repinned, other []string
	for _, a := range next.Accounts {
		was, ok := oldAcc[a.ID]
		switch {
		case !ok:
			added = append(added, a.ID)
		case was.Seat() != a.Seat():
			repinned = append(repinned, a.ID)
		case was.Reserve != a.Reserve || was.IsEnabled() != a.IsEnabled() ||
			was.Label != a.Label:
			other = append(other, a.ID)
		}
	}
	for _, a := range old.Accounts {
		if _, ok := newAcc[a.ID]; !ok {
			removed = append(removed, a.ID)
		}
	}
	if len(added) > 0 {
		out = append(out, "accounts added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		out = append(out, "accounts removed: "+strings.Join(removed, ", "))
	}
	if len(repinned) > 0 {
		out = append(out, "seat pin changed: "+strings.Join(repinned, ", "))
	}
	if len(other) > 0 {
		out = append(out, "account settings changed: "+strings.Join(other, ", "))
	}
	if !slices.Equal(old.Priority, next.Priority) {
		out = append(out, fmt.Sprintf("priority %v → %v", old.Priority, next.Priority))
	}
	num := func(name string, a, b float64) {
		if a != b {
			out = append(out, fmt.Sprintf("%s %g → %g", name, a, b))
		}
	}
	num("switch_at", old.SwitchAt, next.SwitchAt)
	num("switch_at_weekly", old.SwitchAtWeekly, next.SwitchAtWeekly)
	num("hard_floor", old.HardFloor, next.HardFloor)
	num("hot_threshold", old.HotThreshold, next.HotThreshold)
	num("api_budget", float64(old.APIBudget), float64(next.APIBudget))
	dur := func(name string, a, b config.Duration) {
		if a.Duration != b.Duration {
			out = append(out, fmt.Sprintf("%s %s → %s", name, a.Duration, b.Duration))
		}
	}
	dur("cooldown", old.Cooldown, next.Cooldown)
	dur("max_switch_wait", old.MaxSwitchWait, next.MaxSwitchWait)
	dur("poll_active", old.PollActive, next.PollActive)
	dur("poll_hot", old.PollHot, next.PollHot)
	dur("poll_idle", old.PollIdle, next.PollIdle)
	dur("refresh_window", old.RefreshWindow, next.RefreshWindow)
	if old.SwitchWhen != next.SwitchWhen {
		out = append(out, fmt.Sprintf("switch_when %s → %s", old.SwitchWhen, next.SwitchWhen))
	}
	if old.RefreshEnabled() != next.RefreshEnabled() {
		out = append(out, fmt.Sprintf("auto_refresh %v → %v", old.RefreshEnabled(), next.RefreshEnabled()))
	}
	return out
}

// watchConfig signals on the returned channel after the config file changes,
// once per burst of changes: editors write, chmod and rename in quick
// succession, and appendAccount replaces the file by rename.
//
// Directories are watched rather than the file, because a file replaced by
// rename is a new inode, and a watch on the old one goes quiet forever. When
// the config is a symlink, both the link's directory and its target's are
// watched — edits are written beside the target — and the link is resolved
// again after every event, since it can be pointed somewhere else.
//
// The channel has room for one signal and a send never blocks: a reload that
// is already pending covers any change that arrives before it runs.
func watchConfig(path string, debounce time.Duration, stop <-chan struct{}, log *slog.Logger) (<-chan struct{}, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(abs)); err != nil {
		w.Close()
		return nil, err
	}
	watched := map[string]bool{filepath.Dir(abs): true}
	// target is the resolved file, or "" while the link dangles.
	target := ""
	follow := func() {
		t, err := filepath.EvalSymlinks(abs)
		if err != nil {
			target = ""
			return
		}
		if t, err = filepath.Abs(t); err != nil {
			return
		}
		target = t
		if dir := filepath.Dir(t); !watched[dir] {
			watched[dir] = true
			// Not inline: this runs on the goroutine that drains w.Events, and
			// the kqueue backend's Add can wait on its own reader, which waits
			// for that channel to be drained.
			go func() {
				if err := w.Add(dir); err != nil {
					log.Warn("cannot watch the config's target directory", "dir", dir, "err", err)
				}
			}()
		}
	}
	follow()

	// kqueue reports a symlink by its target, so a link pointed somewhere else
	// can go unreported. A cheap once-a-second look at the link and its target
	// catches what the events miss.
	fingerprint := func() string {
		var fp strings.Builder
		if fi, err := os.Lstat(abs); err == nil {
			fmt.Fprintf(&fp, "%d/%d|", fi.ModTime().UnixNano(), fi.Size())
		}
		if t, err := filepath.EvalSymlinks(abs); err == nil {
			fmt.Fprintf(&fp, "%s|", t)
		}
		if fi, err := os.Stat(abs); err == nil {
			fmt.Fprintf(&fp, "%d/%d", fi.ModTime().UnixNano(), fi.Size())
		}
		return fp.String()
	}
	seen := fingerprint()

	out := make(chan struct{}, 1)
	go func() {
		defer w.Close()
		check := time.NewTicker(configCheckInterval)
		defer check.Stop()
		var fire <-chan time.Time
		var timer *time.Timer
		arm := func() {
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			}
			fire = timer.C
		}
		for {
			select {
			case <-stop:
				if timer != nil {
					timer.Stop()
				}
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				name := filepath.Clean(ev.Name)
				if name != abs && name != target {
					continue
				}
				follow()
				arm()
			case <-check.C:
				if fp := fingerprint(); fp != seen && fire == nil {
					follow()
					arm()
				}
			case <-fire:
				fire = nil
				seen = fingerprint()
				select {
				case out <- struct{}{}:
				default:
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Warn("config watch error", "err", err)
			}
		}
	}()
	return out, nil
}

// configCheckInterval is how often the watcher looks at the config directly,
// for the changes file events do not report.
const configCheckInterval = time.Second

// activeHold stops a config reload from becoming a swap.
//
// A reload that drops the active account leaves the state with no active one
// (Reconcile clears it), and policy.Decide with no active account picks the
// best eligible account and says Switch. That is right for a daemon that has
// never seen a live account; after a reload it would replace a live credential
// that is still there and working, on the strength of an edit to a text file.
//
// So the daemon holds — but not indefinitely. It lifts as soon as the live
// credential is attributed to a configured account by the ordinary PollActive
// path, and otherwise after two active-poll intervals. The second matters: when
// the account removed is the one still signed in, nothing will ever attribute
// it, and a hold waiting for that would veto every switch until a restart.
type activeHold struct {
	lost  string    // the active account the reload removed; empty when not holding
	since time.Time // when the hold began
	now   func() time.Time
	log   *slog.Logger
}

func (h *activeHold) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// holdLimit is how long a hold may last: two active polls, enough for the
// poller to have tried to re-attribute the live credential twice.
func holdLimit(cfg *config.Config) time.Duration {
	if d := cfg.PollActive.Duration; d > 0 {
		return 2 * d
	}
	return 2 * time.Minute
}

// afterReload notes whether the reload took the profile's active account
// away: before and after are its active account around the reload. One hold
// per profile (profileLoop.hold).
func (h *activeHold) afterReload(before, after string) {
	if before != "" && before != state.Unattributed && after == "" {
		h.lost, h.since = before, h.clock()
	}
}

// holding reports whether a reload's hold is in force.
func (h *activeHold) holding() bool { return h.lost != "" }

func (h *activeHold) lift(why string) {
	if h.log != nil {
		h.log.Info("hold lifted; normal rotation applies again", "removed_account", h.lost,
			"because", why)
	}
	h.lost = ""
}

// gate turns a Switch into a Stay while holding, and lifts the hold for good
// once the live credential belongs to a configured account or the bound runs
// out.
func (h *activeHold) gate(dec policy.Decision, active string, cfg *config.Config) policy.Decision {
	if h.lost == "" {
		return dec
	}
	if active != "" && hasAccount(cfg, active) {
		h.lift(fmt.Sprintf("the live credential is attributed to %s", active))
		return dec
	}
	if limit := holdLimit(cfg); h.clock().Sub(h.since) >= limit {
		h.lift(fmt.Sprintf("no configured account claimed the live credential within %s", limit))
		return dec
	}
	if dec.Kind != policy.Switch {
		return dec
	}
	return policy.Decision{Kind: policy.Stay, Reason: fmt.Sprintf(
		"the active account %q left the config; holding until the live credential is attributed again "+
			"or %s passes (would have switched to %s: %s)", h.lost, holdLimit(cfg), dec.Target, dec.Reason)}
}
