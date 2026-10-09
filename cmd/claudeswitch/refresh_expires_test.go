package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// IMPROVEMENTS F5: refresh_expires_at is what state.json recorded (UTC,
// RFC 3339), null when nothing was, and never read from the keychain.
func TestAccountListRefreshExpiresAt(t *testing.T) {
	path := appWorld(t, listTOML)
	keychainTripwires(t)
	at := time.Date(2026, 10, 12, 9, 30, 0, 0, time.FixedZone("x", 2*3600))
	st, _ := state.Load("", "default", "work")
	st.Get("w1").RefreshExpiry = at
	st.Get("a1").LastErr = "" // a record with no expiry
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	before := testshim.Invocations()
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	if after := testshim.Invocations(); after != before {
		t.Fatalf("account list ran a forbidden binary")
	}
	got := map[string]any{}
	for _, r := range decodeJSON(t, buf.Bytes())["accounts"].([]any) {
		a := r.(map[string]any)
		got[a["id"].(string)] = a["refresh_expires_at"]
	}
	if got["w1"] != "2026-10-12T07:30:00Z" {
		t.Errorf("w1 refresh_expires_at = %v", got["w1"])
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if v, ok := got[id]; !ok || v != nil {
			t.Errorf("%s refresh_expires_at = %v (present %v), want null", id, v, ok)
		}
	}
}
