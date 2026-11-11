package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// cmdForecast is `cs forecast`: the drain forecast (IMPROVEMENTS "Drain
// forecast") in full. Every account's week, switch point, speed, hours of use
// left and when its week clears, then how long each profile's pool lasts.
func cmdForecast(args []string) error {
	fs := flag.NewFlagSet("forecast", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	only := fs.String("profile", "", "show only this Claude Code profile's pool and accounts (default: every one)")
	if err := noArgs("forecast", parseInterleaved(fs, args)); err != nil {
		return err
	}
	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if err := checkProfileFlag(cfg, *only); err != nil {
		return err
	}
	now := time.Now()
	if *asJSON {
		return emitJSON(forecastJSON(cfg, st, now, *only))
	}
	printHeader(os.Stdout, st)
	renderForecast(os.Stdout, cfg, st, now, *only)
	return nil
}

// forecastAccounts is the accounts cs forecast shows: every one, or with
// --profile, that profile's pool.
func forecastAccounts(cfg *config.Config, st *state.State, only string) []string {
	var pool []string
	if only != "" {
		if vs := profileViews(cfg, st, only); len(vs) == 1 {
			pool = vs[0].pool
		}
	}
	var ids []string
	for _, a := range cfg.Accounts {
		if pool == nil || slices.Contains(pool, a.ID) {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// forecastPool is a view's pool line: its name as the status view gives it,
// and its forecast.
func forecastPool(v profileView, st *state.State, now time.Time) (profile string, pools int, p drain.PoolEstimate) {
	if v.declared {
		profile, pools = v.in.Name, len(v.in.Pools)
	}
	return profile, pools, v.drainPool(st, now)
}

func renderForecast(w io.Writer, cfg *config.Config, st *state.State, now time.Time, only string) {
	t := render.NewTable([]string{"ACCOUNT", "WEEK", "SWITCH AT", "SPEED", "LASTS", "CLEARS"}, 1, 2, 3)
	elsewhere := false
	for _, id := range forecastAccounts(cfg, st, only) {
		e := estimateOf(cfg, st, id, now)
		elsewhere = elsewhere || e.Elsewhere
		mark := "  "
		if activeIn(cfg, st, id) != "" {
			mark = "▸ "
		}
		week, clears := "-", "-"
		if a := st.Accounts[id]; a != nil && a.Last != nil && a.Last.SevenDay.Known() {
			week = fmt.Sprintf("%.0f%%", a.Last.SevenDay.Pct())
			if r := a.Last.SevenDay.ResetsAt; r != nil && !r.IsZero() {
				clears = render.When(*r, now)
			}
		}
		speed := "-"
		if drain.Measured(st.Accounts[id], now) && e.PerHour > 0 {
			speed = fmt.Sprintf("%g/h", round1(e.PerHour))
		}
		t.Add(mark+id, week, fmt.Sprintf("%g%%", switchPointOf(cfg, id)), speed, render.Lasts(e), clears)
	}
	width := render.Width()
	say := func(s string) {
		for _, l := range render.Wrap(s, width-2) {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
	fmt.Fprintf(w, "\n%s\n", t.Fit(width).Render("  "))
	for _, v := range profileViews(cfg, st, only) {
		profile, pools, p := forecastPool(v, st, now)
		say(render.PoolLine(profile, pools, &p, true))
	}
	if !anyMeasured(cfg, st, now) {
		fmt.Fprintln(w)
		say("no speed measured in the last 30 minutes: the daemon measures them as it runs (cs daemon status)")
	}
	parts := []string{"▸ live now"}
	if elsewhere {
		parts = append(parts, render.ElsewhereMark+" used elsewhere")
	}
	parts = append(parts, "SPEED: week points an hour of use", "LASTS: hours of use before the switch point")
	fmt.Fprintf(w, "\n%s", render.Joined(parts, "  ", width))
}

// forecastJSON is cs forecast --json: accounts, each with its id, whether it
// is live here, its week (null unread), switch_at, week_clears_at (null
// unknown) and drain (as cs list --json); and pools, one per profile, with
// its state (hours, idle, drained or unknown), hours_left and speed_per_hour
// (null unless hours) and accounts (how many have room).
func forecastJSON(cfg *config.Config, st *state.State, now time.Time, only string) map[string]any {
	accounts := []map[string]any{}
	for _, id := range forecastAccounts(cfg, st, only) {
		a := st.Accounts[id]
		m := map[string]any{"id": id, "live": activeIn(cfg, st, id) != "", "week": nil,
			"switch_at": switchPointOf(cfg, id), "week_clears_at": nil,
			"drain": drainJSON(estimateOf(cfg, st, id, now), drain.Measured(a, now))}
		if a != nil && a.Last != nil && a.Last.SevenDay.Known() {
			m["week"] = a.Last.SevenDay.Pct()
			if r := a.Last.SevenDay.ResetsAt; r != nil && !r.IsZero() {
				m["week_clears_at"] = r.UTC()
			}
		}
		accounts = append(accounts, m)
	}
	pools := []map[string]any{}
	for _, v := range profileViews(cfg, st, only) {
		_, _, p := forecastPool(v, st, now)
		m := poolDrainJSON(p)
		m["profile"] = v.in.Name
		pools = append(pools, m)
	}
	return map[string]any{"accounts": accounts, "pools": pools}
}
