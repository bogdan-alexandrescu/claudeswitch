package main

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

type orgRecorder struct {
	known map[string]string
}

func (r *orgRecorder) HoldsAccountWhy(context.Context, keychain.Live, string, string) (bool, bool, time.Time) {
	return false, true, time.Time{}
}
func (r *orgRecorder) KnowOrg(key, org string) {
	if key != "" && org != "" {
		r.known[key] = org
	}
}

// The §3 check hands the checker what the daemon read behind each profile's
// live token, so a lock on one profile's token does not block the others.
func TestLiveElsewhereTellsTheCheckerEachProfilesOrg(t *testing.T) {
	st := &state.State{Profiles: map[string]*state.ProfileState{
		"work":    {Active: "work-team", LiveKey: "k-work", LiveOrg: "org-w"},
		"default": {Active: "personal"},
	}}
	r := &orgRecorder{known: map[string]string{}}
	liveElsewhereOf(context.Background(), r, &config.Config{}, st, "default", nil, "research")
	if r.known["k-work"] != "org-w" || len(r.known) != 1 {
		t.Fatalf("known orgs = %v, want only work's", r.known)
	}
}
