package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
)

type Budget struct {
	mu        sync.Mutex
	allowance int
	window    time.Duration
	calls     []time.Time
	locks     map[string]accountLock // set when the API tells us to back off
	now       func() time.Time
	// ledger is the file backing this budget across processes. Empty keeps it
	// process-local, which is what the tests want.
	ledger string
	// lastCall is when this process last spent a call, for pacing.
	lastCall time.Time
}

func NewBudget() *Budget {
	return &Budget{allowance: DefaultAllowance, window: DefaultWindow, now: time.Now}
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
		lf := &ledgerFile{Calls: b.calls, Locks: b.locks}
		fn(lf)
		lf.gc(b.now())
		b.calls, b.locks = lf.Calls, lf.Locks
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

	var lf ledgerFile
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
	b.calls, b.locks = lf.Calls, lf.Locks
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
	last := b.lastCall
	b.mu.Unlock()
	if last.IsZero() {
		return
	}
	if wait := MinSpacing - time.Since(last); wait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
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

		// Recorded, so it still counts against the window and still paces —
		// but never refused. See Interactive.
		if p == Interactive {
			lf.Calls = append(lf.Calls, now)
			b.lastCall = now
			ok, reason = true, ReasonOK
			return
		}
		if now.Before(lf.Locks[lockKey(cred)].Until) {
			ok, reason = false, ReasonLockout
			return
		}

		limit := b.allowance
		if p == Scheduled {
			limit -= ReservedForSwap
		}
		if len(lf.Calls) >= limit {
			switch {
			case p != Scheduled, len(lf.Calls) >= b.allowance:
				ok, reason = false, ReasonBudget
			default:
				ok, reason = false, ReasonReserved
			}
			return
		}
		lf.Calls = append(lf.Calls, now)
		b.lastCall = now
		ok, reason = true, ReasonOK
	})
	return ok, reason
}

// MinBackoff is applied when the API reports a 429 without a usable Retry-After.
// A backoff of zero is not a backoff: it was observed producing a warning every
// 80 seconds while the daemon kept hammering an endpoint that had just refused
// it (2026-09-09).
const MinBackoff = 60 * time.Second

// MaxBackoff caps the wait after repeated refusals. Long enough to stop
// hammering, short enough that recovery is not missed by an hour.
const MaxBackoff = 16 * time.Minute

// MaxLiveBackoff caps it for the account in use. That account is the one a
// rotation decides on, and it is also the one refused most, because Claude Code
// spends its allowance too — five straight refusals left it unread for 25
// minutes at 61% (2026-09-17), long enough to cross the trigger unseen. Idle
// accounts can wait; this one cannot. A longer Retry-After from the server is
// still honoured.
const MaxLiveBackoff = 4 * time.Minute

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
