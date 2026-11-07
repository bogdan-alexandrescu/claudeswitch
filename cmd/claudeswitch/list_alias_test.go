package main

import (
	"strings"
	"testing"
)

// `cs list` is `cs account list`: the table people reach for most.
func TestListIsAccountList(t *testing.T) {
	path := appWorld(t, sharedTOML)
	t.Setenv("COLUMNS", "120")
	want, code := runCLI(t, "account", "list", "--config", path)
	if code != 0 {
		t.Fatalf("account list: exit %d\n%s", code, want)
	}
	for _, args := range [][]string{{"list", "--config", path}, {"list", "--config", path, "--json"}} {
		got, code := runCLI(t, args...)
		if code != 0 {
			t.Errorf("%v: exit %d\n%s", args, code, got)
		}
		if len(args) == 3 && got != want {
			t.Errorf("list differs from account list:\n%s\nwant:\n%s", got, want)
		}
		if len(args) == 4 && !strings.Contains(got, `"accounts"`) {
			t.Errorf("list --json:\n%s", got)
		}
	}
	out, _, _ := runCLISplit(t, "help")
	if !strings.Contains(out, "\n  list ") {
		t.Errorf("help does not list list:\n%s", out)
	}
	out, _, code = runCLISplit(t, "list", "--help")
	if code != 0 || !strings.Contains(out, "usage") {
		t.Errorf("list --help: exit %d\n%s", code, out)
	}
}
