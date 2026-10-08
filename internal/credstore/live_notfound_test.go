package credstore

import (
	"errors"
	"testing"
)

// The daemon and the CLI treat a missing item as "holds nothing" and any other
// failure as "unknown" (D18). That split rests on LiveServiceFor's errors: no
// item under any spelling is ErrNotFound; a lookup that did not answer is not.
func TestLiveServiceForNotFoundMatchesErrNotFound(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "")
	_, err := LiveServiceFor("~/.claude-work")
	if err == nil {
		t.Fatal("no item under any spelling must be an error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("no item under any spelling = %v, want it to match ErrNotFound", err)
	}
}

func TestLiveServiceForLookupTimeoutIsNotErrNotFound(t *testing.T) {
	f := &fakeLookup{err: ErrUnavailable}
	f.install(t)
	liveEnv(t, "/h", "")
	_, err := LiveServiceFor("~/.claude-work")
	if err == nil {
		t.Fatal("a lookup that timed out must be an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("a lookup timeout matched ErrNotFound (%v); it says nothing about the item", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a lookup timeout = %v, want ErrUnavailable", err)
	}
}
