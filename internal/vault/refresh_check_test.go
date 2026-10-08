package vault

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// sizedItem is a live item whose store refuses every write (CheckWrite and
// Write alike), as a keychain would a line over the security -i limit.
type sizedItem struct {
	*recordingItem
	checks int
}

func (s *sizedItem) CheckWrite(*keychain.Blob) error {
	s.checks++
	return errors.New("the command is 4100 bytes and security -i reads at most 4032 per line")
}

func (s *sizedItem) Write(*keychain.Blob) error {
	return errors.New("the command is 4100 bytes and security -i reads at most 4032 per line")
}

// Lane 7: refreshing a live account revokes the token the session holds, so
// the renewed one must reach the live item. If the store would refuse that
// write, the refresh is refused before the token exchange — not after, when
// the live item is left holding a revoked token.
func TestRefreshInChecksTheLiveWriteBeforeExchanging(t *testing.T) {
	entries := map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w1-token", RefreshToken: "r-w1"}},
	}
	v := testVault(t, entries, map[string]string{"renewed": "org-w"})
	work := &sizedItem{recordingItem: item("work", "w1-token")}

	_, err := v.RefreshIn(context.Background(), "w1", "", work, true)
	if err == nil {
		t.Fatal("refreshed a live account whose renewed credential could not be written back")
	}
	if got := entries["w1"].ClaudeAIOAuth.AccessToken; got != "w1-token" {
		t.Fatalf("the token was exchanged anyway (vault now holds %q); the live item keeps a revoked one", got)
	}
	if work.checks == 0 {
		t.Error("the live write was never checked")
	}
	if strings.Contains(err.Error(), "CREDENTIAL AT RISK") || strings.Contains(err.Error(), "revoked token") {
		t.Errorf("refused up front, so nothing is at risk; got %v", err)
	}
}
