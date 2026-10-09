package state

import (
	"testing"
	"time"
)

// 0.6.1 (decided 2026-10-08): idle accounts do not project. Only the live
// account of some profile, or an account read within poll_active, may have
// its figure carried forward by a burn rate. An account nobody is using gets
// a flat pair of readings, and a remembered rate on a flat pair would make
// its figure climb in `cs status` while nothing is spending it.
func TestIdleAccountsDoNotProject(t *testing.T) {
	now := time.Now()
	burning := func(readAgo time.Duration) *Account {
		return &Account{
			Last:      reading(50),
			LastAt:    now.Add(-readAgo),
			PrevWorst: 50, // a flat pair: nothing moved
			PrevAt:    now.Add(-readAgo - 10*time.Minute),
			LastRate:  2,
		}
	}
	s := &State{
		Profiles: map[string]*ProfileState{
			DefaultProfile: {Active: "live"},
			"work":         {Active: "work-live"},
		},
		Accounts: map[string]*Account{
			"live":      burning(10 * time.Minute),
			"work-live": burning(10 * time.Minute),
			"idle":      burning(10 * time.Minute),
			"fresh":     burning(30 * time.Second),
		},
	}
	pollActive := 2 * time.Minute
	cases := []struct {
		id      string
		project bool
	}{
		{"live", true},      // the default profile's live account
		{"work-live", true}, // another profile's live account
		{"idle", false},     // nobody's, read ten minutes ago
		{"fresh", true},     // nobody's, but read within poll_active
		{"missing", false},  // no record at all
	}
	for _, c := range cases {
		if got := s.Projects(c.id, now, pollActive); got != c.project {
			t.Errorf("Projects(%q) = %v, want %v", c.id, got, c.project)
		}
	}
	if got := s.ProjectedFor("idle", now, pollActive); got != 50 {
		t.Errorf("an idle account's figure climbed to %.1f; want its reading, 50", got)
	}
	if got := s.ProjectedFor("live", now, pollActive); got <= 50 {
		t.Errorf("the live account's figure was not projected: %.1f", got)
	}
	if got := s.ProjectedFor("missing", now, pollActive); got != 0 {
		t.Errorf("a missing account projected to %.1f, want 0", got)
	}
}
