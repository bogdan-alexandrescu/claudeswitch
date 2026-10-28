package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Giving up an account held by an idle profile (owner request and decisions
// 2026-10-23). One account cannot be live in two profiles: a refresh in one
// logs the other out (D18). But a profile nobody is using held its account
// from every other profile until the owner moved it by hand. Now, when one
// profile wants an account live in another whose sessions have been idle for
// release_idle_after, that profile is first moved to the best other account
// of its own pools, and the account is free.

// idleHolder reports how long profile holder has been idle, and whether that
// is long enough to give up its account. Its last sign of work is the newest
// of the transcript write the daemon recorded (ActivityAt), its last
// claude.ai call and its last switch. A pinned profile is never idle enough:
// a pin is a promise that nothing moves it. With no sign of work recorded at
// all, it counts as idle only once the daemon has been recording for
// release_idle_after (recordingSince; zero is not known, so never).
func idleHolder(cfg *config.Config, st *state.State, holder string, now, recordingSince time.Time) (time.Duration, bool) {
	w := cfg.ReleaseIdle()
	ps := st.Profiles[holder]
	if w <= 0 || ps == nil || ps.Pinned != "" {
		return 0, false
	}
	last := ps.ActivityAt
	for _, t := range []time.Time{ps.ClaudeAIAt, ps.LastSwitch} {
		if t.After(last) {
			last = t
		}
	}
	if last.IsZero() {
		if recordingSince.IsZero() {
			return 0, false
		}
		last = recordingSince
	}
	idle := max(now.Sub(last), 0)
	return idle, idle >= w
}

// releaseFrom moves the idle profile holder off account, onto the best other
// account of its own pools, so wantedBy can have it. It returns that account,
// or why there is none (then nothing moved). The caller holds the switch
// lock. Recorded like any switch, with the reason, in state and the audit
// log; no Chrome profile is opened for it, since nobody is using the profile.
func releaseFrom(ctx context.Context, v swapVault, cfg *config.Config, st *state.State, holder string,
	live keychain.Live, account, wantedBy string, idle time.Duration, log *slog.Logger,
	record func(audit.Event)) (string, error) {
	now := time.Now()
	var view profileView
	found := false
	for _, pv := range profileViews(cfg, st, holder) {
		view, found = pv, true
	}
	if !found {
		return "", fmt.Errorf("no profile %s", holder)
	}
	in := view.input(st, now)
	// Its choice passes over everything live elsewhere, idle or not: one
	// release, never a chain of them.
	in.Unavailable = bestExclusions(cfg, st, holder, now, time.Time{})
	c := policy.Choose(in, holder)
	if c.ID == "" {
		return "", fmt.Errorf("%s has no other account to move to (%s)", holder, c.Why)
	}
	expectOrg := ""
	for _, a := range cfg.Accounts {
		if a.ID == c.ID {
			expectOrg = a.OrgID
		}
	}
	if expectOrg == "" {
		if a, ok := st.Accounts[c.ID]; ok {
			expectOrg = a.OrgID
		}
	}
	res, err := swapInto(ctx, v, cfg, holder, account, live, c.ID, expectOrg, log)
	if err != nil {
		return "", err
	}
	ps := st.Profile(holder)
	ps.SetActive(c.ID)
	ps.LiveKey, ps.LiveOrg = res.LiveKey, res.OrgID
	ps.LastSwitch, ps.LastFrom = now, account
	if res.Usage != nil {
		a := st.Get(c.ID)
		a.Last, a.LastAt, a.OrgID = res.Usage, res.Usage.FetchedAt, res.OrgID
	}
	reason := fmt.Sprintf("released %s for %s: %s idle %s", account, wantedBy, holder, render.Duration(idle))
	if record != nil {
		record(audit.Event{Kind: "switch", Profile: holder, From: account, To: c.ID, Reason: reason})
	}
	log.Info("gave up an account to another profile", "profile", holder, "from", account, "to", c.ID,
		"wanted_by", wantedBy, "idle", idle.Round(time.Minute))
	return c.ID, nil
}
