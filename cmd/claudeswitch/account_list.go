package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// `cs account list` (lane 15): every configured account from config.toml and
// state.json alone. It never reads the keychain — no vault entry, no live
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
	}
	if u := acct.Last; u != nil {
		if u.FiveHour.Known() {
			m["five_hour"] = u.FiveHour.Pct()
		}
		if u.SevenDay.Known() {
			m["seven_day"] = u.SevenDay.Pct()
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
		"state": accountState(acct, a.Reserve, now), "reading": readingJSON(cfg, acct, now),
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
	for _, a := range listedAccounts(cfg) {
		list = append(list, accountJSON(cfg, st, a, now))
	}
	if asJSON {
		return emitTo(w, map[string]any{"accounts": list})
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "  no accounts are configured")
		return nil
	}
	// The email goes last: it is the column a narrow terminal can cut.
	t := render.NewTable([]string{"ACCOUNT", "PROFILES", "STATE", "SESSION", "WEEK", "PLAN", "EMAIL"}, 3, 4).
		Fit(render.Width())
	str := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return "-"
	}
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
		five, seven := "-", "-"
		if rd, ok := m["reading"].(map[string]any); ok {
			if v, ok := rd["five_hour"].(float64); ok {
				five = fmt.Sprintf("%.0f%%", v)
			}
			if v, ok := rd["seven_day"].(float64); ok {
				seven = fmt.Sprintf("%.0f%%", v)
			}
		}
		t.Add(id, nonEmpty(strings.Join(profiles, ", "), "-"), stateWord, five, seven, str(m["plan"]), str(m["email"]))
	}
	fmt.Fprintf(w, "\n%s", t.Render("  "))
	fmt.Fprintln(w, "\n  * where the account is live · emails, plans and figures as last recorded")
	return nil
}
