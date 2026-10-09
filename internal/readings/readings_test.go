package readings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func use(five, seven float64) *usage.Usage {
	return &usage.Usage{FiveHour: usage.Window{Utilization: &five}, SevenDay: usage.Window{Utilization: &seven}}
}

func openAt(t *testing.T, path string, now time.Time) *Log {
	t.Helper()
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return now }
	return l
}

// One reading per account per poll: the same reading (same time) twice is
// written once, also across a restart.
func TestRecordOncePerReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	now := time.Now().UTC().Truncate(time.Second) // Open compacts by the clock
	l := openAt(t, path, now)
	at := now.Add(-time.Minute)
	for i := 0; i < 3; i++ {
		if err := l.Record("w1", at, use(10, 40)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Record("w2", at, use(5, 20)); err != nil {
		t.Fatal(err)
	}
	l = openAt(t, path, now) // a restarted daemon
	if err := l.Record("w1", at, use(10, 40)); err != nil {
		t.Fatal(err)
	}
	if err := l.Record("w1", now, use(12, 41)); err != nil {
		t.Fatal(err)
	}
	rs, err := Read(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("%d readings, want 3: %+v", len(rs), rs)
	}
	if rs[0].Account != "w1" || *rs[0].FiveHour != 10 || *rs[0].SevenDay != 40 || !rs[0].At.Equal(at) {
		t.Errorf("first: %+v", rs[0])
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}

// An unknown window is null, never 0.
func TestRecordUnknownIsNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	l := openAt(t, path, time.Now())
	five := 7.0
	if err := l.Record("a", time.Now(), &usage.Usage{FiveHour: usage.Window{Utilization: &five}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"seven_day":null`) {
		t.Errorf("line: %s", b)
	}
	rs, _ := Read(path, time.Time{})
	if len(rs) != 1 || rs[0].SevenDay != nil || *rs[0].FiveHour != 7 {
		t.Errorf("%+v", rs)
	}
}

// Compacting drops what is older than the retention, thins what is older
// than a day to one reading per account per bucket (the last), and keeps the
// last day whole.
func TestCompact(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var rs []Reading
	add := func(acct string, at time.Time, v float64) {
		rs = append(rs, Reading{At: at, Account: acct, FiveHour: &v, SevenDay: &v})
	}
	add("w1", now.Add(-31*24*time.Hour), 1) // too old
	old := now.Add(-3 * 24 * time.Hour).Truncate(Bucket)
	for i := 0; i < 5; i++ { // one bucket: kept as one, the last
		add("w1", old.Add(time.Duration(i)*time.Minute), float64(10+i))
		add("w2", old.Add(time.Duration(i)*time.Minute), float64(20+i))
	}
	add("w1", old.Add(Bucket), 30) // the next bucket
	for i := 0; i < 5; i++ {       // the last day: all kept
		add("w1", now.Add(-time.Hour+time.Duration(i)*time.Minute), float64(40+i))
	}
	got := compact(rs, now, MaxBytes)
	var b strings.Builder
	for _, r := range got {
		fmt.Fprintf(&b, "%s %s %g\n", r.At.Format("01-02T15:04"), r.Account, *r.FiveHour)
	}
	want := "10-06T12:04 w1 14\n10-06T12:04 w2 24\n10-06T12:15 w1 30\n" +
		"10-09T11:00 w1 40\n10-09T11:01 w1 41\n10-09T11:02 w1 42\n10-09T11:03 w1 43\n10-09T11:04 w1 44\n"
	if b.String() != want {
		t.Errorf("compacted:\n%s\nwant:\n%s", b.String(), want)
	}
}

// Over the size limit, the oldest readings go first.
func TestCompactSizeLimit(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var rs []Reading
	for i := 0; i < 1000; i++ {
		v := float64(i % 100)
		rs = append(rs, Reading{At: now.Add(-time.Duration(1000-i) * time.Minute), Account: "w1", FiveHour: &v, SevenDay: &v})
	}
	got := compact(rs, now, 20_000)
	size := 0
	for _, r := range got {
		size += len(r.line())
	}
	if size > 20_000 || len(got) == 0 {
		t.Fatalf("%d readings, %d bytes", len(got), size)
	}
	if !got[len(got)-1].At.Equal(rs[len(rs)-1].At) {
		t.Error("the newest reading was dropped")
	}
}

// The log compacts itself when it grows past the size limit, and the file
// stays bounded however long the daemon runs.
func TestRecordStaysBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	now := time.Now().UTC().Truncate(time.Second) // Open compacts by the clock
	l := openAt(t, path, now)
	l.maxBytes = 30_000
	for i := 0; i < 2000; i++ {
		at := now.Add(-time.Duration(2000-i) * 30 * time.Second)
		l.now = func() time.Time { return at }
		if err := l.Record(fmt.Sprintf("acct-%d", i%3), at, use(float64(i%100), 50)); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 30_000 {
		t.Errorf("file is %d bytes, over the 30000 limit", info.Size())
	}
}

// Read skips a torn last line (a write in progress) and anything older than
// since.
func TestReadSkipsTornLinesAndOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	body := `{"at":"2026-10-01T00:00:00Z","account":"a","five_hour":1,"seven_day":2}
{"at":"2026-10-08T00:00:00Z","account":"a","five_hour":3,"seven_day":4}
{"at":"2026-10-08T00:05:00Z","account":"a","five_ho`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := Read(path, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if err != nil || len(rs) != 1 || *rs[0].FiveHour != 3 {
		t.Fatalf("%+v %v", rs, err)
	}
	if rs, err := Read(filepath.Join(t.TempDir(), "none"), time.Time{}); err != nil || len(rs) != 0 {
		t.Errorf("a missing file is no readings: %v %v", rs, err)
	}
}
