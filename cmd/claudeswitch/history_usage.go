package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/readings"
)

// `cs history --usage [--days 30] [--json]` (IMPROVEMENTS F7): each
// account's session and weekly utilization over the span, from the readings
// log the daemon keeps (internal/readings), and the switches from the audit
// log. It reads both files and the config, and writes nothing.

// historyUsage reports the readings and switches since now - days.
func historyUsage(w io.Writer, cfgPath string, days int, asJSON bool, now time.Time) error {
	from := now.Add(-time.Duration(days) * 24 * time.Hour)
	rs, err := readings.Read("", from)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	// The config only names and orders the accounts; without one the log's
	// own names are reported.
	cfg, cerr := config.Load(cfgPath)
	if cerr != nil {
		cfg = nil
		if !asJSON {
			fmt.Fprintf(os.Stderr, "note: %v\n", cerr)
		}
	}

	type point = map[string]any
	series := map[string][]point{}
	for _, r := range rs {
		if r.At.After(now) {
			continue
		}
		series[r.Account] = append(series[r.Account], point{"at": r.At.UTC(), "five_hour": r.FiveHour,
			"seven_day": r.SevenDay})
	}
	var ids []string
	configured := map[string]bool{}
	if cfg != nil {
		for _, a := range listedAccounts(cfg) {
			ids = append(ids, a.ID)
			configured[a.ID] = true
		}
	}
	var others []string
	for id := range series {
		if !configured[id] {
			others = append(others, id)
		}
	}
	sort.Strings(others)
	ids = append(ids, others...)

	accounts := []map[string]any{}
	for _, id := range ids {
		var owner string
		if cfg != nil {
			owner, _ = cfg.ProfileOf(id)
		}
		s := series[id]
		if s == nil {
			s = []point{}
		}
		accounts = append(accounts, map[string]any{"id": id, "profile": orNull(owner),
			"configured": configured[id], "series": s})
	}

	events, err := audit.Tail("", math.MaxInt)
	if err != nil {
		return wrapErr(codeFailed, "", err)
	}
	switches := []map[string]any{}
	for _, e := range events {
		if e.Kind != "switch" || e.At.Before(from) || e.At.After(now) {
			continue
		}
		switches = append(switches, map[string]any{"at": e.At.UTC(), "profile": orNull(e.Profile),
			"from": orNull(e.From), "to": orNull(e.To), "reason": orNull(e.Reason), "forced": e.Forced})
	}

	if asJSON {
		return emitTo(w, map[string]any{"days": days, "from": from.UTC(), "to": now.UTC(),
			"accounts": accounts, "switches": switches})
	}
	fmt.Fprintf(w, "\n  usage over the last %d day(s), from the daemon's readings log\n\n", days)
	for _, a := range accounts {
		s := a["series"].([]point)
		if len(s) == 0 {
			fmt.Fprintf(w, "  %-18s no readings\n", a["id"])
			continue
		}
		peak5, peak7 := "-", "-"
		var m5, m7 float64
		for _, p := range s {
			if v, ok := p["five_hour"].(*float64); ok && v != nil && *v >= m5 {
				m5, peak5 = *v, fmt.Sprintf("%.0f%%", *v)
			}
			if v, ok := p["seven_day"].(*float64); ok && v != nil && *v >= m7 {
				m7, peak7 = *v, fmt.Sprintf("%.0f%%", *v)
			}
		}
		last := s[len(s)-1]["at"].(time.Time)
		fmt.Fprintf(w, "  %-18s %d readings · peak 5h %s · 7d %s · last %s\n", a["id"], len(s), peak5, peak7,
			last.Local().Format("Mon 02 Jan 15:04"))
	}
	fmt.Fprintf(w, "\n  %d switches; `cs history --usage --json` has every reading and switch\n\n", len(switches))
	return nil
}
