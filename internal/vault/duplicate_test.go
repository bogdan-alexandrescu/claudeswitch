package vault

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The duplicate refusal is a type, so a caller that knows how the credential got
// there can say what to do next. `add` saves whatever is live; `login` signed in
// to something specific. Only the caller knows which.
func TestADuplicateSeatIsATypedErrorThatStillReadsTheSame(t *testing.T) {
	var err error = &DuplicateSeatError{Other: "personal", Who: "someone@example.com in Their Organization"}
	wrapped := fmt.Errorf("adding: %w", err)

	var dup *DuplicateSeatError
	if !errors.As(wrapped, &dup) || dup.Other != "personal" {
		t.Fatalf("errors.As should find the duplicate: %v", wrapped)
	}
	msg := err.Error()
	for _, want := range []string{`already vaulted as "personal"`, "someone@example.com", "Nothing was stored"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lost %q:\n%s", want, msg)
		}
	}
}
