package usage

import (
	"testing"
	"time"
)

// R3 (IMPROVEMENTS, 2026-10-09). A long Retry-After is the server saying the
// refusal will last. Cutting it to a fixed 20m made the next call a sure
// second strike, so a repeat long-wait refusal of the same token waits
// longer each time: 20m, 40m, 60m for an idle account, 20m then 30m for the
// account in use.
func TestARepeatLongWaitRefusalBacksOffLonger(t *testing.T) {
	for _, c := range []struct {
		live bool
		want []time.Duration
	}{
		{false, []time.Duration{20 * time.Minute, 40 * time.Minute, 60 * time.Minute, 60 * time.Minute}},
		{true, []time.Duration{20 * time.Minute, 30 * time.Minute, 30 * time.Minute}},
	} {
		b, now := fixedBudget(time.Now())
		for i, want := range c.want {
			if c.live {
				b.PenalizeLive("tok", time.Hour)
			} else {
				b.Penalize("tok", time.Hour)
			}
			got, _ := b.CurrentBackoff("tok")
			if got != want {
				t.Errorf("live=%v long-wait refusal %d: backoff %v, want %v", c.live, i+1, got, want)
			}
			*now = now.Add(got + time.Second)
		}
	}
}

// A credential some profile has live (MarkLive) gets the live schedule from
// whoever reports the refusal, as it gets the live cap.
func TestAMarkedLiveCredentialGetsTheLiveLongWaitSchedule(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.MarkLive("tok")
	var got time.Duration
	for i := 0; i < 3; i++ {
		b.Penalize("tok", time.Hour)
		got, _ = b.CurrentBackoff("tok")
		*now = now.Add(got + time.Second)
		b.MarkLive("tok")
	}
	if got != 30*time.Minute {
		t.Errorf("third long-wait refusal of a live credential: %v, want 30m", got)
	}
}

// The schedule caps what is honoured of Retry-After; it never shortens it
// below that. A server asking for less than the step is given what it asked.
func TestRetryAfterIsHonouredUpToTheStep(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("tok", 25*time.Minute)
	if d, _ := b.CurrentBackoff("tok"); d != 20*time.Minute {
		t.Errorf("first refusal asking 25m: %v, want the 20m step", d)
	}
	*now = now.Add(20*time.Minute + time.Second)
	b.Penalize("tok", 25*time.Minute)
	if d, _ := b.CurrentBackoff("tok"); d != 25*time.Minute {
		t.Errorf("second refusal asking 25m: %v, want the 25m asked (the step allows 40m)", d)
	}

	b2, _ := fixedBudget(time.Now())
	b2.Penalize("tok", 12*time.Minute)
	if d, _ := b2.CurrentBackoff("tok"); d != 12*time.Minute {
		t.Errorf("a 12m Retry-After: %v, want 12m", d)
	}
}

// §42's burst limit answers Retry-After: 0. Its schedule is unchanged: a 5m
// first strike, doubling, at most 20m (10m live); a long-wait schedule does
// not apply to it.
func TestRetryAfterZeroKeepsTheShortFirstStrike(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("tok", time.Hour)
	d, _ := b.CurrentBackoff("tok")
	*now = now.Add(d + time.Second)
	b.Succeeded("tok")
	b.Penalize("tok", 0)
	if d, _ := b.CurrentBackoff("tok"); d != MinBackoff {
		t.Errorf("Retry-After 0 after a success: %v, want %v", d, MinBackoff)
	}
}

// The longest lock the schedule makes is the cap.
func TestMaxLockCoversTheLongestStep(t *testing.T) {
	if MaxLock < 60*time.Minute {
		t.Errorf("MaxLock %v is shorter than the 60m step", MaxLock)
	}
}

// R2: a token past its expiry is never sent; Expired says which are.
func TestExpired(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		exp  time.Time
		want bool
	}{
		{time.Time{}, false}, // unknown is not expired
		{now.Add(time.Minute), false},
		{now, true},
		{now.Add(-time.Minute), true},
	} {
		if got := Expired(c.exp, now); got != c.want {
			t.Errorf("Expired(%v) = %v, want %v", c.exp.Sub(now), got, c.want)
		}
	}
}
