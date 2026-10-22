package detector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// IMPROVEMENTS P4 (owner decision 2026-10-21): a rotation in the middle of a
// browser task breaks Claude in Chrome's bridge, so the detector notes, for
// each session, when it last called a Claude in Chrome tool, and the daemon
// holds a rotation while that is recent. Phase 1 H (owner decision
// 2026-10-21) counts artifact and Docs calls too: each acts as the account
// live when it is made, so a rotation between two of them splits the work
// across accounts (GROUND_TRUTH §55).

// stamp is a transcript record's timestamp, as Claude Code writes it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// toolUseLine is an assistant record calling the named tool, shaped like the
// real one: the call sits in message.content beside the turn's text.
func toolUseLine(at time.Time, name string) string {
	return fmt.Sprintf(`{"parentUuid":"p","type":"assistant","timestamp":%q,"sessionId":"s","cwd":"/work",`+
		`"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Opening it."},`+
		`{"type":"tool_use","id":"toolu_01","name":%q,"input":{"url":"https://example.com"}}]}}`,
		stamp(at), name)
}

func writeSession(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestABrowserCallIsRecordedForItsSessionAtItsOwnTime(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-someone-project")
	first := time.Now().Add(-4 * time.Minute).UTC().Truncate(time.Millisecond)
	second := time.Now().Add(-90 * time.Second).UTC().Truncate(time.Millisecond)
	a := writeSession(t, proj, "a.jsonl",
		toolUseLine(first, "mcp__claude-in-chrome__navigate"),
		toolUseLine(first.Add(time.Minute), "Bash")) // a later call to another tool changes nothing
	b := writeSession(t, proj, "b.jsonl", toolUseLine(second, "mcp__claude-in-chrome__computer"))

	d := New(root, quietLog())
	if at := d.LastClaudeAICall(); !at.IsZero() {
		t.Fatalf("LastClaudeAICall %v before any transcript was read, want zero", at)
	}
	d.scanFile(a)
	if at := d.LastClaudeAICall(); !at.Equal(first) {
		t.Fatalf("LastClaudeAICall %v, want the record's own time %v", at, first)
	}
	d.scanFile(b)
	if at := d.LastClaudeAICall(); !at.Equal(second) {
		t.Fatalf("LastClaudeAICall %v, want the newest session's call %v", at, second)
	}

	d.mu.Lock()
	n, atA, atB := len(d.claudeAI), d.claudeAI[a], d.claudeAI[b]
	d.mu.Unlock()
	if n != 2 || !atA.Equal(first) || !atB.Equal(second) {
		t.Errorf("per session: %d sessions, a %v, b %v; want a %v and b %v", n, atA, atB, first, second)
	}
}

// Only a tool_use of a Claude in Chrome tool counts. Its name turns up in
// many other records: the tool list a session is handed, a person's message,
// a command's text, the tool's own result.
func TestOnlyAClaudeInChromeToolUseCounts(t *testing.T) {
	root := t.TempDir()
	at := stamp(time.Now().Add(-time.Minute))
	p := writeSession(t, filepath.Join(root, "p"), "s.jsonl",
		`{"type":"system","timestamp":"`+at+`","tools":[{"name":"mcp__claude-in-chrome__navigate","description":"Open a URL"}]}`,
		`{"type":"user","timestamp":"`+at+`","message":{"role":"user","content":"use mcp__claude-in-chrome__navigate to open it"}}`,
		`{"type":"attachment","timestamp":"`+at+`","attachment":{"type":"deferred_tools","content":"mcp__claude-in-chrome__computer\nmcp__claude-in-chrome__find"}}`,
		toolUseLine(time.Now().Add(-time.Minute), "Bash"),
		`{"type":"assistant","timestamp":"`+at+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_02","name":"Bash","input":{"command":"echo \"name\":\"mcp__claude-in-chrome__navigate\""}}]}}`,
		`{"type":"user","timestamp":"`+at+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"text","text":"Navigated."}]}]}}`,
		toolUseLine(time.Now().Add(-time.Minute), "mcp__claude-in-chromium__navigate"),
		// A copy of a message sent to the API: the same call, no time of its
		// own. Read as a call made now, an old one would hold a rotation.
		`{"type":"api-request-blob","hash":"h","sessionId":"s","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_03","name":"mcp__claude-in-chrome__navigate","input":{}}]}}`,
	)
	d := New(root, quietLog())
	d.scanFile(p)
	if at := d.LastClaudeAICall(); !at.IsZero() {
		t.Fatalf("LastClaudeAICall %v from records that call no Claude in Chrome tool, want zero", at)
	}
}

// A clock that runs ahead must not stretch the hold: a call stamped in the
// future counts as made now. A record with no time of its own counts as made
// when it was read.
func TestABrowserCallStampedAheadOrNotAtAllCountsAsNow(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	ahead := writeSession(t, proj, "ahead.jsonl", toolUseLine(time.Now().Add(time.Hour), "mcp__claude-in-chrome__navigate"))
	bare := writeSession(t, proj, "bare.jsonl",
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"mcp__claude-in-chrome__find","input":{}}]}}`)

	for _, p := range []string{ahead, bare} {
		d := New(root, quietLog())
		before := time.Now()
		d.scanFile(p)
		after := time.Now()
		if at := d.LastClaudeAICall(); at.Before(before) || at.After(after) {
			t.Errorf("%s: LastClaudeAICall %v, want the time it was read (%v to %v)", filepath.Base(p), at, before, after)
		}
	}
}

// Starting up reads no transcript for browser calls: the startup walk stays
// one walk that opens nothing but the newest file (GROUND_TRUTH §50). A call
// made before the daemon started is carried across a restart by the daemon's
// own state, not found again here.
func TestStartupReadsNoTranscriptForBrowserCalls(t *testing.T) {
	root := t.TempDir()
	writeSession(t, filepath.Join(root, "p"), "s.jsonl", toolUseLine(time.Now().Add(-time.Minute), "mcp__claude-in-chrome__navigate"))
	d := New(root, quietLog())
	stop := make(chan struct{})
	defer close(stop)
	go func() { _ = d.Run(stop) }()
	deadline := time.Now().Add(2 * time.Second)
	for !d.Seeded() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !d.Seeded() {
		t.Fatal("never seeded")
	}
	if at := d.LastClaudeAICall(); !at.IsZero() {
		t.Fatalf("LastClaudeAICall %v after startup, want zero: history is not replayed", at)
	}
}

// The Artifact tools and the Claude Docs connector are claude.ai work as much
// as the browser is; other tools, claude.ai's other connectors among them,
// are not.
func TestArtifactAndDocsCallsCountAsClaudeAIWork(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	for i, name := range []string{"Artifact", "ArtifactComments", "ArtifactData",
		"mcp__claude_ai_Claude_Docs__batch", "mcp__claude_ai_Claude_Docs__read"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Minute).UTC().Truncate(time.Millisecond)
		p := writeSession(t, proj, fmt.Sprintf("s%d.jsonl", i), toolUseLine(at, name))
		d := New(root, quietLog())
		d.scanFile(p)
		if got := d.LastClaudeAICall(); !got.Equal(at) {
			t.Errorf("%s: LastClaudeAICall %v, want %v", name, got, at)
		}
	}
	for i, name := range []string{"Artifacts", "ArtifactX", "mcp__claude_ai_Notion__search",
		"mcp__claude_ai_Claude_Docsx__read", "Agent"} {
		p := writeSession(t, proj, fmt.Sprintf("n%d.jsonl", i), toolUseLine(time.Now().Add(-time.Minute), name))
		d := New(root, quietLog())
		d.scanFile(p)
		if got := d.LastClaudeAICall(); !got.IsZero() {
			t.Errorf("%s counted as claude.ai work (%v)", name, got)
		}
	}
}
