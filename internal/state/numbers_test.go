package state

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func numbersOf(t *testing.T, ids ...string) map[string]int {
	t.Helper()
	got, err := AccountNumbers(ids)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAccountNumbersAreAssignedInOrderAndKept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := numbersOf(t, "b", "a")
	if got["b"] != 1 || got["a"] != 2 {
		t.Fatalf("first numbering = %v, want b=1 a=2", got)
	}
	// Another order, and a newcomer: nobody moves, the newcomer goes last.
	got = numbersOf(t, "c", "a", "b")
	if got["b"] != 1 || got["a"] != 2 || got["c"] != 3 {
		t.Fatalf("second numbering = %v, want b=1 a=2 c=3", got)
	}
}

func TestAccountNumbersAnswerOnlyForTheIdsAsked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	numbersOf(t, "a", "gone")
	if got := numbersOf(t, "a"); len(got) != 1 || got["a"] != 1 {
		t.Fatalf("got %v, want only a=1", got)
	}
}

func TestAccountNumberOfADeletedAccountIsNotReused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	numbersOf(t, "a", "b")
	if err := ForgetAccountNumber("b"); err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(t, "a", "c"); got["c"] != 3 {
		t.Fatalf("got %v, want c=3: b's number is retired", got)
	}
	// An account added back under the old name is a new account.
	if got := numbersOf(t, "b"); got["b"] != 4 {
		t.Fatalf("got %v, want b=4", got)
	}
}

func TestRenameKeepsTheAccountNumber(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	numbersOf(t, "a", "b")
	if err := RenameAccountNumber("a", "z"); err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(t, "z", "b"); got["z"] != 1 || got["b"] != 2 {
		t.Fatalf("got %v, want z=1 b=2", got)
	}
	// A rename being finished a second time has nothing left to move.
	if err := RenameAccountNumber("a", "z"); err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(t, "z"); got["z"] != 1 {
		t.Fatalf("got %v, want z=1 after the repeat", got)
	}
}

// Numbers are promises: a book that cannot be read is reported, never started
// again, which would hand every account a new one.
func TestUnreadableNumberBookIsLeftAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	numbersOf(t, "a")
	if err := os.WriteFile(AccountNumbersPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AccountNumbers([]string{"a", "b"}); err == nil {
		t.Fatal("no error for an unreadable book")
	}
	b, err := os.ReadFile(AccountNumbersPath())
	if err != nil || string(b) != "{not json" {
		t.Fatalf("book was changed: %q, %v", b, err)
	}
}

func TestConcurrentNumberingNeverGivesOneNumberTwice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const n = 24
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := AccountNumbers([]string{fmt.Sprintf("acct-%d", i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	ids := make([]string, 0, n)
	for i := range n {
		ids = append(ids, fmt.Sprintf("acct-%d", i))
	}
	seen := map[int]string{}
	for id, num := range numbersOf(t, ids...) {
		if other, dup := seen[num]; dup {
			t.Fatalf("number %d given to both %s and %s", num, id, other)
		}
		seen[num] = id
	}
	if len(seen) != n {
		t.Fatalf("%d numbers for %d accounts", len(seen), n)
	}
}
