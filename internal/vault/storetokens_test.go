package vault

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// A credential vaulted from a direct login carries what Claude Code reads to
// name the session's plan (subscriptionType) and its tier (rateLimitTier),
// taken from the profile that identified it and mapped as Claude Code maps
// organization_type. Without them, a session this credential is swapped into
// is labelled "Claude API" instead of its plan. A type Claude Code does not
// map is left unset, as Claude Code leaves it; the tier is kept as reported.
func TestStoreTokensRecordsThePlanClaudeCodeReads(t *testing.T) {
	for _, tc := range []struct {
		name, orgType, tier, wantSub string
	}{
		{"team", "claude_team", "default_claude_max_5x", "team"},
		{"max", "claude_max", "default_claude_max_20x", "max"},
		{"pro", "claude_pro", "default_claude_ai", "pro"},
		{"enterprise", "claude_enterprise", "default_claude_max_5x", "enterprise"},
		{"unmapped type", "claude_something_new", "default_claude_max_5x", ""},
		{"profile without either", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := map[string]*keychain.Blob{}
			v, ep := testVaultWith(t, entries,
				map[string]string{"new-token": "org-1"}, map[string]string{"new-token": "acct-1@org-1"})
			ep.orgType = map[string]string{"new-token": tc.orgType}
			ep.tier = map[string]string{"new-token": tc.tier}
			// Pacing between the usage and profile calls is not what this tests.
			clock := time.Now()
			v.budget = usage.NewBudgetWithClock(func() time.Time { return clock },
				func(d time.Duration) { clock = clock.Add(d) })

			tok := &oauth.Tokens{AccessToken: "new-token", RefreshToken: "new-refresh", ExpiresIn: 28800}
			if _, err := v.StoreTokens(context.Background(), "work", "", tok, nil); err != nil {
				t.Fatal(err)
			}
			b := entries["work"]
			if b == nil || b.ClaudeAIOAuth == nil {
				t.Fatalf("nothing vaulted under work: %+v", entries)
			}
			if got := b.ClaudeAIOAuth.SubscriptionType; got != tc.wantSub {
				t.Errorf("subscriptionType = %q, want %q", got, tc.wantSub)
			}
			if got := b.ClaudeAIOAuth.RateLimitTier; got != tc.tier {
				t.Errorf("rateLimitTier = %q, want %q", got, tc.tier)
			}
		})
	}
}
