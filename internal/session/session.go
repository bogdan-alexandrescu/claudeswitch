// Package session reconstructs a stretch of work that spans several accounts.
//
// Rotation makes "how much have I used?" a question no single account can
// answer: the work continues across swaps, and each account only knows its own
// share. This joins two local sources to answer it —
//
//   - the transcripts, which carry per-message token counts and timestamps
//   - the audit log, which records when the active account changed
//
// — attributing every message to whichever account was live when it was written.
// Nothing here calls the network, so it costs no quota and works offline.
package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
)

// Tokens is one message's accounting, as Claude Code records it.
type Tokens struct {
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
	Thinking      int64
}

func (t Tokens) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheCreation
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheCreation += o.CacheCreation
	t.Thinking += o.Thinking
}

// Share is one account's part of a session.
type Share struct {
	Account  string
	Tokens   Tokens
	Messages int
	Active   time.Duration
	Models   map[string]int
}

// Session is the whole span.
type Session struct {
	From, To time.Time
	Shares   []Share
	Total    Tokens
	Messages int
	Switches []audit.Event
	// Unattributed counts messages written while no account was known to be
	// active — before the first recorded switch, typically. They are reported
	// rather than silently folded into a neighbour.
	Unattributed Tokens
	UnattrCount  int
}

func (s *Session) Span() time.Duration { return s.To.Sub(s.From) }

// timeline is the ordered list of "account X became active at T".
type span struct {
	at      time.Time
	account string
}

// buildTimeline reconstructs which account was active over the window, from the
// audit log's switch events. `active` is the account live now, which anchors the
// end of the timeline when no switch has happened inside the window.
func buildTimeline(events []audit.Event, from time.Time, active string) []span {
	var spans []span
	for _, e := range events {
		if e.Kind != "switch" || e.To == "" {
			continue
		}
		spans = append(spans, span{at: e.At, account: e.To})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].at.Before(spans[j].at) })

	// Work out who was active at the start of the window: the target of the last
	// switch before it, or — failing that — the account the first switch inside
	// the window moved away from.
	start := ""
	for _, sp := range spans {
		if sp.at.After(from) {
			break
		}
		start = sp.account
	}
	if start == "" {
		for _, e := range events {
			if e.Kind == "switch" && e.At.After(from) && e.From != "" {
				start = e.From
				break
			}
		}
	}
	if start == "" {
		start = active
	}

	out := []span{{at: from, account: start}}
	for _, sp := range spans {
		if sp.at.After(from) {
			out = append(out, sp)
		}
	}
	return out
}

func accountAt(timeline []span, t time.Time) string {
	acct := ""
	for _, sp := range timeline {
		if sp.at.After(t) {
			break
		}
		acct = sp.account
	}
	return acct
}

// record is the subset of a transcript line we need.
type record struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			Details       struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
	RequestID string `json:"requestId"`
}

// Build assembles the session covering the given window.
func Build(root string, from, to time.Time, active string) (*Session, error) {
	if root == "" {
		root = detector.ProjectsRoot()
	}
	events, err := audit.Tail("", 100000)
	if err != nil {
		events = nil // an unreadable audit log costs attribution, not the whole report
	}
	timeline := buildTimeline(events, from, active)

	s := &Session{From: from, To: to}
	for _, e := range events {
		if e.Kind == "switch" && !e.At.Before(from) && !e.At.After(to) {
			s.Switches = append(s.Switches, e)
		}
	}

	byAccount := map[string]*Share{}
	seen := map[string]bool{}

	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.ModTime().Before(from) {
			return nil // nothing in this file can be inside the window
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if !strings.Contains(string(line), `"usage"`) {
				continue
			}
			var r record
			if json.Unmarshal(line, &r) != nil || r.Message.Usage == nil || r.Timestamp == "" {
				continue
			}
			t, perr := time.Parse(time.RFC3339, r.Timestamp)
			if perr != nil || t.Before(from) || t.After(to) {
				continue
			}
			// The same request can appear more than once in a transcript.
			if r.RequestID != "" {
				if seen[r.RequestID] {
					continue
				}
				seen[r.RequestID] = true
			}

			u := r.Message.Usage
			tok := Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead,
				CacheCreation: u.CacheCreation, Thinking: u.Details.Thinking}

			s.Total.add(tok)
			s.Messages++

			acct := accountAt(timeline, t)
			if acct == "" {
				s.Unattributed.add(tok)
				s.UnattrCount++
				continue
			}
			sh := byAccount[acct]
			if sh == nil {
				sh = &Share{Account: acct, Models: map[string]int{}}
				byAccount[acct] = sh
			}
			sh.Tokens.add(tok)
			sh.Messages++
			if m := r.Message.Model; m != "" && m != "<synthetic>" {
				sh.Models[m]++
			}
		}
		return nil
	})

	// How long each account was the active one, from the timeline.
	for i, sp := range timeline {
		end := to
		if i+1 < len(timeline) {
			end = timeline[i+1].at
		}
		if end.After(to) {
			end = to
		}
		if d := end.Sub(sp.at); d > 0 {
			sh := byAccount[sp.account]
			if sh == nil {
				sh = &Share{Account: sp.account, Models: map[string]int{}}
				byAccount[sp.account] = sh
			}
			sh.Active += d
		}
	}

	for _, sh := range byAccount {
		s.Shares = append(s.Shares, *sh)
	}
	sort.Slice(s.Shares, func(i, j int) bool {
		return s.Shares[i].Tokens.Total() > s.Shares[j].Tokens.Total()
	})
	return s, err
}
