package credstore

import (
	"errors"
	"testing"
)

// A refresh revokes the old token the moment it succeeds, so the store has to
// be proven writable *before* that call. This is the proof — and it must not
// prompt, which is why it rewrites an existing item rather than creating one.
func TestCheckWritableRewritesInPlace(t *testing.T) {
	const svc = "claudeswitch:__test_rewrite"
	want := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "value-under-test"}}
	if err := Write(svc, want); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("no usable credential store on this machine: %v", err)
		}
		t.Fatalf("setting up the item: %v", err)
	}
	t.Cleanup(func() { _ = Delete(svc) })

	if err := CheckWritable(svc); err != nil {
		t.Fatalf("CheckWritable on a store that is answering: %v", err)
	}
	// The point of a no-op rewrite is that it is a no-op.
	got, err := Read(svc)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if got.ClaudeAIOAuth.AccessToken != want.ClaudeAIOAuth.AccessToken {
		t.Fatalf("the check altered the stored value: got %q",
			got.ClaudeAIOAuth.AccessToken)
	}
}

// A missing item must fail rather than be created: the caller is asking whether
// an account it already has can be updated, and silently inventing one would
// answer a different question.
func TestCheckWritableRefusesAMissingItem(t *testing.T) {
	if err := CheckWritable("claudeswitch:__definitely_not_present"); err == nil {
		t.Fatal("expected an error for an item that does not exist")
	}
}
