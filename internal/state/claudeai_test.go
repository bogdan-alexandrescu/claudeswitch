package state

import (
	"os"
	"strings"
	"testing"
	"time"
)

// IMPROVEMENTS P4 and phase 1 H: the daemon records each profile's last
// claude.ai call (Claude in Chrome, an artifact, a Doc), so `cs why` can say
// a rotation is waiting for that work and a restarted daemon keeps holding
// for it. state.json calls it claude_ai_at.

func TestTheLastClaudeAICallSurvivesASaveAndLoad(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	call := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	daemon.Profile("default").ClaudeAIAt = call
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.Profile("default").ClaudeAIAt; !got.Equal(call) {
		t.Fatalf("claude_ai_at read back as %v, want %v", got, call)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"claude_ai_at"`) {
		t.Fatalf("state.json lacks claude_ai_at:\n%s", raw)
	}
}

// The daemon owns it: a command that loaded state before the daemon's last
// save, and saves after it, keeps the daemon's newer call.
func TestACommandSaveKeepsTheDaemonsLastClaudeAICall(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli, _ := Load(path, "default")

	call := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	daemon.Profile("default").ClaudeAIAt = call
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli.Profile("default").Pinned = "work-1"
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.Profile("default").ClaudeAIAt; !got.Equal(call) {
		t.Fatalf("after a command's save claude_ai_at = %v, want the daemon's %v", got, call)
	}
}
