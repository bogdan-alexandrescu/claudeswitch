package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// `cs account list` (lane 15): every configured account from config.toml,
// state.json and the account numbers alone. It never reads the keychain — no vault entry, no live
// item — so the app may run it on every refresh without a prompt (M1).
// What it shows about a credential is what the CLI and the daemon recorded
// in state: the email and plan when it was vaulted, identified or polled,
// the expiries the daemon saw, the last reading.

// Account states, as `account list --json` reports them.
const (
	acctAvailable  = "available"   // the last reading, still current, says it can serve
	acctReserved   = "reserved"    // over its configured reserve
	acctRefused    = "refused"     // the API refused it; reading.refused_until says until when
	acctNeedsLogin = "needs_login" // its credential is dead: only a sign-in brings it back
	acctUnknown    = "unknown"     // never read, or the reading outlived its window
)

// accountState folds what state recorded about an account into one word.
// A dead credential first: nothing else about the account can be trusted
// then.
func accountState(acct *state.Account, reserve float64, now time.Time) string {
	if acct == nil {
		return acctUnknown
	}
	if acct.NeedsSignIn(now) {
		return acctNeedsLogin
	}
	switch acct.AvailabilityAt(reserve, now) {
	case state.Burnt:
		return acctRefused
	case state.Available:
		return acctAvailable
	case state.Reserved:
		return acctReserved
	}
	return acctUnknown
}

// setOrNull is an account setting for JSON, where 0 means it has none.
func setOrNull(v float64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// readingJSON is the last reading in brief, or null when state holds
// neither a reading nor an error for the account. Its binding window is the
// one nearest its own trigger, as the daemon judges it.
func readingJSON(cfg *config.Config, acct *state.Account, now time.Time) any {
	if acct == nil || (acct.Last == nil && acct.LastErr == "") {
		return nil
	}
	m := map[string]any{
		"five_hour": nil, "seven_day": nil, "binding": nil, "at": timeOrNull(acct.LastAt),
		"error": orNull(acct.LastErr), "refused_until": nil, "refused_window": nil,
		"five_hour_resets_at": nil, "seven_day_resets_at": nil,
	}
	if u := acct.Last; u != nil {
		if u.FiveHour.Known() {
			m["five_hour"] = u.FiveHour.Pct()
		}
		if u.SevenDay.Known() {
			m["seven_day"] = u.SevenDay.Pct()
		}
		// When each window clears, as the usage API last said.
		if r := u.FiveHour.ResetsAt; r != nil && !r.IsZero() {
			m["five_hour_resets_at"] = r.UTC()
		}
		if r := u.SevenDay.ResetsAt; r != nil && !r.IsZero() {
			m["seven_day_resets_at"] = r.UTC()
		}
		if which, _, _ := u.WorstAgainst(cfg.TriggerFor(usage.FiveHourKey), cfg.TriggerFor(usage.SevenDayKey)); which != "" {
			m["binding"] = which
		}
	}
	if !acct.BurntTil.IsZero() && now.Before(acct.BurntTil) {
		m["refused_until"] = acct.BurntTil.UTC()
		m["refused_window"] = orNull(acct.BurntWin)
	}
	return m
}

// listedAccounts is every configured account: the enabled ones in rotation
// order, then the disabled ones in config order.
func listedAccounts(cfg *config.Config) []config.Account {
	out := cfg.Ordered()
	for _, a := range cfg.Accounts {
		if !a.IsEnabled() {
			out = append(out, a)
		}
	}
	return out
}

// accountJSON is one account as `account list --json` reports it.
func accountJSON(cfg *config.Config, st *state.State, a config.Account, now time.Time) map[string]any {
	acct := st.Accounts[a.ID]
	// D31: every profile that may use it; "profile" stays, the first of
	// them, for readers older than shared pools.
	profiles := nonNilList(cfg.ProfilesOf(a.ID))
	owner := ""
	if len(profiles) > 0 {
		owner = profiles[0]
	}
	pool, _ := cfg.PoolOf(a.ID)
	activeIn, pinned, pinHard := "", false, false
	for _, name := range cfg.ProfileNames() {
		ps := st.Profiles[name]
		if ps == nil {
			continue
		}
		if ps.Active == a.ID && activeIn == "" {
			activeIn = name
		}
		if ps.Pinned == a.ID {
			pinned = true
			pinHard = pinHard || ps.PinHard
		}
	}
	m := map[string]any{
		"id": a.ID, "email": orNull(st.EmailOf(a.ID)), "plan": orNull(st.PlanOf(a.ID)),
		"seat": orNull(a.Seat()), "enabled": a.IsEnabled(),
		"profile": orNull(owner), "pool": orNull(pool), "profiles": profiles,
		"active_in": orNull(activeIn), "pinned": pinned, "pin_hard": pinHard,
		"refresh_expires_at": nil, "access_expires_at": nil,
		"state": accountState(acct, a.Reserve, now), "reading": readingJSON(cfg.ForAccount(a.ID), acct, now),
		"ignore_weekly_gte": setOrNull(a.IgnoreWeeklyGTE), "pick_priority": setOrNull(float64(a.PickPriority)),
	}
	if acct != nil {
		m["refresh_expires_at"] = timeOrNull(acct.RefreshExpiry)
		m["access_expires_at"] = timeOrNull(acct.TokenExpiry)
	}
	return m
}

// accountStateWord is an account state as the table says it: the app's and
// the rest of the CLI's words, not the JSON's.
func accountStateWord(state string) string {
	if state == acctNeedsLogin {
		return "needs sign-in"
	}
	return strings.ReplaceAll(state, "_", " ")
}

// accountList is `cs account list [--json]`.
func accountList(w io.Writer, cfgPath string, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	now := time.Now()
	list := make([]map[string]any, 0, len(cfg.Accounts))
	// The list still answers without its numbers: they are a convenience,
	// and everything else here is what someone came for.
	numbers, numErr := state.AccountNumbers(listedIDs(cfg))
	for _, a := range listedAccounts(cfg) {
		m := accountJSON(cfg, st, a, now)
		m["number"] = nil
		if n, ok := numbers[a.ID]; ok {
			m["number"] = n
		}
		list = append(list, m)
	}
	if numErr != nil {
		fmt.Fprintf(os.Stderr, "claudeswitch: account numbers are unavailable: %v\n", numErr)
	}
	if asJSON {
		return emitTo(w, map[string]any{"accounts": list})
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "  no accounts are configured")
		return nil
	}
	str := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return "-"
	}
	var rows []accountRow
	for _, m := range list {
		id := m["id"].(string)
		if m["pinned"] == true {
			id += " (pinned)"
		}
		// Every profile that may use it, the one it is live in starred: a
		// shared account is live in one profile at a time (D34), not
		// necessarily the first.
		var profiles []string
		for _, p := range m["profiles"].([]string) {
			if p == m["active_in"] {
				p += "*"
			}
			profiles = append(profiles, p)
		}
		stateWord := accountStateWord(m["state"].(string))
		if m["enabled"] == false {
			stateWord = "disabled, " + stateWord
		}
		number := "-"
		if n, ok := m["number"].(int); ok {
			number = strconv.Itoa(n)
		}
		r := accountRow{cells: []string{number, id, nonEmpty(strings.Join(profiles, ", "), "-"), stateWord},
			plan: str(m["plan"]), email: str(m["email"]), id: m["id"].(string)}
		_, r.live = m["active_in"].(string)
		owners := m["profiles"].([]string)
		r.profiles = nonEmpty(strings.Join(owners, ", "), "-")
		if len(owners) > 0 {
			r.owner = owners[0]
		}
		if acct := st.Accounts[r.id]; acct != nil {
			r.last = acct.Last
		}
		if rd, ok := m["reading"].(map[string]any); ok {
			if v, ok := rd["five_hour"].(float64); ok {
				r.five = &v
			}
			if v, ok := rd["seven_day"].(float64); ok {
				r.seven = &v
			}
			if v, ok := rd["five_hour_resets_at"].(time.Time); ok {
				r.r5 = &v
			}
			if v, ok := rd["seven_day_resets_at"].(time.Time); ok {
				r.r7 = &v
			}
		}
		rows = append(rows, r)
	}
	// Each figure is followed by when its window clears: a time of day
	// where the terminal has room, else how long until then, else nothing.
	// The email is the column that gives way first; it keeps emailRoom.
	width := render.Width()
	if headerOn() && writeAccountBox(w, cfg, st, rows, width, now) {
		return nil
	}
	figure := func(v *float64, at *time.Time, form int) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("%.0f%%", *v) + accountClears(at, form, now)
	}
	head := []string{"#", "ACCOUNT", "PROFILES", "STATE", "SESSION", "WEEK", "PLAN", "EMAIL"}
	build := func(form int) [][]string {
		var out [][]string
		for _, r := range rows {
			out = append(out, append(append([]string{}, r.cells...),
				figure(r.five, r.r5, form), figure(r.seven, r.r7, form), r.plan, r.email))
		}
		return out
	}
	var cells [][]string
	for form := 0; form <= 2; form++ {
		cells = build(form)
		if form == 2 || tableWidth(head, cells)+emailRoom <= width {
			break
		}
	}
	// The email goes last: it is the column a narrow terminal can cut.
	t := render.NewTable(head, 0, 4, 5).Fit(width)
	for _, c := range cells {
		t.Add(c...)
	}
	fmt.Fprintf(w, "\n%s", t.Render("  "))
	fmt.Fprintln(w, "\n  cs switch <#> to use that account · * live now · figures as last recorded")
	return nil
}

// emailRoom is what the email column keeps when a terminal is too narrow for
// a whole address.
const emailRoom = 12

// accountRow is one account in the text forms of `cs account list`.
type accountRow struct {
	cells       []string // number, name, profiles, state
	five, seven *float64
	r5, r7      *time.Time
	plan, email string
	id, owner   string
	profiles    string // without the piped form's star: ▸ says it here
	live        bool
	last        *usage.Usage
}

// accountClears is what follows a figure: when its window clears, as a time
// of day (form 0), how long until then (1), or nothing (2).
func accountClears(at *time.Time, form int, now time.Time) string {
	switch {
	case form == 2 || at == nil:
		return ""
	case !at.After(now):
		return " · now"
	case form == 0:
		return " · " + render.When(*at, now)
	}
	return " · " + render.Duration(at.Sub(now))
}

// writeAccountBox writes the list as a bordered table in the status view's
// colours and marks, for a terminal. It gives up what matters least until the
// table fits: the time of day for a countdown, then the countdown, the email
// and the plan. It writes nothing and reports false when even that is too
// wide, and the plain table is drawn instead: a box that wraps is unreadable.
func writeAccountBox(w io.Writer, cfg *config.Config, st *state.State, rows []accountRow, width int, now time.Time) bool {
	figure := func(r accountRow, v *float64, at *time.Time, window string, form int) string {
		if v == nil {
			return "-"
		}
		pcfg := cfg
		if r.owner != "" {
			pcfg = cfg.ForProfile(r.owner)
		}
		cell := render.Figure(*v, pcfg.ForAccount(r.id).TriggerFor(window), r.last, window)
		if after := accountClears(at, form, now); after != "" {
			cell += render.Grey(after)
		}
		return cell
	}
	shapes := []struct {
		form        int
		plan, email bool
	}{{0, true, true}, {1, true, true}, {2, true, true}, {2, true, false}, {2, false, false}}
	for _, s := range shapes {
		head := []string{"#", "ACCOUNT", "PROFILES", "STATE", "SESSION", "WEEK"}
		if s.plan {
			head = append(head, "PLAN")
		}
		if s.email {
			head = append(head, "EMAIL")
		}
		var cells [][]string
		for _, r := range rows {
			name := "  " + r.cells[1]
			if r.live {
				name = render.Accent("▸") + " " + render.Bold(r.cells[1])
			}
			row := []string{r.cells[0], name, r.profiles, render.ColorState(r.cells[3]),
				figure(r, r.five, r.r5, usage.FiveHourKey, s.form),
				figure(r, r.seven, r.r7, usage.SevenDayKey, s.form)}
			if s.plan {
				row = append(row, r.plan)
			}
			if s.email {
				row = append(row, r.email)
			}
			cells = append(cells, row)
		}
		lastAtMost := width
		if s.email {
			lastAtMost = emailRoom
		}
		if render.BoxWidth(head, cells, lastAtMost) > width {
			continue
		}
		t := render.NewTable(head, 0, 4, 5).Fit(width)
		for i, c := range cells {
			t.Add(c...)
			if rows[i].live {
				t.Highlight(i)
			}
		}
		printHeader(w, st)
		fmt.Fprintf(w, "\n%s", t.RenderBox())
		fmt.Fprintln(w, "\n  ▸ live now · ! limit warning · cs switch <#> to use it")
		if key := render.ColourKey(); key != "" {
			fmt.Fprintln(w, key)
		}
		return true
	}
	return false
}

// tableWidth is how wide a render.Table of head and rows is, indented two
// columns, without its last column.
func tableWidth(head []string, rows [][]string) int {
	n := len(head) - 1
	w := 2 + 2*n
	for i := 0; i < n; i++ {
		c := utf8.RuneCountInString(head[i])
		for _, r := range rows {
			c = max(c, utf8.RuneCountInString(r[i]))
		}
		w += c
	}
	return w
}
