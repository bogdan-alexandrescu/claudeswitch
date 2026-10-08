package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Budget is the daemon-wide call allowance for the usage endpoint.
//
// Measured (ground truth #12): roughly 5 calls buys a 299-second lockout. There
// are no rate-limit headers on success, so we cannot discover the budget safely
// at runtime — we hold well under it by construction.
//
// The allowance is deliberately 4, not 5: one call is always held back for the
// pre-switch check, which is the call that actually matters. A poller that locks
// itself out runs blind for five minutes; that is a bug, not a hiccup.
// The limit is a BURST allowance that refills, not a sustained cap.
//
// Measured 2026-09-08: five calls in quick succession, then a 429 with
// retry-after 299. I read that as "5 per 5 minutes sustained" and built the
// budget around it — wrongly. Measured again 2026-09-10, one call every 20
// seconds: eleven consecutive 200s, with calls 6 through 11 all inside a single
// five-minute span. A fixed window would have refused call 6.
//
// The intervening 429s that seemed to confirm the low figure had a different
// cause: the budget was per-process, so the daemon and every CLI invocation each
// believed they held a full allowance and the real spend was several times what
// any of them thought.
//
// So the window here is a burst guard, not a rate cap: it stops a tight loop
// from tripping the lockout, while allowing a cadence far quicker than before.
// Sustained ~15 calls per 5 minutes was demonstrably fine; 12 keeps margin.
const (
	DefaultAllowance = 12
	DefaultWindow    = 5 * time.Minute
	ReservedForSwap  = 1
)

// The per-ACCOUNT allowance (GROUND_TRUTH §42, docs/DESIGN.md 4.3c).
//
// The endpoint's limit belongs to each account: at one call every 20 seconds
// an idle seat answered 24 calls and refused the 25th, while another account
// on the same machine was answered normally, and the refused one recovered
// between 10 and 15 minutes later. The window above is one machine-wide burst
// guard; this is the limit itself, modelled as a token bucket per credential.
//
// The numbers are the most generous pair the measurement allows, rounded
// down. A bucket of AccountBurst refilling one call per AccountRefill runs dry
// at its 24th call at 20-second spacing — the measured edge — so any larger
// bucket with this refill, or any faster refill with this bucket, predicts
// calls the endpoint refused. The refill also matches claude-swap's
// independent figure of ~30 an hour per identity. The endpoint's own bucket
// is a little larger (~24), and that difference is the room left for Claude
// Code's own calls on the live account, which this program cannot see (§40).
//
// What follows: polling an idle account every AccountRefill (2 minutes) or slower
// never empties it, all day; a hot spell at one call a minute spends half a
// call a minute net, so a 15-minute spell needs 7.5 of the hot reserve that
// routine polling leaves for it; 20-second polling
// runs out in about 8, as observed.
const (
	AccountBurst  = 20
	AccountRefill = 2 * time.Minute
	// AccountReserve is held back from scheduled polling for the checks a
	// rotation depends on: verifying the incoming account, and re-reading the
	// live one after a swap.
	AccountReserve = 3
	// DefaultHotReserve (config hot_reserve) is held back from routine polling for hot polling: a hot
	// spell at poll_hot 60s over the 15-minute lookahead spends 7.5 calls
	// beyond the refill (DESIGN 4.3c). Without it, routine polling of the
	// account in use at the refill rate (poll_active 2m) wears the allowance
	// down to the swap reserve over a working day — Claude Code's own reads
	// spend it too — and the hot spell, the one time a fresh reading
	// matters, is the one that gets deferred. Routine polls stop at
	// AccountReserve+hot reserve and wait for the refill instead.
	DefaultHotReserve = 10
	// OverdueFloor is how far above the swap reserve an Overdue read may
	// go: the account in use is read, below the hot reserve, once its
	// reading is overdue — after a hot spell spent the reserve, routine
	// polls would otherwise wait about 20 minutes for it to refill.
	//
	// One, not three: a 14-minute spell at the default hot reserve ends
	// with about 5 calls left (swap reserve + 2), so with a floor of 3 the
	// first overdue read waited on the refill and the reading reached 3m55s
	// (day simulation), past the daemon's 3m stale-decision cap. With 1 it
	// stays at 2m30s. The swap reserve itself is untouched.
	OverdueFloor = 1
)

// Priority says what a call is worth, and therefore what it is allowed to
// spend. The two windows of protection here — the per-window allowance and the
// lockout armed after a 429 — are both our own bookkeeping, and there is one
// kind of call they must never refuse.
type Priority int

const (
	// Scheduled is the daemon polling on its own cadence. It leaves the
	// reserve alone, so a rotation can always be checked.
	Scheduled Priority = iota
	// Swap is a call a rotation depends on. It may spend the reserve.
	Swap
	// Interactive is a call a person is waiting on, having just done something
	// that cannot be cheaply repeated — an interactive login, in the one case
	// that exists today. It is never refused by our own bookkeeping.
	//
	// The lockout is armed by a 429 against whichever token happened to be
	// polling, but the rate limit it reflects belongs to that account, not to
	// this program. Applied to a credential that has made no calls at all it is
	// not a safety measure, it is a guess carried over from someone else — and
	// it produced a deadlock: accounts run out of quota, the 429s arm a
	// twenty-minute lockout, and `add`, the one command that fixes the
	// shortage, is refused for the whole of it. The login it threw away was the
	// expensive part (observed 2026-09-16).
	Interactive
	// Hot is the poller reading the account in use while it moves toward
	// its trigger (DESIGN 4.3c). Like Scheduled it leaves the swap reserve
	// alone, but it may spend the hot reserve.
	Hot
	// Overdue is a routine read of the account in use whose reading is
	// already older than the cadence allows. It may spend the hot reserve
	// down to AccountReserve+OverdueFloor.
	Overdue
)

type Budget struct {
	mu        sync.Mutex
	allowance int
	window    time.Duration
	calls     []time.Time
	locks     map[string]accountLock // set when the API tells us to back off
	buckets   map[string]bucket      // each account's own allowance
	live      map[string]time.Time   // credentials seen live (MarkLive)
	// The model's advanced numbers (SetModel).
	hotReserve int
	unseen     float64
	now        func() time.Time
	// sleep waits out Pace's gap; a seam so a simulated clock can advance.
	sleep func(context.Context, time.Duration)
	// ledger is the file backing this budget across processes. Empty keeps it
	// process-local, which is what the tests want.
	ledger string
	// lastCall is when this process last spent a call, for pacing.
	lastCall time.Time
}

func NewBudget() *Budget {
	return &Budget{allowance: DefaultAllowance, window: DefaultWindow, now: time.Now, sleep: realSleep,
		hotReserve: DefaultHotReserve, unseen: DefaultLiveUnseenPerHour}
}

// NewBudgetWithClock is a process-local budget on a supplied clock, for
// simulations: sleep advances that clock instead of waiting.
func NewBudgetWithClock(now func() time.Time, sleep func(time.Duration)) *Budget {
	b := NewBudget()
	b.now = now
	b.sleep = func(_ context.Context, d time.Duration) { sleep(d) }
	return b
}

func realSleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// SetAllowance sets how many calls per window are permitted, so the figure can
// come from config rather than being fixed at build time.
func (b *Budget) SetAllowance(n int) {
	if n < 2 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allowance = n
}

// shared is the budget every caller in this process uses. It is backed by a file
// so that it is shared across PROCESSES too.
//
// A process-local budget is not a budget: the limit belongs to the account, not
// to whoever happens to be calling. With one per process, the daemon believed it
// had three calls while every `cs status` believed the same, the real allowance
// was spent several times over, and the daemon's polls began timing out — after
// which it made decisions on stale readings and said nothing, because "stay" is
// logged at debug level (observed 2026-09-10, at 93% with no rotation).
var (
	sharedOnce sync.Once
	sharedB    *Budget
)

// Shared returns the single budget every caller of the usage API must use.
func Shared() *Budget {
	sharedOnce.Do(func() {
		sharedB = NewBudget()
		sharedB.ledger = defaultLedgerPath()
	})
	return sharedB
}

func defaultLedgerPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "claudeswitch", "api-calls.json")
}

// ledgerFile is the cross-process record of recent calls.
//
// Calls is one window for every account: it is the burst guard, and it stays
// machine-wide because nothing has shown the burst limit to be anything else.
// Locks are per credential. A 429 is the API refusing one account, and the
// account most often refused is the live one, whose allowance Claude Code
// spends too (it calls the same endpoint with the same token). A single lock
// for everything turned that into a pause on reading every other account —
// exactly the ones a rotation needs current figures for (observed 2026-09-16:
// every refusal of the day landed on whichever account was live).
type ledgerFile struct {
	Calls []time.Time `json:"calls"`
	// Locks is keyed by lockKey, a digest of the credential, so the file never
	// holds a token.
	Locks map[string]accountLock `json:"locks,omitempty"`
	// Accounts is each credential's own allowance, keyed the same way. An
	// account with no entry has a full one.
	Accounts map[string]bucket `json:"accounts,omitempty"`
	// Live is when each credential was last seen as some profile's live
	// one (MarkLive), keyed the same way. A 429 on a credential seen live
	// within liveFor gets the live backoff cap from any caller.
	Live map[string]time.Time `json:"live,omitempty"`

	// unseen is the caller's unseen_calls_per_hour, for refillPerHour.
	unseen float64
}

// liveFor is how long a MarkLive holds. The daemon re-reads every profile's
// live item every 15 minutes, so a live token is re-marked well within it.
const liveFor = time.Hour

// MarkLive records that cred is some profile's live credential, so a 429 on
// it — reported by the vault during a swap or a probe as much as by the
// poller — is capped at MaxLiveBackoff rather than MaxBackoff.
func (b *Budget) MarkLive(cred string) {
	key := lockKey(cred)
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.withLedger(func(lf *ledgerFile) {
		if lf.Live == nil {
			lf.Live = map[string]time.Time{}
		}
		lf.Live[key] = now
	})
}

// bucket is one account's allowance as of At. Tokens below zero is a debt,
// run up by interactive calls, which are charged but never refused.
type bucket struct {
	Tokens float64   `json:"tokens"`
	At     time.Time `json:"at"`
}

// level is the allowance at now, refilled since At at perHour calls an hour
// and capped at the burst.
func (k bucket) level(now time.Time, perHour float64) float64 {
	t := k.Tokens + now.Sub(k.At).Hours()*perHour
	if t > AccountBurst {
		t = AccountBurst
	}
	return t
}

// DefaultLiveUnseenPerHour (config unseen_calls_per_hour) is what the model sets aside, on a credential some
// profile is running on, for Claude Code's own reads of the same endpoint
// with the same token (GROUND_TRUTH §40: established from its code, rate not
// measured). They spend the account's allowance where this program cannot
// see them, so a live account is modelled as refilling this much slower.
// Without it, polling the account in use at the refill rate (poll_active 2m)
// slowly overdraws the real allowance while the model thinks it full.
const DefaultLiveUnseenPerHour = 2.0

// refillPerHour is what key regains an hour, less the unseen spend when it
// is live.
func (lf *ledgerFile) refillPerHour(key string, now time.Time) float64 {
	r := float64(time.Hour) / float64(AccountRefill)
	if at, ok := lf.Live[key]; ok && now.Sub(at) <= liveFor {
		r -= lf.unseen
	}
	return r
}

func (lf *ledgerFile) account(key string, now time.Time) float64 {
	k, ok := lf.Accounts[key]
	if !ok {
		return AccountBurst
	}
	return k.level(now, lf.refillPerHour(key, now))
}

func (lf *ledgerFile) charge(key string, now time.Time) {
	if key == "" {
		return
	}
	if lf.Accounts == nil {
		lf.Accounts = map[string]bucket{}
	}
	lf.Accounts[key] = bucket{Tokens: lf.account(key, now) - 1, At: now}
}

// need is how much allowance a call of priority p requires: a scheduled poll
// leaves the reserve alone; a swap check may spend it down to the last call.
func (b *Budget) need(p Priority) float64 {
	switch p {
	case Scheduled:
		return float64(1 + AccountReserve + b.hotReserve)
	case Overdue:
		return float64(1 + AccountReserve + min(OverdueFloor, b.hotReserve))
	case Hot:
		return 1 + AccountReserve
	}
	return 1
}

// SetModel sets the per-account model's two advanced numbers from config
// (hot_reserve, unseen_calls_per_hour). An out-of-range value is not applied
// and is reported, so the caller can log it; config validates both, so one
// reaching here is a bug, not a setting.
func (b *Budget) SetModel(hotReserve int, unseenPerHour float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var errs []error
	if hotReserve >= 0 && hotReserve <= AccountBurst-AccountReserve-1 {
		b.hotReserve = hotReserve
	} else {
		errs = append(errs, fmt.Errorf("hot reserve %d is outside 0..%d; keeping %d",
			hotReserve, AccountBurst-AccountReserve-1, b.hotReserve))
	}
	if unseenPerHour >= 0 && unseenPerHour < float64(time.Hour/AccountRefill) {
		b.unseen = unseenPerHour
	} else {
		errs = append(errs, fmt.Errorf("unseen calls per hour %g is outside 0..%d; keeping %g",
			unseenPerHour, int(time.Hour/AccountRefill), b.unseen))
	}
	return errors.Join(errs...)
}

type accountLock struct {
	Until time.Time `json:"until,omitzero"`
	// Strikes counts consecutive refusals, so the backoff can grow rather than
	// retrying at a fixed interval forever.
	Strikes int `json:"strikes,omitempty"`
}

// lockKey names a credential in the ledger without storing it. Keying on the
// token rather than an account id is what makes the live credential one entry
// however it was reached — polled as its vault entry, or as the live item
// before attribution. A refreshed token starts a fresh entry, which at worst
// costs one extra call.
func lockKey(cred string) string {
	if cred == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(cred))
	return hex.EncodeToString(sum[:8])
}

// forgetLocksAfter is how long an expired lock's strike count is kept once its
// lock has run out with no success recorded. Past it the entry most likely
// belongs to a token that has since been refreshed, and nothing will ever
// clear it.
const forgetLocksAfter = time.Hour

func (lf *ledgerFile) gc(now time.Time) {
	for k, l := range lf.Locks {
		if now.Sub(l.Until) > forgetLocksAfter {
			delete(lf.Locks, k)
		}
	}
	for k, at := range lf.Live {
		if now.Sub(at) > liveFor {
			delete(lf.Live, k)
		}
	}
	// A full allowance is the same as no entry, and an entry for a token that
	// was refreshed away would otherwise stay forever.
	for k := range lf.Accounts {
		if lf.account(k, now) >= AccountBurst {
			delete(lf.Accounts, k)
		}
	}
}

// withLedger runs fn against the shared on-disk record, under an exclusive lock,
// and writes back whatever fn leaves behind. Every call that touches the budget
// goes through here, so two processes cannot both believe they have the last
// slot.
func (b *Budget) withLedger(fn func(*ledgerFile)) {
	// local applies fn to this process's own state, used when there is no file
	// to share and whenever the file cannot be reached. Falling back to a
	// process-local budget is the right failure: it is more conservative than
	// no budget at all.
	local := func() {
		lf := &ledgerFile{Calls: b.calls, Locks: b.locks, Accounts: b.buckets, Live: b.live, unseen: b.unseen}
		fn(lf)
		lf.gc(b.now())
		b.calls, b.locks, b.buckets, b.live = lf.Calls, lf.Locks, lf.Accounts, lf.Live
	}
	if b.ledger == "" {
		local()
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.ledger), 0o700); err != nil {
		local()
		return
	}
	f, err := os.OpenFile(b.ledger, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		local()
		return
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		local()
		return
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	lf := ledgerFile{unseen: b.unseen}
	if raw, err := io.ReadAll(f); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &lf)
	}
	fn(&lf)
	lf.gc(b.now())

	if raw, err := json.Marshal(lf); err == nil {
		_ = f.Truncate(0)
		_, _ = f.Seek(0, 0)
		_, _ = f.Write(raw)
	}
	// Keep the in-memory copy in step, so Remaining() is close without a lock.
	b.calls, b.locks, b.buckets, b.live = lf.Calls, lf.Locks, lf.Accounts, lf.Live
}

func (b *Budget) prune(now time.Time) {
	cut := now.Add(-b.window)
	keep := b.calls[:0]
	for _, t := range b.calls {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	b.calls = keep
}

// Reason describes why a call was refused, for the audit log and for `status`.
type Reason string

const (
	ReasonOK       Reason = ""
	ReasonLockout  Reason = "backing off after a 429"
	ReasonBudget   Reason = "call budget spent for this window"
	ReasonReserved Reason = "remaining call is reserved for a swap check"
	// ReasonAccount is this account's own allowance (§42) being down to its
	// reserve, or empty. Only this account waits; the others are unaffected.
	ReasonAccount Reason = "this account's usage allowance is low; deferred"
)

// MinSpacing is the shortest gap between two calls this program will make.
//
// The endpoint's limit is a burst allowance: twelve calls at one every twenty
// seconds passed without complaint, while five in quick succession tripped it.
// Spacing is therefore the thing that matters, and enforcing it here rather than
// at each call site means a new caller cannot forget — `cs identify` sweeping
// four accounts did exactly that.
const MinSpacing = 2500 * time.Millisecond

// Pace blocks until the shortest safe gap since the previous call has passed.
// Callers making several calls in a row should use it; a single call costs
// nothing, since the gap will already have elapsed.
func (b *Budget) Pace(ctx context.Context) {
	b.mu.Lock()
	last, now, sleep := b.lastCall, b.now(), b.sleep
	b.mu.Unlock()
	if last.IsZero() {
		return
	}
	if sleep == nil {
		sleep = realSleep
	}
	if wait := MinSpacing - now.Sub(last); wait > 0 {
		sleep(ctx, wait)
	}
}

// Allow reserves a call with credential cred if one is available. priority
// marks a call that may spend the reserved slot — used for the pre-switch
// verification, never for scheduled polling. A lock refuses only calls with the
// credential it was armed against.
func (b *Budget) Allow(cred string, p Priority) (bool, Reason) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	ok, reason := false, ReasonOK
	b.withLedger(func(lf *ledgerFile) {
		// Pruning first, for every caller. An earlier version of the
		// interactive branch below returned before reaching this, so an
		// interactive call appended without ever dropping expired entries and
		// the ledger grew without bound — only a scheduled call ever trimmed
		// it. Harmless to the arithmetic, since everything that counts filters
		// by the window anyway, but it is a file on disk.
		cut := now.Add(-b.window)
		keep := lf.Calls[:0]
		for _, t := range lf.Calls {
			if t.After(cut) {
				keep = append(keep, t)
			}
		}
		lf.Calls = keep

		key := lockKey(cred)
		// Recorded, so it still counts against the window and the account's
		// allowance and still paces — but never refused. See Interactive.
		if p == Interactive {
			lf.Calls = append(lf.Calls, now)
			lf.charge(key, now)
			b.lastCall = now
			ok, reason = true, ReasonOK
			return
		}
		if now.Before(lf.Locks[key].Until) {
			ok, reason = false, ReasonLockout
			return
		}
		if key != "" && lf.account(key, now) < b.need(p) {
			ok, reason = false, ReasonAccount
			return
		}

		limit := b.allowance
		routine := p == Scheduled || p == Hot || p == Overdue
		if routine {
			limit -= ReservedForSwap
		}
		if len(lf.Calls) >= limit {
			switch {
			case !routine, len(lf.Calls) >= b.allowance:
				ok, reason = false, ReasonBudget
			default:
				ok, reason = false, ReasonReserved
			}
			return
		}
		lf.Calls = append(lf.Calls, now)
		lf.charge(key, now)
		b.lastCall = now
		ok, reason = true, ReasonOK
	})
	return ok, reason
}

// AccountReadyAt is when a call of priority p with credential cred will fit
// that account's own allowance: now, if it already does. A lock and the
// machine-wide window have answers of their own and are not considered.
func (b *Budget) AccountReadyAt(cred string, p Priority) time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	var short, perHour float64
	b.withLedger(func(lf *ledgerFile) {
		key := lockKey(cred)
		short = b.need(p) - lf.account(key, now)
		perHour = lf.refillPerHour(key, now)
	})
	if short <= 0 {
		return now
	}
	return now.Add(time.Duration(short / perHour * float64(time.Hour)))
}

// AccountLevel is how many calls an account's allowance holds now.
func (b *Budget) AccountLevel(cred string) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var n float64
	b.withLedger(func(lf *ledgerFile) { n = lf.account(lockKey(cred), b.now()) })
	return n
}

// MinBackoff is the first wait after a 429, whatever Retry-After says.
// A backoff of zero is not a backoff: it was observed producing a warning every
// 80 seconds while the daemon kept hammering an endpoint that had just refused
// it (2026-09-09). Nor is a minute: these refusals carry Retry-After: 0, which
// carries no information, and last 10–15 minutes (GROUND_TRUTH §42), so a
// retry a minute later only collects another refusal. Five minutes, doubled,
// reaches the measured recovery on the second try.
const MinBackoff = 5 * time.Minute

// MaxBackoff caps the wait after repeated refusals. Long enough to stop
// hammering, short enough that recovery is not missed by an hour.
const MaxBackoff = 20 * time.Minute

// MaxLiveBackoff caps it for the account in use. That account is the one a
// rotation decides on, and it is also the one refused most, because Claude Code
// spends its allowance too — five straight refusals left it unread for 25
// minutes at 61% (2026-09-17), long enough to cross the trigger unseen. Idle
// accounts can wait; this one waits no longer than a refusal lasts (5 + 10
// minutes covers the measured 10–15). A refusal in the transcript is still
// acted on at once by the detector, whatever this lock says. A longer
// Retry-After from the server is still honoured.
const MaxLiveBackoff = 10 * time.Minute

// MaxLock bounds how long we will stop calling the API, whatever Retry-After
// says. It exists so the pause can never outlast the watchdog that is supposed
// to notice a daemon which has stopped seeing: if it could, the watchdog would
// kill a healthy daemon mid-wait and the restart would inherit the same lock.
// Anything that needs a longer pause than this needs a person, not a timer.
const MaxLock = 20 * time.Minute

// Penalize records a 429 and backs off, doubling each time the refusals keep
// coming.
//
// A fixed sixty seconds meant a sustained refusal was met with one request a
// minute, for as long as it lasted — 224 of them in one night. Backing off
// further each time is both politer and likelier to recover, and a success
// resets it.
func (b *Budget) Penalize(cred string, retryAfter time.Duration) {
	b.penalize(cred, retryAfter, MaxBackoff)
}

// PenalizeLive is Penalize for the credential currently in use.
func (b *Budget) PenalizeLive(cred string, retryAfter time.Duration) {
	b.penalize(cred, retryAfter, MaxLiveBackoff)
}

func (b *Budget) penalize(cred string, retryAfter, maxBackoff time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := lockKey(cred)
	b.withLedger(func(lf *ledgerFile) {
		now := b.now()
		if lf.Locks == nil {
			lf.Locks = map[string]accountLock{}
		}
		l := lf.Locks[key]
		defer func() { lf.Locks[key] = l }()

		// A refusal that arrives while we are already serving a lock says
		// nothing new — it is the previous refusal echoing, usually because a
		// call slipped past the budget. Re-arming the full penalty for it is
		// how a lock renewed itself indefinitely and never ran down.
		if now.Before(l.Until) {
			return
		}

		// Whoever reports it, a refusal of a live credential gets the live
		// cap: the vault's swap and probe calls lock the token a session is
		// running on just as surely as the poller's.
		if at, ok := lf.Live[key]; ok && now.Sub(at) <= liveFor && maxBackoff > MaxLiveBackoff {
			maxBackoff = MaxLiveBackoff
		}
		l.Strikes++
		wait := retryAfter
		// Double per consecutive refusal, starting at the minimum.
		backoff := MinBackoff << min(l.Strikes-1, 6)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		if wait < backoff {
			wait = backoff
		}
		// Cap what the API asks for, too. Retry-After: 3600 was being honoured
		// literally, which is longer than any watchdog will wait — so the
		// process was killed and restarted for the whole hour, seeing nothing.
		// We come back early and, if the API still refuses, back off again.
		if wait > MaxLock {
			wait = MaxLock
		}
		til := now.Add(wait)
		if til.After(l.Until) {
			l.Until = til
		}
	})
}

// Succeeded clears the consecutive-refusal count. Called after any call that
// the server actually answered, so a single refusal does not leave the backoff
// escalated for the rest of the day.
func (b *Budget) Succeeded(cred string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.withLedger(func(lf *ledgerFile) {
		// Strikes and lock together. A call that succeeded is proof the API is
		// not refusing this account, and holding a pause after that is just
		// refusing ourselves — recovery waited out a penalty that reality had
		// already lifted.
		delete(lf.Locks, lockKey(cred))
	})
}

// CurrentBackoff reports the wait now in force for a credential, for display.
func (b *Budget) CurrentBackoff(cred string) (time.Duration, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var til time.Time
	var strikes int
	b.withLedger(func(lf *ledgerFile) {
		l := lf.Locks[lockKey(cred)]
		til, strikes = l.Until, l.Strikes
	})
	// The injected clock, not wall time: every other method here uses b.now(),
	// and mixing the two makes the backoff unmeasurable in a test and slightly
	// wrong in practice.
	if d := til.Sub(b.now()); d > 0 {
		return d, strikes
	}
	return 0, strikes
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// LockedUntil reports a backoff in force for a credential.
func (b *Budget) LockedUntil(cred string) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var til time.Time
	b.withLedger(func(lf *ledgerFile) { til = lf.Locks[lockKey(cred)].Until })
	if b.now().Before(til) {
		return til, true
	}
	return time.Time{}, false
}

// AnyLocked reports how many credentials are backing off, and when the last of
// those locks ends. It is for display and for the watchdog, neither of which
// knows credentials.
func (b *Budget) AnyLocked() (time.Time, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	var last time.Time
	n := 0
	b.withLedger(func(lf *ledgerFile) {
		for _, l := range lf.Locks {
			if now.Before(l.Until) {
				n++
				if l.Until.After(last) {
					last = l.Until
				}
			}
		}
	})
	return last, n
}

// Remaining is how many scheduled (non-priority) calls are available now.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	b.withLedger(func(lf *ledgerFile) {
		cut := b.now().Add(-b.window)
		used := 0
		for _, t := range lf.Calls {
			if t.After(cut) {
				used++
			}
		}
		n = b.allowance - ReservedForSwap - used
	})
	if n < 0 {
		return 0
	}
	return n
}
