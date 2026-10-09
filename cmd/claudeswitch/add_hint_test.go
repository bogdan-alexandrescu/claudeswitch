package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// `add` names what is live; it does not sign in. Someone who runs it hoping to
// add a new account meets the duplicate refusal for the account they are already
// on, and the refusal has to say how to get the account they wanted
// (2026-10-07: `cs add lab-1` while signed in as personal).
func TestAddPointsToLoginWhenTheLiveAccountIsAlreadyVaulted(t *testing.T) {
	err := addError("lab-1", fmt.Errorf("store: %w",
		&vault.DuplicateSeatError{Other: "personal", Who: "someone@example.com"}))
	msg := err.Error()
	for _, want := range []string{`"personal"`, "saves whatever Claude Code is signed in to", "cs login lab-1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q:\n%s", want, msg)
		}
	}
}

func TestAddLeavesOtherErrorsAlone(t *testing.T) {
	orig := errors.New("keychain unavailable")
	if got := addError("x", orig); got != orig {
		t.Errorf("unrelated errors should pass through unchanged, got %v", got)
	}
	if addError("x", nil) != nil {
		t.Error("nil stays nil")
	}
}
