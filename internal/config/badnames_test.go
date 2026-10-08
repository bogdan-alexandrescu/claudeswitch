package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Lane 7 review (owner decision: strict, with a working rename): a config
// whose ids break the name rule does not load, and the error names each bad
// id with the exact command that fixes it.
func TestLoadNamesEachBadIDAndItsFix(t *testing.T) {
	body := "priority = [\"me+work\", \"ok\"]\n\n[[account]]\nid = \"me+work\"\n\n[[account]]\nid = \"ok\"\n\n" +
		"[[account]]\nid = \"x@y\"\n"
	_, err := loadString(t, body)
	if err == nil {
		t.Fatal("a config with bad ids loaded")
	}
	for _, want := range []string{`"me+work"`, "cs rename me+work <new-id>", `"x@y"`, "cs rename x@y <new-id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	var ne *NameError
	if !errors.As(err, &ne) || len(ne.Bad) != 2 {
		t.Errorf("want a NameError naming both ids, got %#v", err)
	}
}

// rename must be able to load a config whose only fault is its names.
func TestLoadAllowingBadNamesLoadsWhatOnlyNamesBreak(t *testing.T) {
	body := "priority = [\"me+work\"]\n\n[[account]]\nid = \"me+work\"\n"
	c, err := loadAllowing(t, body)
	if c == nil {
		t.Fatalf("not loaded: %v", err)
	}
	var ne *NameError
	if !errors.As(err, &ne) {
		t.Errorf("the name fault must still be reported: %v", err)
	}
	if len(c.Accounts) != 1 || c.Accounts[0].ID != "me+work" {
		t.Errorf("accounts %+v", c.Accounts)
	}
	// Any other fault still refuses.
	if c, _ := loadAllowing(t, "switch_at = 200\n"+body); c != nil {
		t.Error("a config with a real fault besides its names loaded")
	}
	// A clean config: no error.
	if c, err := loadAllowing(t, "[[account]]\nid = \"fine\"\n"); c == nil || err != nil {
		t.Errorf("clean config: %v", err)
	}
}

func loadAllowing(t *testing.T, body string) (*Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := t.TempDir() + "/config.toml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadAllowingBadNames(path)
}
