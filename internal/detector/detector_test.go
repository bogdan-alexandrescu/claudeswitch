package detector

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// rejectionLine mimics the real record: a "<synthetic>" assistant message
// carrying quotaLimits. Shape taken from ground truth #3.
func rejectionLine(kind string, resetsAt int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-09-04T09:27:54.667Z",`+
		`"message":{"model":"<synthetic>","usage":{"input_tokens":1,"output_tokens":1}},`+
		`"quotaLimits":{"status":"rejected","resetsAt":%d,"rateLimitType":%q,`+
		`"unifiedRateLimitFallbackAvailable":false,"overageStatus":"rejected",`+
		`"overageDisabledReason":"org_level_disabled","isUsingOverage":false}}`, resetsAt, kind)
}

func normalLine() string {
	return `{"type":"assistant","timestamp":"2026-09-04T09:20:00.000Z",` +
		`"message":{"model":"claude-opus-5","usage":{"input_tokens":2,"output_tokens":259}}}`
}

// The observed ratio is the whole reason dedupe exists: 6,290 raw records over
// 21 days were only 24 real limit hits, because every retry writes another one.
func TestBackfillCollapsesRetriesIntoRealHits(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-someone-project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Add(-2 * time.Hour).Unix()
	var content string
	realHits := 3
	retriesEach := 200
	for h := 0; h < realHits; h++ {
		resets := base + int64(h*3600)
		for r := 0; r < retriesEach; r++ {
			content += rejectionLine(FiveHour, resets) + "\n"
			content += normalLine() + "\n"
		}
	}
	// A different window type on the same reset time is a genuinely separate hit.
	content += rejectionLine(SevenDay, base) + "\n"

	if err := os.WriteFile(filepath.Join(proj, "sess.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	hits, raw, err := Backfill(root, 24*time.Hour, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if wantRaw := realHits * retriesEach; raw != wantRaw+1 {
		t.Errorf("raw records: got %d, want %d", raw, wantRaw+1)
	}
	if want := realHits + 1; len(hits) != want {
		t.Fatalf("deduped hits: got %d, want %d (retries of one refusal are one hit)", len(hits), want)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].ResetsAt.Before(hits[i-1].ResetsAt) {
			t.Fatal("hits must come back in chronological order")
		}
	}
}

func TestBackfillIgnoresNonRejections(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	os.MkdirAll(proj, 0o755)
	body := normalLine() + "\n" +
		`{"type":"assistant","quotaLimits":{"status":"allowed","resetsAt":123,"rateLimitType":"five_hour"}}` + "\n"
	os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(body), 0o644)

	hits, raw, err := Backfill(root, 24*time.Hour, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if raw != 0 || len(hits) != 0 {
		t.Fatalf("only status==rejected counts: raw=%d hits=%d", raw, len(hits))
	}
}

func TestBackfillSkipsFilesOlderThanWindow(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	os.MkdirAll(proj, 0o755)
	f := filepath.Join(proj, "old.jsonl")
	os.WriteFile(f, []byte(rejectionLine(FiveHour, time.Now().Unix())+"\n"), 0o644)
	old := time.Now().Add(-30 * 24 * time.Hour)
	os.Chtimes(f, old, old)

	hits, _, err := Backfill(root, 24*time.Hour, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("a file older than the window must not be read: got %d hits", len(hits))
	}
}

func TestRejectionKeyIdentifiesOneRealHit(t *testing.T) {
	ts := time.Unix(1788520200, 0)
	a := Rejection{Type: FiveHour, ResetsAt: ts}
	b := Rejection{Type: FiveHour, ResetsAt: ts, SeenAt: time.Now()}
	c := Rejection{Type: SevenDay, ResetsAt: ts}
	if a.Key() != b.Key() {
		t.Error("retries of the same refusal must share a key")
	}
	if a.Key() == c.Key() {
		t.Error("different window types are different hits")
	}
}

// Latency is what justifies the reactive path existing: it only helps if it
// beats the retry. Assert it is measured and bounded, not left at zero.
func TestRejectionCarriesMeasuredLatency(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(proj, "s.jsonl")
	if err := os.WriteFile(f, []byte(rejectionLine(FiveHour, time.Now().Unix())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate the file so a known gap exists between "written" and "seen".
	written := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(f, written, written); err != nil {
		t.Fatal(err)
	}

	d := New(root, quietLog())
	d.scanFile(f) // offsets start at zero for a file we never seeded

	select {
	case r := <-d.Rejections():
		if r.Latency <= 0 {
			t.Fatal("latency was not measured")
		}
		if r.Latency < time.Second {
			t.Fatalf("latency %v is smaller than the 2s backdate; the measurement is wrong", r.Latency)
		}
		if d.WorstLatency() != r.Latency {
			t.Fatalf("WorstLatency %v does not track the emitted rejection %v",
				d.WorstLatency(), r.Latency)
		}
	default:
		t.Fatal("no rejection emitted")
	}
}
