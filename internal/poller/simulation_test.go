package poller

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// limitedAPI is the usage endpoint as GROUND_TRUTH §42 measured it, per
// account: a burst of 24 calls, regained here at one per two minutes, and
// once refused, refused for twelve minutes with Retry-After: 0. It is a little
// more generous in burst than the model the budget holds (20) and exactly as
// slow to refill, so a poller that trips it has outrun its own model.
type limitedAPI struct {
	now    func() time.Time
	accts  map[string]*simAccount // by token
	bucket map[string]*simBucket
	calls  map[string][]time.Time
	// refusals counts 429s answered to claudeswitch, by token.
	refusals map[string]int
	// outside counts calls made by Claude Code itself (see spendOutside), and
	// outsideRefused how many of those were refused.
	outside, outsideRefused int
}

type simAccount struct {
	id          string
	five        float64
	fiveResets  time.Time
	sevenResets time.Time
}

type simBucket struct {
	tokens    float64
	at        time.Time
	lockedTil time.Time
	low       float64
}

const (
	simBurst     = 24.0
	simRefill    = 2 * time.Minute
	simLockedFor = 12 * time.Minute
)

// take spends one call from tok's allowance, reporting whether it was granted.
func (l *limitedAPI) take(tok string) bool {
	now := l.now()
	b := l.bucket[tok]
	if b == nil {
		b = &simBucket{tokens: simBurst, at: now, low: simBurst}
		l.bucket[tok] = b
	}
	b.tokens += float64(now.Sub(b.at)) / float64(simRefill)
	if b.tokens > simBurst {
		b.tokens = simBurst
	}
	b.at = now
	if now.Before(b.lockedTil) {
		return false
	}
	if b.tokens < 1 {
		b.lockedTil = now.Add(simLockedFor)
		return false
	}
	b.tokens--
	if b.tokens < b.low {
		b.low = b.tokens
	}
	return true
}

// spendOutside is Claude Code calling the same endpoint with the live
// credential (GROUND_TRUTH §40): calls the budget cannot see.
func (l *limitedAPI) spendOutside(tok string) {
	l.outside++
	if !l.take(tok) {
		l.outsideRefused++
	}
}

func (l *limitedAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	l.calls[tok] = append(l.calls[tok], l.now())
	h := http.Header{}
	if !l.take(tok) {
		l.refusals[tok]++
		h.Set("Retry-After", "0")
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: h,
			Body: io.NopCloser(strings.NewReader(`{"type":"rate_limit_error"}`)), Request: r}, nil
	}
	a := l.accts[tok]
	h.Set("anthropic-organization-id", "org-"+a.id)
	body := usageBody(a.five, 20, a.fiveResets, a.sevenResets)
	return &http.Response{StatusCode: 200, Header: h,
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// workload is how fast the person spends the live account's five-hour window,
// in points a minute, through a working day.
func workload(t time.Time) float64 {
	hm := t.Hour()*60 + t.Minute()
	switch {
	case hm >= 9*60 && hm < 11*60:
		return 0.4
	case hm >= 11*60+30 && hm < 12*60+30:
		return 1.0
	case hm >= 12*60+30 && hm < 13*60+30:
		return 0.3
	case hm >= 14*60 && hm < 14*60+50:
		return 1.0 // 0 → 50, never in reach
	case hm >= 14*60+50 && hm < 15*60+3:
		// A hot spell that stops short of the trigger (50 → 76, hot from
		// 55), then a pause of ~50 minutes: the reading must not stall while
		// the spell's spend is regained.
		return 2.0
	case hm >= 15*60+55 && hm < 16*60+30:
		// A second, full spell on the same account: 76 → 85 at 0.6 a
		// minute, within reach from its first rise, about 15 minutes hot.
		return 0.6
	case hm >= 17*60 && hm < 18*60:
		return 1.0
	}
	return 0
}

// TestADayOfUsageDrawsNoRefusals replays a working day through the poller on
// a fake clock against an endpoint that enforces the measured per-account
// limit: the account in use burns to its trigger twice (rotating away each
// time), three accounts sit idle, Claude Code reads the live account's usage
// on its own twice an hour, and a transcript refusal forces a rotation in the
// middle. The daemon's loop is reduced to what touches the poller: a tick
// every five seconds, re-attribution every fifteen minutes, and a rotation —
// verified with a Swap-priority read, as the vault does — when the reading of
// the account in use reaches its trigger or the detector reports a refusal.
//
// It asserts zero 429s, that every rotation's verification was granted, and
// that while the account in use is hot and moving its reading is never older
// than poll_hot.
func TestADayOfUsageDrawsNoRefusals(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "missing.toml")) // the defaults
	cfg.Priority = []string{"a", "b", "c", "d"}
	cfg.Accounts = []config.Account{{ID: "a", OrgID: "org-a"}, {ID: "b", OrgID: "org-b"},
		{ID: "c", OrgID: "org-c"}, {ID: "d", OrgID: "org-d"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the default cadence must validate: %v", err)
	}
	pollHot := cfg.PollHot.Duration
	const step = 5 * time.Second

	clock := time.Date(2026, 10, 7, 7, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	start := clock
	st := &state.State{Accounts: map[string]*state.Account{},
		Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "a"}}}
	p := New(cfg, st, quiet())
	p.now = now
	p.budget = usage.NewBudgetWithClock(now, func(d time.Duration) { clock = clock.Add(d) })
	p.budget.SetAllowance(cfg.APIBudget)

	week := start.Add(4 * 24 * time.Hour)
	api := &limitedAPI{now: now, bucket: map[string]*simBucket{}, calls: map[string][]time.Time{},
		refusals: map[string]int{}, accts: map[string]*simAccount{
			"tok-a": {id: "a", five: 10, fiveResets: start.Add(6 * time.Hour), sevenResets: week},
			"tok-b": {id: "b", five: 20, fiveResets: start.Add(8 * time.Hour), sevenResets: week},
			"tok-c": {id: "c", five: 30, fiveResets: start.Add(9 * time.Hour), sevenResets: week},
			"tok-d": {id: "d", five: 50, fiveResets: start.Add(13 * time.Hour), sevenResets: week},
		}}
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}, Now: now}
	old := readVault
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id}}, nil
	}
	t.Cleanup(func() { readVault = old })
	live := &fakeItem{token: "tok-a"}
	p.SetLive(state.DefaultProfile, live)
	ctx := context.Background()

	type rotation struct {
		at, crossed time.Time
		from, to    string
		why         string
	}
	var rotations []rotation
	var crossedAt time.Time // when the live account truly reached its trigger
	refusalAt := time.Date(2026, 10, 7, 13, 30, 0, 0, time.UTC)
	var refusedStep time.Time
	var hotSteps, staleHot int
	var worstHotAge time.Duration
	wasHot := map[string]bool{}
	var spellEnded time.Time
	var spellAccount string
	var postSteps int
	const staleCap = 4 * time.Minute // cmd/claudeswitch staleDecisionAfter at the 3m default
	var followUp bool
	var worstFollowUpAge time.Duration
	// spell is a hot spell's start (with the account's allowance then) or end.
	type spell struct {
		account        string
		started, ended time.Time
		level          float64
	}
	var spells []spell
	var worstAnyAge time.Duration
	var worstAnyAt time.Time
	var staleAnySteps int
	var worstPostAge time.Duration

	rotate := func(why string) {
		from := st.Default().Active
		target, best := "", 101.0
		for _, id := range cfg.Priority {
			a := st.Accounts[id]
			if id == from || a == nil || a.Last == nil || clock.Before(a.BurntTil) {
				continue
			}
			if _, w := a.Last.Worst(); w < cfg.SwitchAt-cfg.Margin() && w < best {
				target, best = id, w
			}
		}
		if target == "" {
			t.Fatalf("%s: nowhere to rotate to at %s", why, clock.Format("15:04:05"))
		}
		acct := st.Get(target)
		if r := p.fetchInto(ctx, acct, "tok-"+target, usage.Swap); r != usage.ReasonOK || acct.LastErr != "" {
			t.Fatalf("%s: the swap check of %s at %s was refused: %q %s",
				why, target, clock.Format("15:04:05"), r, acct.LastErr)
		}
		live.token = "tok-" + target
		st.Default().SetActive(target)
		rotations = append(rotations, rotation{at: clock, crossed: crossedAt, from: from, to: target, why: why})
		crossedAt = time.Time{}
	}

	lastReattribute := clock
	if _, err := p.PollActiveIn(ctx, state.DefaultProfile); err != nil {
		t.Fatal(err)
	}
	for end := start.Add(16 * time.Hour); clock.Before(end); {
		clock = clock.Add(step)
		// The world: the person's work spends the live account; windows reset.
		for _, a := range api.accts {
			if !clock.Before(a.fiveResets) {
				a.five, a.fiveResets = 0, a.fiveResets.Add(5*time.Hour)
			}
		}
		la := api.accts[live.token]
		la.five += workload(clock) * step.Minutes()
		if la.five >= cfg.SwitchAt && crossedAt.IsZero() {
			crossedAt = clock
		}
		if m := clock.Minute(); (m == 15 || m == 45) && clock.Second() == 0 {
			api.spendOutside(live.token)
		}

		// The daemon.
		p.Tick(ctx)
		if clock.Sub(lastReattribute) >= ReattributeInterval {
			lastReattribute = clock
			if _, err := p.PollActiveIn(ctx, state.DefaultProfile); err != nil {
				t.Fatalf("re-attribution at %s: %v", clock.Format("15:04:05"), err)
			}
		}
		active := st.Default().Active
		if clock.Equal(refusalAt) {
			p.ApplyRejection(active, "seven_day", clock.Add(150*time.Minute))
			refusedStep = clock
			rotate("transcript refusal")
		} else if a := st.Accounts[active]; a != nil && a.Last != nil {
			if _, _, over := a.Last.WorstAgainst(cfg.SwitchAt, cfg.SwitchAtWeekly); over >= 0 {
				rotate("trigger")
			}
		}
		// Spell bookkeeping first, so the checks below know which spell this is.
		active = st.Default().Active
		if wasHot[active] && !p.hot[active] {
			spellEnded, spellAccount = clock, active
			spells = append(spells, spell{account: active, ended: clock})
		}
		if !wasHot[active] && p.hot[active] {
			// A follow-up spell starts within an hour of one on the same
			// account ending, before its hot reserve can have rebuilt.
			followUp = !spellEnded.IsZero() && spellAccount == active && clock.Sub(spellEnded) <= time.Hour
			spells = append(spells, spell{account: active, started: clock,
				level: p.budget.AccountLevel("tok-" + active)})
		}
		wasHot = map[string]bool{active: p.hot[active]}

		// The claim: hot and moving means a reading no older than poll_hot —
		// asserted for every spell that starts with its reserve rebuilt. A
		// follow-up spell is reported, and fails only past 2 minutes.
		if p.hot[active] && workload(clock) > 0 {
			hotSteps++
			age := clock.Sub(st.Accounts[active].LastAt)
			switch {
			case followUp:
				if age > worstFollowUpAge {
					worstFollowUpAge = age
				}
				if age > 2*time.Minute {
					t.Errorf("%s: %s, in a follow-up spell, has a reading %v old (limit 2m)",
						clock.Format("15:04:05"), active, age)
				}
			default:
				if age > worstHotAge {
					worstHotAge = age
				}
				if age > pollHot {
					staleHot++
					if staleHot <= 3 {
						t.Errorf("%s: %s is hot and moving but its reading is %v old (poll_hot %v)",
							clock.Format("15:04:05"), active, age, pollHot)
					}
				}
			}
		}

		// Never a stale decision: the reading of the account in use stays
		// under the daemon's stale-decision cap (staleDecisionAfter, 4m at the
		// 3m default) all day, after hot spells included (owner decision
		// 2026-10-07).
		if age := clock.Sub(st.Accounts[active].LastAt); age > worstAnyAge {
			worstAnyAge, worstAnyAt = age, clock
		}
		if age := clock.Sub(st.Accounts[active].LastAt); age >= staleCap {
			staleAnySteps++
			if staleAnySteps <= 3 {
				t.Errorf("%s: %s's reading is %v old, at or past the %v stale-decision cap",
					clock.Format("15:04:05"), active, age, staleCap)
			}
		}
		if !spellEnded.IsZero() && active == spellAccount && clock.Sub(spellEnded) <= 30*time.Minute {
			postSteps++
			if age := clock.Sub(st.Accounts[active].LastAt); age > worstPostAge {
				worstPostAge = age
			}
		}
	}

	// Zero refusals of our calls, all day.
	total := 0
	for tok, n := range api.refusals {
		total += n
		t.Errorf("%s was refused %d time(s)", tok, n)
	}
	if api.outsideRefused > 0 {
		t.Errorf("Claude Code's own reads were refused %d of %d times: the poller left it no room",
			api.outsideRefused, api.outside)
	}

	// The shape of the day: a reached its trigger twice, b was refused, and
	// each trigger was acted on within poll_hot of the true crossing.
	fromA := 0
	for _, r := range rotations {
		t.Logf("rotation %s %s -> %s (%s)", r.at.Format("15:04:05"), r.from, r.to, r.why)
		if r.why == "trigger" {
			if r.from == "a" {
				fromA++
			}
			if lag := r.at.Sub(r.crossed); r.crossed.IsZero() || lag > pollHot {
				t.Errorf("the trigger on %s was acted on %v after it was crossed, want within poll_hot (%v)",
					r.from, lag, pollHot)
			}
		}
	}
	if fromA != 2 {
		t.Errorf("a burned to its trigger %d time(s), want 2 — the scenario did not play out", fromA)
	}
	if refusedStep.IsZero() || len(rotations) < 2 || !rotations[1].at.Equal(refusedStep) {
		t.Errorf("the transcript refusal at %s was not acted on in the same step", refusalAt.Format("15:04"))
	}
	if postSteps == 0 {
		t.Error("no hot spell ended without a rotation: the post-spell check exercised nothing")
	}
	t.Logf("for 30 minutes after a hot spell: %v observed, oldest reading %v", time.Duration(postSteps)*step, worstPostAge)

	// A second full spell on the same account, 40–60 minutes after the first
	// ended without a rotation: how full was the hot reserve when it began?
	var lastEnd time.Time
	var lastEndAccount string
	second := false
	for _, s := range spells {
		if !s.ended.IsZero() {
			lastEnd, lastEndAccount = s.ended, s.account
			t.Logf("hot spell on %s ended %s", s.account, s.ended.Format("15:04:05"))
			continue
		}
		above := s.level - float64(usage.AccountReserve)
		t.Logf("hot spell on %s began %s with %.1f calls in its allowance: %.1f above the swap reserve, "+
			"the hot reserve %.0f%% full", s.account, s.started.Format("15:04:05"), s.level, above,
			100*min(above/float64(cfg.HotReserveCalls()), 1))
		if gap := s.started.Sub(lastEnd); s.account == lastEndAccount && !lastEnd.IsZero() &&
			gap >= 40*time.Minute && gap <= 60*time.Minute {
			second = true
			t.Logf("  %v after the previous spell on the same account ended", gap)
		}
	}
	t.Logf("the account in use: oldest reading all day %v (at %s); %v in all at or past the 4m stale-decision cap",
		worstAnyAge, worstAnyAt.Format("15:04:05"), time.Duration(staleAnySteps)*step)
	t.Logf("follow-up spell: oldest hot reading %v (poll_hot %v; fails past 2m)", worstFollowUpAge, pollHot)
	if !second {
		t.Error("no second spell on the same account 40–60 minutes after the first: the scenario did not play out")
	}
	if hotSteps == 0 {
		t.Error("the account in use was never hot: the scenario exercised nothing")
	}

	// The numbers, for the record.
	toks := make([]string, 0, len(api.calls))
	for tok := range api.calls {
		toks = append(toks, tok)
	}
	sort.Strings(toks)
	for _, tok := range toks {
		calls := api.calls[tok]
		peak := 0
		for i := range calls {
			n := 0
			for j := i; j < len(calls) && calls[j].Sub(calls[i]) < 15*time.Minute; j++ {
				n++
			}
			if n > peak {
				peak = n
			}
		}
		t.Logf("%s: %d calls in 16h, at most %d in any 15 minutes, endpoint allowance never below %.1f of %.0f",
			tok, len(calls), peak, api.bucket[tok].low, simBurst)
	}
	t.Logf("hot and moving for %v; oldest reading then %v; Claude Code read %d times unrefused",
		time.Duration(hotSteps)*step, worstHotAge, api.outside-api.outsideRefused)
	_ = total
}
