package state

import (
	"encoding/json"
	"sort"
	"time"
)

// Ghost is the old live item of a profile removed from the config, or
// re-pointed to another dir, kept because the account last live there may
// still be: Claude Code keeps running on the old dir until someone stops it,
// and installing that account in a second profile is the one-credential-
// two-profiles failure (PROFILES §3). Every §3 check consults ghosts
// as it consults profiles, until the daemon finds the item no longer holds
// the account, or the person runs `cs profile forget <name>` (owner
// decision, lane 7 security review).
//
// The item is recorded by its resolved identity (keychain service, and the
// credential file on Linux), not by dir, so it is the same item after a
// daemon restart whatever the environment.
type Ghost struct {
	Profile string    `json:"profile"`
	Why     string    `json:"why"` // GhostRemoved or GhostRepointed
	Dir     string    `json:"dir,omitempty"`
	Service string    `json:"service"`
	File    string    `json:"credential_file,omitempty"`
	Account string    `json:"account"`
	Seat    string    `json:"seat,omitempty"`
	Since   time.Time `json:"since"`
}

// UnmarshalJSON also reads "instance", the field's name in dev builds before
// D19; "profile" wins when both are present. Saves write "profile" only.
func (g *Ghost) UnmarshalJSON(b []byte) error {
	type plain Ghost
	in := struct {
		*plain
		DevInstance string `json:"instance"`
	}{plain: (*plain)(g)}
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	if g.Profile == "" {
		g.Profile = in.DevInstance
	}
	return nil
}

const (
	GhostRemoved   = "removed"
	GhostRepointed = "re-pointed"
)

// Key identifies a ghost: one profile's one old item.
func (g *Ghost) Key() string { return g.Profile + "|" + g.Service + "|" + g.File }

// AddGhost records a ghost (replacing one with the same key).
func (s *State) AddGhost(g *Ghost) {
	if s.Ghosts == nil {
		s.Ghosts = map[string]*Ghost{}
	}
	k := g.Key()
	s.Ghosts[k] = g
	s.markGhost(k, true)
}

// DropGhost releases one ghost.
func (s *State) DropGhost(key string) {
	delete(s.Ghosts, key)
	s.markGhost(key, false)
}

// DropGhostsOf releases every ghost of a profile and says how many.
func (s *State) DropGhostsOf(name string) int {
	n := 0
	for k, g := range s.Ghosts {
		if g.Profile == name {
			s.DropGhost(k)
			n++
		}
	}
	return n
}

// GhostList is every ghost, oldest first.
func (s *State) GhostList() []*Ghost {
	out := make([]*Ghost, 0, len(s.Ghosts))
	for _, g := range s.Ghosts {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// RenameGhostAccount follows an account rename into the ghosts guarding it.
func (s *State) RenameGhostAccount(oldID, newID string) {
	for k, g := range s.Ghosts {
		if g.Account == oldID {
			cp := *g
			cp.Account = newID
			s.Ghosts[k] = &cp
			s.markGhost(k, true)
		}
	}
}

func (s *State) markGhost(key string, added bool) {
	if s.ghostAdds == nil {
		s.ghostAdds, s.ghostDrops = map[string]bool{}, map[string]bool{}
	}
	if added {
		s.ghostAdds[key] = true
		delete(s.ghostDrops, key)
	} else {
		s.ghostDrops[key] = true
		delete(s.ghostAdds, key)
	}
}

// mergeGhosts makes the ghosts the disk's plus this process's own changes
// since its last save. Either side may add or release one, so neither side's
// copy wins whole: a CLI `forget` must not be undone by the daemon's next
// save, and a ghost the daemon just made must not be lost to a CLI save.
func (s *State) mergeGhosts(disk *State) {
	merged := map[string]*Ghost{}
	for k, g := range disk.Ghosts {
		if g != nil {
			merged[k] = g
		}
	}
	for k := range s.ghostAdds {
		if g := s.Ghosts[k]; g != nil {
			merged[k] = g
		}
	}
	for k := range s.ghostDrops {
		delete(merged, k)
	}
	s.Ghosts = merged
	if len(s.Ghosts) == 0 {
		s.Ghosts = nil
	}
}
