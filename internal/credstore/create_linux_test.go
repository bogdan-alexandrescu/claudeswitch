//go:build linux

package credstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// On Linux a profile's live credential is <dir>/.credentials.json. Seeding
// creates it 0600 and never replaces one that is there.
func TestCreateLiveItemCreatesTheFileOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude-work")
	file := filepath.Join(dir, ".credentials.json")
	if ok, err := LiveExistsFor(dir); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	if err := CreateLiveItem(LiveServiceName(dir), file, &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "first"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if ok, err := LiveExistsFor(dir); err != nil || !ok {
		t.Fatalf("after: %v %v", ok, err)
	}
	err = CreateLiveItem(LiveServiceName(dir), file, &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "second"}})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	b, err := readAt("x", file)
	if err != nil || b.ClaudeAIOAuth.AccessToken != "first" {
		t.Fatalf("the existing credential changed: %+v %v", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestCreateLiveItemNeedsTheFile(t *testing.T) {
	if err := CreateLiveItem(LiveServiceName("/x"), "", &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "t"}}); err == nil {
		t.Fatal("no file named: refuse rather than fall back to this environment's")
	}
}
