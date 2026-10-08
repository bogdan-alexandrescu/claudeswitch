package detector

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// A freshly created profile has no <dir>/projects until Claude Code first
// runs there. That is normal, and the minute rescan must not warn about it.
func TestAddWatchesIsQuietAboutAMissingRoot(t *testing.T) {
	var logs bytes.Buffer
	d := New(filepath.Join(t.TempDir(), "projects"), slog.New(slog.NewTextHandler(&logs, nil)))
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 3; i++ {
		d.addWatches(w)
	}
	if strings.Contains(logs.String(), "WARN") {
		t.Fatalf("a missing projects dir warned:\n%s", logs.String())
	}

	// Once it appears its project dirs are watched.
	if err := os.MkdirAll(filepath.Join(d.root, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.addWatches(w)
	if got := w.WatchList(); len(got) != 2 {
		t.Errorf("watching %q, want the root and p1", got)
	}
}

// Any other failure to read the root still warns.
func TestAddWatchesWarnsWhenTheRootIsUnreadable(t *testing.T) {
	var logs bytes.Buffer
	root := filepath.Join(t.TempDir(), "projects")
	if err := os.WriteFile(root, nil, 0o600); err != nil { // a file, not a dir
		t.Fatal(err)
	}
	d := New(root, slog.New(slog.NewTextHandler(&logs, nil)))
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	d.addWatches(w)
	if !strings.Contains(logs.String(), "cannot read transcripts root") {
		t.Fatalf("an unreadable root did not warn:\n%s", logs.String())
	}
}
