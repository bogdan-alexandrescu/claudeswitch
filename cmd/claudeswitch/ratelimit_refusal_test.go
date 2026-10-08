package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// R1: an unknown caused by a rate-limit lock carries the time the lock clears
// out of the §3 check; any other unknown carries none.
func TestLiveElsewhereOfCarriesTheRetryTime(t *testing.T) {
	cfg := doctorConfig(t, twoProfileTOML)
	targets := []liveTarget{{name: "default", live: fakeLive{"def-item"}}, {name: "work", live: fakeLive{"work-item"}}}
	st, _ := state.Load(filepath.Join(t.TempDir(), "s.json"), "default", "work")
	at := time.Now().Add(3 * time.Minute)

	v := &fakeVault{holds: map[string]string{"work-item/personal": "unknown"},
		seatRetryAt: map[string]time.Time{"work-item/personal": at}}
	other, why, retryAt := liveElsewhereOf(context.Background(), v, cfg, st, "default", targets, "personal")
	if other != "work" || why == "" {
		t.Fatalf("liveElsewhereOf = %q (%s), want a refusal naming work", other, why)
	}
	if !retryAt.Equal(at) {
		t.Fatalf("retryAt = %v, want %v", retryAt, at)
	}

	v = &fakeVault{holds: map[string]string{"work-item/personal": "unknown"}}
	other, why, retryAt = liveElsewhereOf(context.Background(), v, cfg, st, "default", targets, "personal")
	if other != "work" || !retryAt.IsZero() {
		t.Fatalf("plain unknown = %q, %v; want work with no retry time", other, retryAt)
	}
	if why != "it could not be confirmed absent from the live credential" {
		t.Errorf("a plain unknown changed its wording: %q", why)
	}
}

// The refusal the CLI gives (use, login, profile create --seed): the owner's
// wording, code `live` kept, and retry_at in the JSON error object.
func TestRateLimitedRefusalWordingAndJSON(t *testing.T) {
	at := time.Date(2026, 10, 8, 14, 3, 7, 0, time.Local)
	err := rateLimitedRefusal("personal", "work", at)
	want := `can't confirm "personal" isn't signed in under profile "work": ` +
		`the check is rate limited until 14:03:07; try again then`
	if err.Message != want {
		t.Fatalf("message = %q\nwant      %q", err.Message, want)
	}
	if err.Code != codeLive {
		t.Errorf("code = %q, want %q (the app branches on it)", err.Code, codeLive)
	}
	raw, _ := json.Marshal(errorObject(err))
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			RetryAt string `json:"retry_at"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(raw, &got); jerr != nil {
		t.Fatal(jerr)
	}
	if got.Error.RetryAt != at.Format(time.RFC3339) {
		t.Errorf("retry_at = %q, want %q", got.Error.RetryAt, at.Format(time.RFC3339))
	}
	if got.Error.Code != codeLive || got.Error.Message != want {
		t.Errorf("error object = %+v", got.Error)
	}

	// Every other error carries retry_at null (absent values are null, never
	// a missing key).
	raw, _ = json.Marshal(errorObject(appErr(codeLive, "", "x")))
	if !strings.Contains(string(raw), `"retry_at":null`) {
		t.Errorf("a plain refusal's retry_at is not null: %s", raw)
	}
}

// The daemon's refusal says the same, in its log line, audit row and
// notification.
func TestDaemonRateLimitedRefusalNamesTheRetryTime(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	overWorkPool(r)
	r.d.st.Profile("default").SetActive("a")
	at := time.Now().Add(4 * time.Minute)
	r.v.holds = map[string]string{"item-default/w2": "unknown"}
	r.v.seatRetryAt = map[string]time.Time{"item-default/w2": at}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("wrote %+v under an unknown", swaps)
	}
	clock := "rate limited until " + at.Local().Format("15:04:05") + "; try again then"
	errs := r.aud.kind("error")
	if len(errs) != 1 || !strings.Contains(errs[0].Err, clock) ||
		!strings.Contains(errs[0].Err, "can't confirm w2 isn't signed in under profile default") {
		t.Fatalf("audit rows = %+v, want one naming %q", errs, clock)
	}
	if !strings.Contains(r.logs.String(), at.Local().Format("15:04:05")) {
		t.Errorf("log does not name the retry time:\n%s", r.logs.String())
	}
	if len(r.nt.sent) != 1 || !strings.Contains(r.nt.sent[0], clock) {
		t.Errorf("notifications = %q, want one naming %q", r.nt.sent, clock)
	}
}
