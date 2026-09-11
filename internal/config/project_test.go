package config

import "testing"

func TestMatchDirCoversSubdirectories(t *testing.T) {
	for _, tc := range []struct {
		pattern, dir string
		want         bool
	}{
		{"/work/**", "/work", true},
		{"/work/**", "/work/repo", true},
		{"/work/**", "/work/a/b/c", true},
		{"/work/**", "/working", false}, // a prefix of the name, not a parent
		{"/work/**", "/other", false},
		{"/work", "/work/repo", true}, // a bare directory covers what is inside it
		{"/work", "/work", true},
		{"/work/repo", "/work/other", false},
	} {
		if got := matchDir(tc.pattern, tc.dir); got != tc.want {
			t.Errorf("matchDir(%q, %q) = %v, want %v", tc.pattern, tc.dir, got, tc.want)
		}
	}
}

// The most specific rule wins, so a subdirectory can override a broader rule.
func TestProjectForPrefersTheMostSpecificRule(t *testing.T) {
	c := &Config{Projects: map[string]Project{
		"/work/**":     {Eligible: []string{"work"}},
		"/work/oss/**": {Eligible: []string{"personal"}},
	}}
	pr, ok := c.ProjectFor("/work/oss/thing")
	if !ok {
		t.Fatal("expected a rule")
	}
	if len(pr.Eligible) != 1 || pr.Eligible[0] != "personal" {
		t.Fatalf("the deeper rule should win, got %v", pr.Eligible)
	}
}

func TestScopeAllowed(t *testing.T) {
	c := &Config{Projects: map[string]Project{
		"/work/**": {Eligible: []string{"work"}},
		"/any/**":  {Eligible: []string{"*"}},
	}}
	if !c.ScopeAllowed("work", "/work/x") {
		t.Error("work should be allowed in a work directory")
	}
	if c.ScopeAllowed("personal", "/work/x") {
		t.Error("personal must not be allowed in a work directory")
	}
	if !c.ScopeAllowed("personal", "/any/x") {
		t.Error(`"*" should allow anything`)
	}
	if !c.ScopeAllowed("personal", "/unruled/x") {
		t.Error("a directory with no rule restricts nothing")
	}
	if !c.ScopeAllowed("personal", "") {
		t.Error("an unknown directory restricts nothing")
	}
}

// A rule no account can satisfy would strand that directory silently.
func TestValidateRejectsAnUnsatisfiableProjectRule(t *testing.T) {
	c := &Config{
		SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		RefreshWindow: Duration{3600000000000},
		Accounts:      []Account{{ID: "a", Scope: "work"}},
		Projects:      map[string]Project{"/x/**": {Eligible: []string{"nonexistent"}}},
	}
	if err := c.validate(); err == nil {
		t.Fatal("a project rule naming a scope no account has should be refused at load time")
	}
}
