package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func captureLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// 2026-10-07: an account added while the daemon ran was invisible to it until a
// restart, because the config was read once at startup.
func TestReloadPicksUpAnAddedAccount(t *testing.T) {
	path := writeConfig(t, baseConfig)
	log, buf := captureLog()
	r := newConfigReloader(path, loadOrFail(t, path), log)

	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	next, changes := r.reload()
	if !hasAccount(next, "work-b") {
		t.Fatal("the reloaded config must contain the new account")
	}
	if len(changes) == 0 || !strings.Contains(strings.Join(changes, "\n"), "work-b") {
		t.Errorf("the change must be described, got %v", changes)
	}
	if !strings.Contains(buf.String(), "work-b") {
		t.Errorf("the reload must be logged with what changed:\n%s", buf)
	}
}

func TestReloadDescribesRemovedAccountsAndThresholds(t *testing.T) {
	path := writeConfig(t, baseConfig)
	log, _ := captureLog()
	r := newConfigReloader(path, loadOrFail(t, path), log)

	edited := strings.Replace(baseConfig, "switch_at        = 85", "switch_at        = 80", 1)
	edited = strings.Replace(edited, `priority = ["work-a", "personal"]`, `priority = ["work-a"]`, 1)
	edited = edited[:strings.LastIndex(edited, "[[account]]")]
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changes := r.reload()
	all := strings.Join(changes, "\n")
	for _, want := range []string{"personal", "removed", "switch_at", "85", "80"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %v", want, changes)
		}
	}
}

// A half-saved or mistyped config must not take the daemon down or leave it
// with no accounts: it keeps what it had, and says why — once, not on every
// write the editor makes while the mistake is still there.
func TestReloadKeepsThePreviousConfigWhenTheNewOneIsBroken(t *testing.T) {
	path := writeConfig(t, baseConfig)
	log, buf := captureLog()
	cur := loadOrFail(t, path)
	r := newConfigReloader(path, cur, log)

	if err := os.WriteFile(path, []byte("switch_at = [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, changes := r.reload()
	if next != cur || changes != nil {
		t.Fatalf("a broken config must leave the previous one in force, got %v / %v", next, changes)
	}
	_, _ = r.reload()
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Errorf("the same error must be warned about once, got %d:\n%s", n, buf)
	}

	if err := os.WriteFile(path, []byte("switch_at = 150\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = r.reload()
	if n := strings.Count(buf.String(), "level=WARN"); n != 2 {
		t.Errorf("a different error is news and must be warned about, got %d:\n%s", n, buf)
	}

	// Fixed again: back in business, and a later identical mistake is news.
	if err := os.WriteFile(path, []byte(baseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if next, _ := r.reload(); next == nil || !hasAccount(next, "work-a") {
		t.Fatal("a repaired config must load")
	}
}

func TestReloadOfAnUnchangedConfigSaysNothing(t *testing.T) {
	path := writeConfig(t, baseConfig)
	log, buf := captureLog()
	r := newConfigReloader(path, loadOrFail(t, path), log)
	if _, changes := r.reload(); len(changes) != 0 {
		t.Errorf("nothing changed, got %v", changes)
	}
	if strings.Contains(buf.String(), "level=INFO") {
		t.Errorf("an unchanged reload must not be logged:\n%s", buf)
	}
}

// Editors save in bursts (write, chmod, rename), and appendAccount replaces
// the file by rename. One reload per save, not one per event.
func TestWatchConfigSignalsOnceForABurstOfWrites(t *testing.T) {
	path := writeConfig(t, baseConfig)
	stop := make(chan struct{})
	defer close(stop)
	log, _ := captureLog()
	changed, err := watchConfig(path, 100*time.Millisecond, stop, log)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(path, []byte(baseConfig+"\n"), 0o600)
		time.Sleep(10 * time.Millisecond)
	}
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("a change to the config must be signalled")
	}
	select {
	case <-changed:
		t.Fatal("one burst must produce one signal")
	case <-time.After(400 * time.Millisecond):
	}

	// And it keeps watching after the file was replaced by rename.
	_ = os.WriteFile(path, []byte(baseConfig), 0o600)
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("the watch must survive the file being replaced")
	}
}
