// Package audit records every observation, decision and swap.
//
// A rotation that surprises the user must be reconstructable afterwards: what
// was known, what was decided, and why. Append-only JSONL, 0600.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"` // decision | switch | rejection | error
	Decision string    `json:"decision,omitempty"`
	From     string    `json:"from,omitempty"`
	To       string    `json:"to,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Forced   bool      `json:"forced,omitempty"`
	DryRun   bool      `json:"dry_run,omitempty"`
	Window   string    `json:"window,omitempty"`
	ResetsAt time.Time `json:"resets_at,omitzero"`
	FiveHour *float64  `json:"five_hour,omitempty"`
	SevenDay *float64  `json:"seven_day,omitempty"`
	Err      string    `json:"error,omitempty"`

	// Severity transitions. The API's own limits[] carries a severity field;
	// "warning" was first seen at 78% utilization on 2026-09-09. Recording every
	// transition is how we learn whether it has a consistent onset, and whether
	// a further level exists before an outright rejection.
	Account      string   `json:"account,omitempty"`
	LimitKind    string   `json:"limit_kind,omitempty"`
	FromSeverity string   `json:"from_severity,omitempty"`
	ToSeverity   string   `json:"to_severity,omitempty"`
	Percent      *float64 `json:"percent,omitempty"`
}

type Log struct {
	mu   sync.Mutex
	path string
}

func DefaultPath() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", "claudeswitch", "audit.jsonl")
	}
	return "audit.jsonl"
}

func Open(path string) (*Log, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Log{path: path}, nil
}

// Write appends an event. Failing to audit must never stop the daemon, so the
// error is returned for logging rather than treated as fatal.
func (l *Log) Write(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Tail returns the last n events, newest last.
func Tail(path string, n int) ([]Event, error) {
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Event
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		var e Event
		if json.Unmarshal(b[start:i], &e) == nil {
			all = append(all, e)
		}
		start = i + 1
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// Filter narrows a slice of events by kind and age. Both are optional; an empty
// kind or zero duration means "no restriction".
func Filter(events []Event, kind string, since time.Duration) []Event {
	if kind == "" && since == 0 {
		return events
	}
	cut := time.Time{}
	if since > 0 {
		cut = time.Now().Add(-since)
	}
	out := events[:0:0]
	for _, e := range events {
		if kind != "" && e.Kind != kind {
			continue
		}
		if !cut.IsZero() && e.At.Before(cut) {
			continue
		}
		out = append(out, e)
	}
	return out
}
