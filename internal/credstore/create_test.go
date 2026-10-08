package credstore

import "testing"

// LiveServiceName is the item Claude Code reads for a dir, computed: the
// name `cs profile create --seed` creates, so Claude Code adopts it.
func TestLiveServiceNameIsClaudeCodes(t *testing.T) {
	if got := LiveServiceName(""); got != liveServiceBase {
		t.Errorf("no dir: %q", got)
	}
	d := "/home/someone/.claude-work"
	if got := LiveServiceName(d); got != liveServiceFor(d) {
		t.Errorf("%q: %q, want %q", d, got, liveServiceFor(d))
	}
}
