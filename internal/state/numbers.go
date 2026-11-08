package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// An account's number is the short name `cs list` shows and `cs switch 2`
// takes, as a pm2 process id is. It is this machine's: another machine may
// number the same account differently. It lasts as long as the account does:
// a rename keeps it, the order of the rotation does not touch it, and once an
// account is deleted its number is never given out again, so a number typed
// from memory cannot land on some other account.

const accountNumbersLockName = "account-numbers.lock"

// AccountNumbersPath is the book's file, beside state.json. A file of its own
// so that handing out a number never waits on, or races, the daemon's saves.
func AccountNumbersPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "account-numbers.json")
}

type numberBook struct {
	Version int            `json:"version"`
	Next    int            `json:"next"`
	Numbers map[string]int `json:"numbers"`
}

func readNumberBook() (*numberBook, error) {
	book := &numberBook{Version: 1, Next: 1, Numbers: map[string]int{}}
	b, err := os.ReadFile(AccountNumbersPath())
	if errors.Is(err, os.ErrNotExist) {
		return book, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, book); err != nil {
		return nil, fmt.Errorf("%s: %w", AccountNumbersPath(), err)
	}
	if book.Numbers == nil {
		book.Numbers = map[string]int{}
	}
	// However the file came to be, the next number is a new one.
	for _, n := range book.Numbers {
		if n >= book.Next {
			book.Next = n + 1
		}
	}
	if book.Next < 1 {
		book.Next = 1
	}
	return book, nil
}

// changeNumberBook applies change under the book's lock, and writes the book
// when change says it changed anything. An unreadable book stops here and is
// left as it is: starting it again would renumber every account.
func changeNumberBook(change func(*numberBook) bool) error {
	lk, err := blockingLock(lockPath(accountNumbersLockName))
	if err != nil {
		return err
	}
	defer lk.Release()
	book, err := readNumberBook()
	if err != nil {
		return err
	}
	if !change(book) {
		return nil
	}
	b, err := json.MarshalIndent(book, "", "  ")
	if err != nil {
		return err
	}
	path := AccountNumbersPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (b *numberBook) of(ids []string) map[string]int {
	out := make(map[string]int, len(ids))
	for _, id := range ids {
		if n, ok := b.Numbers[id]; ok {
			out[id] = n
		}
	}
	return out
}

// AccountNumbers is the number of each of ids, handing the next free number,
// in the order given, to any that has none yet.
func AccountNumbers(ids []string) (map[string]int, error) {
	// The book is replaced whole, so a read needs no lock, and the usual
	// call, with nothing to hand out, takes none.
	book, err := readNumberBook()
	if err != nil {
		return nil, err
	}
	if out := book.of(ids); len(out) == len(unique(ids)) {
		return out, nil
	}
	var out map[string]int
	err = changeNumberBook(func(b *numberBook) bool {
		changed := false
		for _, id := range ids {
			if _, ok := b.Numbers[id]; !ok {
				b.Numbers[id] = b.Next
				b.Next++
				changed = true
			}
		}
		out = b.of(ids)
		return changed
	})
	return out, err
}

func unique(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// RenameAccountNumber moves a number to an account's new id. Nothing to move
// is not an error: a rename can be finished by running it again.
func RenameAccountNumber(oldID, newID string) error {
	return changeNumberBook(func(b *numberBook) bool {
		n, ok := b.Numbers[oldID]
		if !ok {
			return false
		}
		delete(b.Numbers, oldID)
		b.Numbers[newID] = n
		return true
	})
}

// ForgetAccountNumber retires a deleted account's number.
func ForgetAccountNumber(id string) error {
	return changeNumberBook(func(b *numberBook) bool {
		if _, ok := b.Numbers[id]; !ok {
			return false
		}
		delete(b.Numbers, id)
		return true
	})
}
