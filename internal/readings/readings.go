// Package readings keeps the usage readings the daemon takes, for `cs
// history --usage` and the app's History charts (IMPROVEMENTS F7).
//
// state.json holds only each account's last two readings. This log keeps
// the past: one JSON line per reading, appended by the daemon when a poll
// brings an account a new one, and read-only to everyone else.
//
//	~/.local/state/claudeswitch/readings.jsonl   (0600)
//	{"at":"2026-10-09T11:58:02Z","account":"work-1","five_hour":42,"seven_day":61.5}
//
// at is when the API reported the figures (state's last_at), UTC; a window
// it did not report is null, never 0. Lines are in the order written, which
// is time order per account.
//
// It stays bounded. Compacting rewrites the file (to a temporary file, then
// renamed over it, so a reader never sees half of it): readings older than
// Retention go; those older than FineFor are thinned to the last one per
// account per Bucket; and past MaxBytes the oldest go until it is three
// quarters of that. The daemon compacts when it opens the log, every
// CompactEvery, and whenever an append takes the file over MaxBytes. At the
// default cadence that is well under a megabyte for a handful of accounts.
package readings

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

const (
	Retention    = 30 * 24 * time.Hour // older readings are dropped
	FineFor      = 24 * time.Hour      // newer readings are all kept
	Bucket       = 15 * time.Minute    // older ones: the last per account per bucket
	MaxBytes     = 4 << 20             // the file's ceiling
	CompactEvery = 6 * time.Hour
)

// Reading is one usage reading of one account.
type Reading struct {
	At       time.Time `json:"at"`
	Account  string    `json:"account"`
	FiveHour *float64  `json:"five_hour"`
	SevenDay *float64  `json:"seven_day"`
}

func (r Reading) line() []byte {
	b, _ := json.Marshal(r)
	return append(b, '\n')
}

// DefaultPath is the log beside state.json.
func DefaultPath() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", "claudeswitch", "readings.jsonl")
	}
	return "readings.jsonl"
}

// Log is the daemon's writer. It is not safe for concurrent use; the daemon
// records from the one goroutine that owns its state.
type Log struct {
	path        string
	last        map[string]time.Time // the newest reading written per account
	lastCompact time.Time
	maxBytes    int
	now         func() time.Time
}

// Open opens (creating its directory) and compacts the log.
func Open(path string) (*Log, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &Log{path: path, last: map[string]time.Time{}, maxBytes: MaxBytes, now: time.Now}
	rs, err := Read(path, time.Time{})
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if r.At.After(l.last[r.Account]) {
			l.last[r.Account] = r.At
		}
	}
	if len(rs) > 0 {
		return l, l.rewrite(rs)
	}
	return l, nil
}

// Record appends an account's reading taken at at, unless it is the one
// already written (or older): a reading is written once however many times
// the daemon passes it.
func (l *Log) Record(account string, at time.Time, u *usage.Usage) error {
	if u == nil || at.IsZero() || !at.After(l.last[account]) || l.now().Sub(at) > Retention {
		return nil
	}
	r := Reading{At: at.UTC(), Account: account}
	if u.FiveHour.Known() {
		v := u.FiveHour.Pct()
		r.FiveHour = &v
	}
	if u.SevenDay.Known() {
		v := u.SevenDay.Pct()
		r.SevenDay = &v
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(r.line())
	info, serr := f.Stat()
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	l.last[account] = at
	if l.lastCompact.IsZero() {
		l.lastCompact = l.now()
	}
	if (serr == nil && info.Size() > int64(l.maxBytes)) || l.now().Sub(l.lastCompact) >= CompactEvery {
		return l.Compact()
	}
	return nil
}

// Compact rewrites the log within its bounds.
func (l *Log) Compact() error {
	rs, err := Read(l.path, time.Time{})
	if err != nil {
		return err
	}
	return l.rewrite(rs)
}

func (l *Log) rewrite(rs []Reading) error {
	l.lastCompact = l.now()
	var b bytes.Buffer
	for _, r := range compact(rs, l.now(), l.maxBytes) {
		b.Write(r.line())
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".readings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.path)
}

// compact applies the bounds: retention, thinning past FineFor, and the
// size ceiling (oldest first, down to three quarters of it).
func compact(rs []Reading, now time.Time, maxBytes int) []Reading {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
	type key struct {
		account string
		bucket  time.Time
	}
	lastIn := map[key]int{} // index of the last reading in each old bucket
	for i, r := range rs {
		if now.Sub(r.At) > FineFor {
			lastIn[key{r.Account, r.At.Truncate(Bucket)}] = i
		}
	}
	var out []Reading
	size := 0
	for i, r := range rs {
		age := now.Sub(r.At)
		if age > Retention {
			continue
		}
		if age > FineFor && lastIn[key{r.Account, r.At.Truncate(Bucket)}] != i {
			continue
		}
		out = append(out, r)
		size += len(r.line())
	}
	if size > maxBytes {
		keep := maxBytes * 3 / 4
		for len(out) > 0 && size > keep {
			size -= len(out[0].line())
			out = out[1:]
		}
	}
	return out
}

// Read is every reading at or after since (zero: all), oldest first. A
// missing file is no readings; a line that does not parse (one being
// written) is skipped.
func Read(path string, since time.Time) ([]Reading, error) {
	if path == "" {
		path = DefaultPath()
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Reading
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Reading
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Account == "" || r.At.IsZero() {
			continue
		}
		if !since.IsZero() && r.At.Before(since) {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
