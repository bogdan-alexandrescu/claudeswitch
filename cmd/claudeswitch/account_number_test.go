package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// listNumbers is `account list --json` as id → number.
func listNumbers(t *testing.T, path string) map[string]int {
	t.Helper()
	out, _, code := runCLISplit(t, "account", "list", "--json", "--config", path)
	if code != 0 {
		t.Fatalf("account list --json: exit %d\n%s", code, out)
	}
	var doc struct {
		Accounts []struct {
			ID     string `json:"id"`
			Number int    `json:"number"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := map[string]int{}
	for _, a := range doc.Accounts {
		got[a.ID] = a.Number
	}
	return got
}

func TestListNumbersAccountsInItsOwnOrder(t *testing.T) {
	path := appWorld(t, sharedTOML)
	got := listNumbers(t, path)
	for i, id := range []string{"p1", "t1", "t2", "s1", "loose"} {
		if got[id] != i+1 {
			t.Errorf("%s = %d, want %d (all: %v)", id, got[id], i+1, got)
		}
	}
	out, code := runCLI(t, "list", "--config", path)
	if code != 0 {
		t.Fatalf("list: exit %d\n%s", code, out)
	}
	if !regexp.MustCompile(`(?m)^\s+#\s+ACCOUNT\b`).MatchString(out) {
		t.Errorf("no # column:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^\s+2\s+t1\b`).MatchString(out) {
		t.Errorf("t1 is not row 2:\n%s", out)
	}
}

func TestSwitchTakesAnAccountNumber(t *testing.T) {
	path := appWorld(t, sharedTOML)
	listNumbers(t, path)
	for _, cmd := range []string{"use", "switch"} {
		want, wantCode := runCLI(t, cmd, "t1", "--profile", "default", "--dry-run", "--config", path)
		got, code := runCLI(t, cmd, "2", "--profile", "default", "--dry-run", "--config", path)
		if code != wantCode || got != want {
			t.Errorf("%s 2: exit %d, want %d\n%s\nwant:\n%s", cmd, code, wantCode, got, want)
		}
	}
}

// Nothing has to run `cs list` first.
func TestANumberWorksBeforeTheListHasBeenSeen(t *testing.T) {
	path := appWorld(t, sharedTOML)
	want, _ := runCLI(t, "use", "p1", "--dry-run", "--config", path)
	got, _ := runCLI(t, "use", "1", "--dry-run", "--config", path)
	if got != want {
		t.Errorf("use 1:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnUnknownNumberSaysSo(t *testing.T) {
	path := appWorld(t, sharedTOML)
	out, code := runCLI(t, "switch", "99", "--config", path)
	if code == 0 || !strings.Contains(out, "no account numbered 99") {
		t.Errorf("switch 99: exit %d\n%s", code, out)
	}
}

// An account really called "2" has always been reachable by that name.
func TestAnExactNameBeatsANumber(t *testing.T) {
	path := appWorld(t, `priority = ["first", "2"]
[[account]]
id = "first"
[[account]]
id = "2"
`)
	listNumbers(t, path)
	want, _ := runCLI(t, "use", "2", "--dry-run", "--config", path)
	if strings.Contains(want, "first") || !strings.Contains(want, `"2"`) {
		t.Errorf("use 2 did not mean the account named 2:\n%s", want)
	}
}

func TestRenameKeepsAnAccountsNumberInTheList(t *testing.T) {
	path := appWorld(t, sharedTOML)
	listNumbers(t, path)
	if out, code := runCLI(t, "account", "rename", "t1", "renamed", "--config", path); code != 0 {
		t.Fatalf("rename: exit %d\n%s", code, out)
	}
	if got := listNumbers(t, path); got["renamed"] != 2 || got["t2"] != 3 {
		t.Errorf("after rename: %v, want renamed=2 t2=3", got)
	}
}

func TestDeleteRetiresAnAccountsNumber(t *testing.T) {
	path := appWorld(t, sharedTOML)
	if got := listNumbers(t, path); got["loose"] != 5 {
		t.Fatalf("loose = %d, want 5", got["loose"])
	}
	if out, code := runCLI(t, "account", "delete", "loose", "--yes", "--config", path); code != 0 {
		t.Fatalf("delete: exit %d\n%s", code, out)
	}
	got, err := state.AccountNumbers([]string{"loose"})
	if err != nil {
		t.Fatal(err)
	}
	if got["loose"] != 6 {
		t.Errorf("loose = %d after its delete, want a new number, 6", got["loose"])
	}
}
