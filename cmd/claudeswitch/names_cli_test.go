package main

import (
	"strings"
	"testing"
)

// Lane 7: an account id becomes a keychain item name and a config line, so a
// name the config would refuse is refused before anything is vaulted — not
// after, when the vault entry exists and the config edit fails.
func TestAddRefusesANameTheConfigWouldNotLoad(t *testing.T) {
	for _, bad := range []string{"a b", "x\ny", "-x", "../up"} {
		_, err := chooseAddName([]string{bad}, false, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "account id") {
			t.Errorf("add accepted %q: %v", bad, err)
		}
	}
	got, err := chooseAddName([]string{"work-2"}, false, nil, nil, nil)
	if err != nil || got != "work-2" {
		t.Errorf("a plain name: %q, %v", got, err)
	}
}

func TestLoginRefusesANameTheConfigWouldNotLoad(t *testing.T) {
	if err := checkNewAccountID("x\ny"); err == nil || !strings.Contains(err.Error(), "account id") {
		t.Errorf("login accepted a name with a newline: %v", err)
	}
	if err := checkNewAccountID("work-2"); err != nil {
		t.Errorf("login refused a plain name: %v", err)
	}
}

// A suggested name typed in at the prompt is checked the same way.
func TestAddRefusesATypedNameTheConfigWouldNotLoad(t *testing.T) {
	_, err := chooseAddName(nil, true, liveIs("jane@acme.io", "Acme", "org-1"), nil,
		func(string, string) string { return "jane at acme" })
	if err == nil || !strings.Contains(err.Error(), "account id") {
		t.Errorf("a typed name with spaces was accepted: %v", err)
	}
}
