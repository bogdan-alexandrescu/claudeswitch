// Package usage talks to the endpoint behind Claude Code's /usage command.
//
// Ground truth #11-#14:
//   - GET https://api.anthropic.com/api/oauth/usage returns exact utilization.
//   - It authenticates with whatever token is presented, so an account can be
//     polled WITHOUT switching to it. That is the fact the whole daemon rests on.
//   - It is rate limited to roughly 5 calls / 5 minutes, answering 429 with
//     retry-after: 299. There are no rate-limit headers on a 200, so the budget
//     can only be learned by exhausting it. Be conservative by construction.
//   - Every 200 carries anthropic-organization-id, identifying the account the
//     token belongs to. That is how a swap is verified.
//
// This is an undocumented endpoint. It lives behind this one adapter so that a
// shape change fails loudly in exactly one place.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	Endpoint    = "https://api.anthropic.com/api/oauth/usage"
	betaHeader  = "oauth-2025-04-20"
	userAgent   = "claude-cli/2.1.266 (external, cli)"
	FiveHourKey = "five_hour"
	SevenDayKey = "seven_day"
)

// Window is one quota window as the API reports it.
type Window struct {
	Utilization  *float64   `json:"utilization"`
	ResetsAt     *time.Time `json:"resets_at"`
	LockedReason *string    `json:"locked_reason"`
}

func (w Window) Known() bool { return w.Utilization != nil }

func (w Window) Pct() float64 {
	if w.Utilization == nil {
		return 0
	}
	return *w.Utilization
}

// Limit is an entry of the limits[] array. severity is worth logging from day
// one: if a non-"normal" value reliably precedes a rejection it beats a fixed
// percentage as a trigger, but nothing but "normal" has been observed yet.
//
// The entry is kept in the API's own shape, scope included, because state.json
// stores the Usage as encoded here and the macOS app reads the scope from it.
// Percent is a pointer: a percent the API sent as null, or did not send, is
// unknown, and decoding it into a float64 made it 0 — the most reassuring
// figure there is (DESIGN 4.4).
type Limit struct {
	Kind     string      `json:"kind"`
	Group    string      `json:"group"`
	Percent  *float64    `json:"percent"`
	Severity string      `json:"severity"`
	IsActive bool        `json:"is_active"`
	ResetsAt *time.Time  `json:"resets_at"`
	Scope    *LimitScope `json:"scope"`
}

// LimitScope narrows a limit to part of the account. Only a model has been
// observed (GROUND_TRUTH §42); surface is kept as sent, so that a scope this
// code does not understand stays visible as such.
type LimitScope struct {
	Model   *LimitModel     `json:"model"`
	Surface json.RawMessage `json:"surface,omitempty"`
}

// LimitModel names the model a scoped limit applies to.
type LimitModel struct {
	ID          *string `json:"id"`
	DisplayName *string `json:"display_name"`
}

// Kinds of limits[] entry this code understands.
const (
	KindSession      = "session"
	KindWeeklyAll    = "weekly_all"
	KindWeeklyScoped = "weekly_scoped"
)

// LimitClass is what a limits[] entry is, as far as this code understands it.
type LimitClass int

const (
	// LimitUnknown is a kind or a scope this code does not recognise. It is
	// shown as unknown and never counted as room.
	LimitUnknown LimitClass = iota
	// LimitSession is the five-hour window (five_hour).
	LimitSession
	// LimitWeekly is the account's whole weekly window (seven_day).
	LimitWeekly
	// LimitModelWeekly is a weekly limit on one model (IMPROVEMENTS I6).
	LimitModelWeekly
)

func (c LimitClass) String() string {
	switch c {
	case LimitSession:
		return "session"
	case LimitWeekly:
		return "weekly"
	case LimitModelWeekly:
		return "model weekly"
	}
	return "unknown"
}

// Known reports whether the API gave this limit a figure.
func (l Limit) Known() bool { return l.Percent != nil }

// Pct is the figure, or 0 when unknown: check Known first.
func (l Limit) Pct() float64 {
	if l.Percent == nil {
		return 0
	}
	return *l.Percent
}

// ModelName is the model a scoped limit applies to: its display name, else
// its id, else "".
func (l Limit) ModelName() string {
	if l.Scope == nil || l.Scope.Model == nil {
		return ""
	}
	if n := l.Scope.Model.DisplayName; n != nil && strings.TrimSpace(*n) != "" {
		return strings.TrimSpace(*n)
	}
	if id := l.Scope.Model.ID; id != nil {
		return strings.TrimSpace(*id)
	}
	return ""
}

// scoped reports whether the entry carries any scope at all.
func (l Limit) scoped() bool {
	return l.Scope != nil && (l.Scope.Model != nil || !rawEmpty(l.Scope.Surface))
}

func rawEmpty(r json.RawMessage) bool {
	s := strings.TrimSpace(string(r))
	return s == "" || s == "null"
}

// Class says what the entry is. A known kind with a scope it should not have,
// or a scope with more in it than a model, is unknown: counting it as the
// account's window would be guessing.
func (l Limit) Class() LimitClass {
	switch l.Kind {
	case KindSession:
		if !l.scoped() {
			return LimitSession
		}
	case KindWeeklyAll:
		if !l.scoped() {
			return LimitWeekly
		}
	case KindWeeklyScoped:
		if l.Scope != nil && l.ModelName() != "" && rawEmpty(l.Scope.Surface) {
			return LimitModelWeekly
		}
	}
	return LimitUnknown
}

// Describe names an entry for a person, for one this code does not
// understand: its kind, and what its scope says.
func (l Limit) Describe() string {
	s := l.Kind
	if s == "" {
		s = "(no kind)"
	}
	var scope []string
	if l.Scope != nil {
		if m := l.ModelName(); m != "" {
			scope = append(scope, "model "+m)
		} else if l.Scope.Model != nil {
			scope = append(scope, "an unnamed model")
		}
		if !rawEmpty(l.Scope.Surface) {
			scope = append(scope, "surface "+strings.TrimSpace(string(l.Scope.Surface)))
		}
	}
	if len(scope) > 0 {
		s += " (" + strings.Join(scope, ", ") + ")"
	}
	return s
}

// Money is an amount as the API reports it: minor units plus an exponent, so
// 1234 with exponent 2 is 12.34.
type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Exponent    int    `json:"exponent"`
}

func (m Money) Float() float64 {
	d := 1.0
	for i := 0; i < m.Exponent; i++ {
		d *= 10
	}
	return float64(m.AmountMinor) / d
}

func (m Money) String() string {
	sym := map[string]string{"USD": "$", "EUR": "€", "GBP": "£"}[m.Currency]
	if sym == "" {
		sym = m.Currency + " "
	}
	return fmt.Sprintf("%s%.2f", sym, m.Float())
}

// Spend is usage-based billing on this account, when it is enabled.
type Spend struct {
	Used     Money   `json:"used"`
	Limit    *Money  `json:"limit"`
	Percent  float64 `json:"percent"`
	Severity string  `json:"severity"`
	Enabled  bool    `json:"enabled"`
}

// ExtraUsage is paid overflow: continuing past the plan's limits, for money.
// Whether it is on decides something the policy engine cannot otherwise know —
// that an exhausted account will keep working, and start charging.
type ExtraUsage struct {
	Enabled      bool     `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit"`
	UsedCredits  *float64 `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
	Currency     string   `json:"currency"`
}

// Usage is one reading for one account.
type Usage struct {
	FiveHour   Window     `json:"five_hour"`
	SevenDay   Window     `json:"seven_day"`
	Limits     []Limit    `json:"limits"`
	Spend      Spend      `json:"spend"`
	ExtraUsage ExtraUsage `json:"extra_usage"`

	// Set by the client, not the wire.
	OrgID     string    `json:"-"`
	FetchedAt time.Time `json:"-"`
}

// Worst returns the higher of the two windows' utilization: the one that will
// bite first.
func (u *Usage) Worst() (string, float64) {
	f, s := u.FiveHour.Pct(), u.SevenDay.Pct()
	if !u.FiveHour.Known() && !u.SevenDay.Known() {
		return "", 0
	}
	if s > f {
		return SevenDayKey, s
	}
	return FiveHourKey, f
}

// WorstAgainst reports which window is in the most trouble relative to its own
// trigger, that window's utilization, and how far past the trigger it is.
//
// Worst() compares the two windows to each other, which is only meaningful when
// both are judged by the same number. Once the 5-hour and weekly windows have
// separate triggers, "85% of one" and "85% of the other" stop being comparable
// quantities: the first is nearly spent, the second still holds days. What
// matters is distance from the line each window is actually being held to.
func (u *Usage) WorstAgainst(fiveTrigger, sevenTrigger float64) (key string, pct, exceedance float64) {
	f, s := u.FiveHour.Pct(), u.SevenDay.Pct()
	if !u.FiveHour.Known() && !u.SevenDay.Known() {
		return "", 0, 0
	}
	fe, se := f-fiveTrigger, s-sevenTrigger
	switch {
	case !u.SevenDay.Known():
		return FiveHourKey, f, fe
	case !u.FiveHour.Known():
		return SevenDayKey, s, se
	case se > fe:
		return SevenDayKey, s, se
	}
	return FiveHourKey, f, fe
}

// Billing renders what this account is costing, or empty when nothing is
// billed. Percentages say when you will be stopped; this says what you are
// paying, which for anyone on usage-based billing is the number that matters.
func (u *Usage) Billing() string {
	switch {
	case u.ExtraUsage.Enabled && u.ExtraUsage.UsedCredits != nil:
		s := fmt.Sprintf("overflow %.2f", *u.ExtraUsage.UsedCredits)
		if u.ExtraUsage.MonthlyLimit != nil {
			s += fmt.Sprintf("/%.0f", *u.ExtraUsage.MonthlyLimit)
		}
		return s
	case u.Spend.Enabled:
		s := u.Spend.Used.String()
		if u.Spend.Limit != nil {
			s += " / " + u.Spend.Limit.String()
		}
		return s
	}
	return ""
}

// WillBill reports whether continuing on this account costs money rather than
// stopping — paid overflow turns a wall into a bill, which is a materially
// different thing to rotate into.
func (u *Usage) WillBill() bool { return u.ExtraUsage.Enabled }

// Binding returns the limits[] entry the API currently marks active.
func (u *Usage) Binding() *Limit {
	for i := range u.Limits {
		if u.Limits[i].IsActive {
			return &u.Limits[i]
		}
	}
	return nil
}

// ModelWeekly is every per-model weekly limit, in the API's order.
func (u *Usage) ModelWeekly() []Limit {
	var out []Limit
	for _, l := range u.Limits {
		if l.Class() == LimitModelWeekly {
			out = append(out, l)
		}
	}
	return out
}

// UnknownLimits is every entry this code does not understand.
func (u *Usage) UnknownLimits() []Limit {
	var out []Limit
	for _, l := range u.Limits {
		if l.Class() == LimitUnknown {
			out = append(out, l)
		}
	}
	return out
}

// ModelLimit finds the weekly limit for a model, matched case-insensitively.
func (u *Usage) ModelLimit(name string) (Limit, bool) {
	for _, l := range u.ModelWeekly() {
		if strings.EqualFold(l.ModelName(), strings.TrimSpace(name)) {
			return l, true
		}
	}
	return Limit{}, false
}

// WithModels is the reading as the policy judges it when the config counts
// these models' weekly limits like the weekly window (IMPROVEMENTS I6).
//
// The weekly window becomes the highest of seven_day and each counted model's
// limit, with that limit's reset; from names the model when one of them is
// the higher. A model the account has no limit for adds nothing: the weekly
// window governs it. A counted model whose limit has no figure makes the
// account's weekly standing unknown, and unreadable names it; the caller must
// treat the account as unknown (DESIGN 4.4).
//
// With no models, or none raising the weekly figure, it returns u itself.
// Otherwise a copy; u is never modified.
func (u *Usage) WithModels(models []string) (eff *Usage, from, unreadable string) {
	if u == nil || len(models) == 0 {
		return u, "", ""
	}
	best := u.SevenDay
	for _, m := range models {
		l, ok := u.ModelLimit(m)
		if !ok {
			continue
		}
		if !l.Known() {
			if unreadable == "" {
				unreadable = l.ModelName()
			}
			continue
		}
		if !best.Known() || l.Pct() > best.Pct() {
			p := l.Pct()
			best = Window{Utilization: &p, ResetsAt: l.ResetsAt}
			from = l.ModelName()
		}
	}
	if from == "" {
		return u, "", unreadable
	}
	cp := *u
	cp.SevenDay = best
	return &cp, from, unreadable
}

// RateLimitedError is returned on a 429. RetryAfter is authoritative: the API
// does send retry-after on the 429 even though it sends nothing on a 200.
type RateLimitedError struct {
	RetryAfter time.Duration
	// Local marks a pause this program imposed on itself rather than one the
	// API asked for. The distinction matters to whoever reads the log: a local
	// pause means the fix is in our own budget, and saying "usage API rate
	// limited" for it sends them to look at the wrong thing entirely.
	Local bool
}

func (e *RateLimitedError) Error() string {
	who := "usage API rate limited"
	if e.Local {
		who = "paused by our own call budget"
	}
	// A lock that has already expired reads as "retry in 0s", which looks like
	// a broken number rather than "try again now".
	if e.RetryAfter <= 0 {
		return who + ", retry now"
	}
	return fmt.Sprintf("%s, retry in %s", who, e.RetryAfter.Round(time.Second))
}

// ShapeError means the response parsed as JSON but is not the shape we depend
// on. This is the signal to disable predictive switching and fall back to
// reactive-only, loudly. It must never be treated as "no headroom info, assume
// fine".
type ShapeError struct{ Detail string }

func (e *ShapeError) Error() string { return "usage API returned an unexpected shape: " + e.Detail }

func IsRateLimited(err error) (*RateLimitedError, bool) {
	var r *RateLimitedError
	ok := errors.As(err, &r)
	return r, ok
}

func IsShapeError(err error) bool {
	var s *ShapeError
	return errors.As(err, &s)
}

// TokenRejectedError is a 401 or 403: the token sent was not accepted. That
// may be the account needing a login, or only a stale copy of a token that
// has since been refreshed elsewhere — the caller can tell, this cannot.
type TokenRejectedError struct{ Status int }

func (e *TokenRejectedError) Error() string {
	return fmt.Sprintf("usage API rejected the token (%d): the account may need a re-login", e.Status)
}

func IsTokenRejected(err error) bool {
	var t *TokenRejectedError
	return errors.As(err, &t)
}

type Client struct {
	HTTP *http.Client
	// Now stamps each reading's FetchedAt; nil is the wall clock. A seam for
	// simulations on a fake clock.
	Now func() time.Time
}

// newTransport configures connection handling for a process that lives for days.
//
// The default transport keeps idle connections indefinitely, and a connection
// that has gone stale — after a laptop sleeps, a network changes, or a NAT table
// forgets it — is handed out anyway and fails at the TLS handshake. The daemon
// spent tonight reporting "TLS handshake timeout" while curl answered the same
// endpoint in 200ms, which is that bug exactly.
//
// Short idle timeouts cost a handshake now and then. That is far cheaper than a
// daemon that cannot see.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second, Transport: newTransport()}}
}

// retryable reports whether an error is worth one more attempt on a fresh
// connection: transport-level failures, never a response the server actually
// sent. A 429 or a 401 means something and must not be retried away.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	s := err.Error()
	for _, frag := range []string{
		"TLS handshake timeout", "connection reset", "broken pipe",
		"EOF", "unexpected EOF", "server closed idle connection",
		"no such host", "connection refused",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

// do sends a request, retrying once on a transport failure with the idle pool
// cleared. A stale pooled connection fails instantly and the retry succeeds;
// without this the daemon simply goes blind until someone restarts it.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err == nil || !retryable(err) || req.Context().Err() != nil {
		return resp, err
	}
	if tr, ok := c.HTTP.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	// The body is nil on these requests, so the request is reusable as-is.
	return c.HTTP.Do(req.Clone(req.Context()))
}

// Fetch reads usage for whichever account owns accessToken.
func (c *Client) Fetch(ctx context.Context, accessToken string) (*Usage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("calling usage API: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading usage response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		d := 300 * time.Second
		if v := resp.Header.Get("Retry-After"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				d = time.Duration(n) * time.Second
			}
		}
		return nil, &RateLimitedError{RetryAfter: d}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, &TokenRejectedError{Status: resp.StatusCode}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	var u Usage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, &ShapeError{Detail: err.Error()}
	}
	if !u.FiveHour.Known() && !u.SevenDay.Known() {
		return nil, &ShapeError{Detail: "neither five_hour nor seven_day carried a utilization"}
	}
	u.OrgID = resp.Header.Get("anthropic-organization-id")
	u.FetchedAt = time.Now()
	if c.Now != nil {
		u.FetchedAt = c.Now()
	}
	return &u, nil
}

// Profile is the identity behind a credential.
//
// The usage API returns only an organization id, and an organization is NOT a
// quota pool: a team organization has one seat per member, each with its own
// limits. Two credentials for the same organization can report entirely
// different utilization (observed 2026-09-10: 23%/19% and 3%/0%). So identity —
// for attribution, for duplicate detection, for deciding whether a changed token
// is "the same account refreshed" — is the ACCOUNT uuid, never the organization.
type Profile struct {
	Account struct {
		UUID   string `json:"uuid"`
		Email  string `json:"email"`
		Name   string `json:"display_name"`
		HasMax bool   `json:"has_claude_max"`
		HasPro bool   `json:"has_claude_pro"`
	} `json:"account"`
	Organization struct {
		UUID          string `json:"uuid"`
		Name          string `json:"name"`
		Type          string `json:"organization_type"`
		RateLimitTier string `json:"rate_limit_tier"`
		SeatTier      string `json:"seat_tier"`
		ExtraUsage    bool   `json:"has_extra_usage_enabled"`
		SubStatus     string `json:"subscription_status"`
	} `json:"organization"`
}

// Plan renders the subscription behind a seat in a form a person recognises.
// The wire values are internal ("default_claude_max_20x", "claude_team"), and
// what matters day to day is the multiplier: a 5x seat runs out four times
// sooner than a 20x one, which is exactly the sort of thing you want visible
// when choosing a rotation order.
func (p *Profile) Plan() string {
	tier := p.Organization.RateLimitTier
	name := ""
	switch {
	case strings.Contains(tier, "max_20x"):
		name = "Max 20x"
	case strings.Contains(tier, "max_5x"):
		name = "Max 5x"
	case strings.Contains(tier, "pro"):
		name = "Pro"
	case tier == "":
		name = "unknown"
	default:
		// Something new: show it rather than hide it behind "unknown".
		name = strings.TrimPrefix(tier, "default_claude_")
	}
	if p.Organization.Type == "claude_team" {
		name += " team"
	}
	if p.Organization.ExtraUsage {
		name += " +overflow"
	}
	if st := p.Organization.SubStatus; st != "" && st != "active" {
		name += " (" + st + ")"
	}
	return name
}

// Seat is the quota pool a profile belongs to: this person, in this
// organization. The same person in a different organization is a different pool.
func (p *Profile) Seat() string {
	if p.Account.UUID == "" || p.Organization.UUID == "" {
		return ""
	}
	return p.Account.UUID + "@" + p.Organization.UUID
}

// ShortSeat abbreviates person@organization without losing either half, and is
// the only correct way to shorten a seat for display.
//
// It lives here, in the package that defines what a seat is, because the
// alternative was tried: the same six lines were written locally the first time
// this bug was fixed, and three more copies of the bug survived in other
// packages — including the guard that refuses to refresh a mis-filed credential,
// whose entire job is to name two seats that differ. A seat put through a plain
// eight-character truncation comes out as the account uuid alone, so a message
// contrasting two of them names the half that matches and discards the half
// that differs.
func ShortSeat(seat string) string {
	person, org, ok := strings.Cut(seat, "@")
	if !ok {
		return shortID(seat)
	}
	return shortID(person) + "@" + shortID(org)
}

// shortID is the first eight characters, which is enough to recognise a uuid
// and short enough to sit in a table.
func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Describe renders the seat for a human: the email plus the organization, since
// the same address can own several pools.
func (p *Profile) Describe() string {
	return p.Account.Email + " in " + p.Organization.Name
}

// ProfileEndpoint identifies whoever owns a token.
const ProfileEndpoint = "https://api.anthropic.com/api/oauth/profile"

// FetchProfile reads the identity behind an access token.
func (c *Client) FetchProfile(ctx context.Context, accessToken string) (*Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ProfileEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("identity lookup: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &RateLimitedError{RetryAfter: 300 * time.Second}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity lookup returned %d", resp.StatusCode)
	}
	var pr Profile
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, &ShapeError{Detail: err.Error()}
	}
	if pr.Account.UUID == "" {
		return nil, &ShapeError{Detail: "identity lookup carried no account uuid"}
	}
	return &pr, nil
}
