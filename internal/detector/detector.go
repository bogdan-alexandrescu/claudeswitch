// Package detector watches Claude Code's transcripts for rate-limit rejections.
//
// Ground truth #3, #9, #10: on refusal Claude Code writes a local "<synthetic>"
// assistant record carrying a quotaLimits object with the window type and an
// authoritative resetsAt. It appears within milliseconds and needs no network.
//
// It is a SAFETY NET, not the primary sensor — the usage poller is. Its job is
// to catch anything that slips between polls, and its resetsAt overrides
// whatever the poller last believed.
//
// Two facts shape the implementation:
//   - Every retry writes another record. Over 21 days, 6,290 raw records were
//     only 24 real limit hits. Dedupe by (rateLimitType, resetsAt).
//   - There are 2,197+ transcript files and new ones appear while running, so
//     files are watched, never re-read wholesale.
package detector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// marker is the cheap substring test that avoids JSON-parsing every line.
const marker = `"quotaLimits"`

// Window types as they appear in rateLimitType.
const (
	FiveHour = "five_hour"
	SevenDay = "seven_day"
)

// Rejection is one genuine limit hit, after dedupe.
type Rejection struct {
	Type     string    // five_hour | seven_day
	ResetsAt time.Time // authoritative
	SeenAt   time.Time
	File     string

	// Latency is the gap between the transcript record being written and this
	// detector emitting it. The reactive path is only worth having if it beats
	// the retry, so the number is measured rather than assumed. Derived from the
	// file's modification time, which bounds it from above: the record was
	// written at or before that moment.
	Latency time.Duration
}

// Key identifies a real hit. Every retry of the same refusal shares it.
func (r Rejection) Key() string {
	return r.Type + "@" + r.ResetsAt.UTC().Format(time.RFC3339)
}

type quotaLimit struct {
	Status                        string `json:"status"`
	ResetsAt                      int64  `json:"resetsAt"`
	RateLimitType                 string `json:"rateLimitType"`
	OverageStatus                 string `json:"overageStatus"`
	UnifiedRateLimitFallbackAvail bool   `json:"unifiedRateLimitFallbackAvailable"`
}

// line is the subset of a transcript record we care about. quotaLimits has been
// observed as an object; tolerate an array too rather than break on a change.
type line struct {
	Timestamp string          `json:"timestamp"`
	Quota     json.RawMessage `json:"quotaLimits"`
	CWD       string          `json:"cwd"`
}

type Detector struct {
	root    string
	log     *slog.Logger
	mu      sync.Mutex
	offsets map[string]int64
	seen    map[string]time.Time // dedupe key -> first seen
	out     chan Rejection
	// lastWrite is the newest transcript write we have been told about. The
	// watcher already sees every write, so tracking it here costs nothing —
	// whereas walking the tree to find it costs a stat of every file, and this
	// tree is 7,654 files deep.
	lastWrite time.Time

	// worstLatency is the largest observed gap between a rejection being written
	// and being emitted. Shown in status, because the whole reactive path rests
	// on this being small.
	worstLatency time.Duration

	// lastCWD is the working directory of the most recent session activity, and
	// cwdFrom is the transcript it came from. Project rules need to know where
	// work is happening; the transcripts record it on every line, but parsing
	// every line is ruinous, so one per file is enough.
	lastCWD string
	cwdFrom string
}

// ProjectsRoot is where Claude Code keeps transcripts.
func ProjectsRoot() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "projects"
	}
	return filepath.Join(h, ".claude", "projects")
}

func New(root string, log *slog.Logger) *Detector {
	if root == "" {
		root = ProjectsRoot()
	}
	return &Detector{
		root:    root,
		log:     log,
		offsets: map[string]int64{},
		seen:    map[string]time.Time{},
		out:     make(chan Rejection, 16),
	}
}

// Rejections is the stream of deduped hits.
func (d *Detector) Rejections() <-chan Rejection { return d.out }

// Run watches until ctx-like stop channel closes. Existing files are seeded to
// their current end: history is for `backfill`, not for the live stream.
func (d *Detector) Run(stop <-chan struct{}) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	d.seedOffsets()
	d.ScanNow()
	d.seedCWD()
	d.addWatches(w)

	// fsnotify can miss a newly created project directory; a slow rescan is the
	// cheap insurance. It only adds watches, it does not re-read files.
	rescan := time.NewTicker(60 * time.Second)
	defer rescan.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-rescan.C:
			d.addWatches(w)
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if strings.HasSuffix(ev.Name, ".jsonl") {
				d.mu.Lock()
				d.lastWrite = time.Now()
				d.mu.Unlock()
				d.scanFile(ev.Name)
			} else if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
				_ = w.Add(ev.Name)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			d.log.Warn("transcript watcher error", "err", err)
		}
	}
}

func (d *Detector) addWatches(w *fsnotify.Watcher) {
	_ = w.Add(d.root)
	entries, err := os.ReadDir(d.root)
	if err != nil {
		d.log.Warn("cannot read transcripts root", "path", d.root, "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			_ = w.Add(filepath.Join(d.root, e.Name()))
		}
	}
}

// seedOffsets marks every existing file as already-read, so starting the daemon
// does not replay weeks of old rejections as if they just happened.
func (d *Detector) seedOffsets() {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = filepath.WalkDir(d.root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if fi, err := e.Info(); err == nil {
			d.offsets[p] = fi.Size()
		}
		return nil
	})
}

func (d *Detector) scanFile(path string) {
	d.mu.Lock()
	off := d.offsets[path]
	d.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < off { // truncated or rotated
		off = 0
	}
	if _, err := f.Seek(off, 0); err != nil {
		return
	}

	// Only look for a working directory when this is a different session from
	// the one we last recorded.
	d.mu.Lock()
	needCWD := d.cwdFrom != path
	d.mu.Unlock()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript lines get large
	var read int64
	for sc.Scan() {
		b := sc.Bytes()
		read += int64(len(b)) + 1
		// Note where the work is happening, for project rules — but only once per
		// file. Every transcript line carries a cwd, and unmarshalling each one
		// was enough allocation churn to starve the whole daemon: it stopped
		// polling entirely and its readings went stale while it burned CPU in
		// the garbage collector (observed 2026-09-10).
		//
		// A session's directory does not change, so the first line of a file is
		// as good as the last.
		if needCWD && bytes.Contains(b, []byte(`"cwd"`)) {
			var l line
			if json.Unmarshal(b, &l) == nil && l.CWD != "" {
				d.mu.Lock()
				d.lastCWD, d.cwdFrom = l.CWD, path
				d.mu.Unlock()
				needCWD = false
			}
		}
		if !strings.Contains(string(b), marker) {
			continue
		}
		for _, q := range parseQuota(b) {
			if q.Status != "rejected" || q.ResetsAt == 0 {
				continue
			}
			now := time.Now()
			lat := time.Duration(0)
			if mt := fi.ModTime(); !mt.IsZero() && now.After(mt) {
				lat = now.Sub(mt)
			}
			d.emit(Rejection{
				Type:     q.RateLimitType,
				ResetsAt: time.Unix(q.ResetsAt, 0),
				SeenAt:   now,
				File:     path,
				Latency:  lat,
			})
		}
	}
	d.mu.Lock()
	d.offsets[path] = off + read
	d.mu.Unlock()
}

func parseQuota(b []byte) []quotaLimit {
	var l line
	if err := json.Unmarshal(b, &l); err != nil || len(l.Quota) == 0 {
		return nil
	}
	var one quotaLimit
	if err := json.Unmarshal(l.Quota, &one); err == nil && one.Status != "" {
		return []quotaLimit{one}
	}
	var many []quotaLimit
	if err := json.Unmarshal(l.Quota, &many); err == nil {
		return many
	}
	return nil
}

// emit publishes a rejection unless it is a retry of one already reported.
func (d *Detector) emit(r Rejection) {
	d.mu.Lock()
	key := r.Key()
	if _, dup := d.seen[key]; dup {
		d.mu.Unlock()
		return
	}
	d.seen[key] = r.SeenAt
	d.pruneSeenLocked()
	d.mu.Unlock()

	d.mu.Lock()
	if r.Latency > d.worstLatency {
		d.worstLatency = r.Latency
	}
	d.mu.Unlock()

	select {
	case d.out <- r:
	default:
		d.log.Warn("rejection dropped: consumer not keeping up", "type", r.Type)
	}
}

// pruneSeenLocked forgets dedupe keys whose window has long reset, so the map
// cannot grow without bound in a long-lived daemon.
func (d *Detector) pruneSeenLocked() {
	if len(d.seen) < 512 {
		return
	}
	cut := time.Now().Add(-14 * 24 * time.Hour)
	for k, seen := range d.seen {
		if seen.Before(cut) {
			delete(d.seen, k)
		}
	}
}

// Backfill reads history and returns the deduped rejections, newest last. It is
// for `status` and for verifying the detector against known data — never for the
// live stream.
func Backfill(root string, since time.Duration, log *slog.Logger) ([]Rejection, int, error) {
	d := New(root, log)
	cut := time.Now().Add(-since)
	var raw int
	dedup := map[string]Rejection{}

	err := filepath.WalkDir(d.root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().Before(cut) {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			b := sc.Bytes()
			if !strings.Contains(string(b), marker) {
				continue
			}
			for _, q := range parseQuota(b) {
				if q.Status != "rejected" || q.ResetsAt == 0 {
					continue
				}
				raw++
				r := Rejection{Type: q.RateLimitType, ResetsAt: time.Unix(q.ResetsAt, 0), File: p}
				if prev, ok := dedup[r.Key()]; !ok || fi.ModTime().Before(prev.SeenAt) {
					r.SeenAt = fi.ModTime()
					dedup[r.Key()] = r
				}
			}
		}
		return nil
	})

	out := make([]Rejection, 0, len(dedup))
	for _, r := range dedup {
		out = append(out, r)
	}
	for i := 1; i < len(out); i++ { // insertion sort by ResetsAt; the set is tiny
		for j := i; j > 0 && out[j].ResetsAt.Before(out[j-1].ResetsAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, raw, err
}

// LastActivity returns the most recent transcript write this detector has
// observed, which is the best available proxy for "a session is mid-turn".
//
// Honest about what this is: a heuristic. Claude Code writes to the transcript
// as a turn progresses, so recent writes mean recent activity — but a long
// think with no writes looks idle, and a background agent's writes look like
// user activity. Good enough to prefer an idle gap; the hard floor exists so a
// wrong answer here cannot cost a wall.
//
// It reads what the watcher already told us rather than walking the tree. The
// walk version stat'd 7,654 files every twenty seconds, which showed up as the
// daemon's entire CPU cost.
func (d *Detector) LastActivity() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastWrite
}

// IdleFor reports whether no transcript has been written for at least dur. A
// detector that has seen nothing yet counts as idle: at startup there is no
// evidence of a turn in flight.
func (d *Detector) IdleFor(dur time.Duration) bool {
	last := d.LastActivity()
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= dur
}

// CurrentDir is the working directory of the most recent session activity, or
// empty when nothing has been seen yet. Project rules are applied against it.
func (d *Detector) CurrentDir() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastCWD
}

// WorstLatency is the largest record-written-to-emitted gap seen this run.
func (d *Detector) WorstLatency() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.worstLatency
}

// seedCWD reads the newest transcript's last record so a freshly started daemon
// knows where work is happening without waiting for the next write.
func (d *Detector) seedCWD() {
	newest, newestAt := "", time.Time{}
	_ = filepath.WalkDir(d.root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newestAt) {
			newest, newestAt = p, fi.ModTime()
		}
		return nil
	})
	if newest == "" {
		return
	}
	f, err := os.Open(newest)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	cwd := ""
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.CWD != "" {
			cwd = l.CWD
			break // a session's directory does not change
		}
	}
	if cwd != "" {
		d.mu.Lock()
		d.lastCWD, d.cwdFrom = cwd, newest
		d.mu.Unlock()
	}
}

// ScanNow seeds lastWrite once at startup so a daemon launched mid-turn does
// not believe the machine is idle. One walk, not one per tick.
func (d *Detector) ScanNow() {
	newest := time.Time{}
	_ = filepath.WalkDir(d.root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	d.mu.Lock()
	d.lastWrite = newest
	d.mu.Unlock()
}
