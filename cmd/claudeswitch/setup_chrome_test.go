package main

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// setup offers each account a Chrome profile of its own (P2), the setup in
// which Claude in Chrome follows rotation by itself. Offered, not assumed,
// and only for accounts that have none yet.
func TestSetupOffersEachAccountItsOwnChromeProfile(t *testing.T) {
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetChromeExisting("a", "Profile 2", time.Now())
	cfg := &config.Config{Accounts: []config.Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	for _, c := range []struct {
		yes  bool
		want []string
	}{{true, []string{"b", "c"}}, {false, nil}} {
		var asked int
		var added []string
		var out bytes.Buffer
		offerChromePerAccount(&out, cfg, st, func(string, bool) bool { asked++; return c.yes },
			func(id string) error { added = append(added, id); return nil })
		if asked != 1 || !reflect.DeepEqual(added, c.want) {
			t.Errorf("answer %v: asked %d times, added %v, want %v", c.yes, asked, added, c.want)
		}
	}
	// One account has nothing to rotate between: not asked.
	var asked int
	offerChromePerAccount(&bytes.Buffer{}, &config.Config{Accounts: []config.Account{{ID: "a"}}}, st,
		func(string, bool) bool { asked++; return true }, func(string) error { return nil })
	if asked != 0 {
		t.Error("asked with one account")
	}
}
