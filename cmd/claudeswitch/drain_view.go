package main

import (
	"math"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// The drain forecast (IMPROVEMENTS "Drain forecast") as the views ask for it:
// the daemon measures each account's week speed into state, and these turn a
// record into an estimate.

// switchPointOf is where account id leaves rotation on its week: its
// ignore_weekly_gte, else the week trigger of the first profile that uses it.
func switchPointOf(cfg *config.Config, id string) float64 {
	pcfg := cfg
	if owners := cfg.ProfilesOf(id); len(owners) > 0 {
		pcfg = cfg.ForProfile(owners[0])
	}
	return pcfg.ForAccount(id).TriggerFor(usage.SevenDayKey)
}

// estimateOf is account id's drain forecast at now.
func estimateOf(cfg *config.Config, st *state.State, id string, now time.Time) drain.Estimate {
	return drain.For(st.Accounts[id], switchPointOf(cfg, id), activeIn(cfg, st, id) != "", now)
}

// anyMeasured reports whether a running daemon has measured any configured
// account lately: without that there is nothing to forecast, and a view
// leaves the forecast out rather than show a column of dashes.
func anyMeasured(cfg *config.Config, st *state.State, now time.Time) bool {
	for _, a := range cfg.Accounts {
		if drain.Measured(st.Accounts[a.ID], now) {
			return true
		}
	}
	return false
}

// round1 is v to one decimal, for JSON a person may read.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// drainJSON is an estimate for --json: state (hours, lasts_to_reset, idle,
// drained or unknown), hours_left (hours of use before the switch point,
// null unless hours or lasts_to_reset), speed_per_hour (week points an hour
// of use, null unless measured lately) and used_elsewhere.
func drainJSON(e drain.Estimate, measured bool) map[string]any {
	m := map[string]any{"state": string(e.Kind), "hours_left": nil, "speed_per_hour": nil,
		"used_elsewhere": e.Elsewhere}
	if e.Kind == drain.Hours || e.Kind == drain.ToReset {
		m["hours_left"] = round1(e.Left.Hours())
	}
	if measured {
		m["speed_per_hour"] = round1(e.PerHour)
	}
	return m
}
