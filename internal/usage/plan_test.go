package usage

import "testing"

// Anthropic renamed its plans on 2026-10-30: Max 20x is now Ultra and Max 5x
// is now Max. The rate-limit tiers on the wire kept their old names, so the
// label has to follow the plan, not the wire value.
func TestPlanUsesTheCurrentPlanNames(t *testing.T) {
	for _, c := range []struct {
		tier, orgType string
		overflow      bool
		want          string
	}{
		{"default_claude_max_20x", "claude_max", false, "Ultra"},
		{"default_claude_max_5x", "claude_max", false, "Max"},
		{"default_claude_max_5x", "claude_team", false, "Max team"},
		{"default_claude_max_20x", "claude_team", true, "Ultra team +overflow"},
		{"default_claude_pro", "claude_pro", false, "Pro"},
	} {
		var p Profile
		p.Organization.RateLimitTier = c.tier
		p.Organization.Type = c.orgType
		p.Organization.ExtraUsage = c.overflow
		if got := p.Plan(); got != c.want {
			t.Errorf("%s (%s): got %q, want %q", c.tier, c.orgType, got, c.want)
		}
	}
}
