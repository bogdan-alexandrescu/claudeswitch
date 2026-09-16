package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// tempState is a state file a test can write to, so the recording half of this
// can be asserted rather than assumed.
func tempState(t *testing.T) *state.State {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A spent refresh token is the one condition here that no amount of waiting
// fixes. `refresh` explained it to whoever ran the command and recorded
// nothing, so `status` went on showing whatever the last poll had said — and
// the two then disagreed about the only thing needing action.
func TestRefreshRecordsAnAccountThatCannotBeRenewed(t *testing.T) {
	st := tempState(t)
	var out bytes.Buffer

	err := &oauth.NeedsLoginError{Detail: "the stored refresh token expired on 2026-09-01"}
	if !reportUnrenewable(&out, st, "work-a", err) {
		t.Fatal("a spent refresh token must be reported as terminal")
	}

	got := st.Get("work-a").LastErr
	if got == "" {
		t.Error("it must be recorded where status looks, not only printed")
	}
	if !strings.Contains(got, "interactive login") {
		t.Errorf("the recorded text must say what is wrong: %q", got)
	}
	// stateOf keys off this text, so the two have to agree about it.
	if !strings.Contains(out.String(), "cannot be refreshed") {
		t.Errorf("the person running the command must be told too:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "work-a") {
		t.Errorf("the account must be named:\n%s", out.String())
	}
}

// Everything else is the caller's to handle: a rate limit, a network blip and a
// spent token must not be reported the same way, since only one of them needs
// a person.
func TestRefreshLeavesOtherFailuresToTheCaller(t *testing.T) {
	st := tempState(t)
	var out bytes.Buffer

	for _, err := range []error{
		errors.New("usage API rate limited, retry in 52m50s"),
		errors.New("dial tcp: lookup api.anthropic.com: no such host"),
	} {
		if reportUnrenewable(&out, st, "work-a", err) {
			t.Errorf("%v is not a terminal condition", err)
		}
	}
	if st.Get("work-a").LastErr != "" {
		t.Errorf("nothing should have been recorded: %q", st.Get("work-a").LastErr)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should have been printed:\n%s", out.String())
	}
}

// The error travels wrapped through Refresh, so unwrapping has to work or the
// terminal case is silently treated as transient.
func TestRefreshSeesAWrappedNeedsLogin(t *testing.T) {
	st := tempState(t)
	var out bytes.Buffer

	wrapped := errors.New("refreshing work-a: " +
		(&oauth.NeedsLoginError{Detail: "invalid_grant"}).Error())
	if reportUnrenewable(&out, st, "work-a", wrapped) {
		t.Error("a look-alike string is not the typed error and must not match")
	}

	real := errors.Join(errors.New("context"),
		&oauth.NeedsLoginError{Detail: "invalid_grant"})
	if !reportUnrenewable(&out, st, "work-a", real) {
		t.Error("a wrapped NeedsLoginError must still be recognised")
	}
}
