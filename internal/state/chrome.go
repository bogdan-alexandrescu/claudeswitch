package state

import (
	"sort"
	"time"
)

// ChromeProfile is an account's own Chrome profile (IMPROVEMENTS C1, C2):
// a directory name passed to Chrome as --profile-directory, inside Chrome's
// default user-data dir. Either `cs chrome add` made it, or (Existing) it is
// one the person already had, named with `cs chrome add --existing`.
// claudeswitch records the mapping here and never writes Chrome's files.
type ChromeProfile struct {
	Dir   string    `json:"profile_dir"`
	Added time.Time `json:"added,omitzero"`
	// Existing marks a Chrome profile claudeswitch did not create.
	Existing bool `json:"existing,omitempty"`
}

// ChromeEntry is one mapping, for listing.
type ChromeEntry struct {
	Account string
	ChromeProfile
}

// SetChrome records that account's Chrome profile.
func (s *State) SetChrome(account, dir string, at time.Time) {
	if s.Chrome == nil {
		s.Chrome = map[string]*ChromeProfile{}
	}
	s.Chrome[account] = &ChromeProfile{Dir: dir, Added: at}
	s.touchChrome(account)
}

// SetChromeExisting records that account's Chrome profile as one the person
// already had (IMPROVEMENTS C2).
func (s *State) SetChromeExisting(account, dir string, at time.Time) {
	s.SetChrome(account, dir, at)
	s.Chrome[account].Existing = true
}

// DropChrome forgets an account's Chrome profile and says whether there was
// one. The Chrome profile itself is left alone.
func (s *State) DropChrome(account string) bool {
	if _, ok := s.Chrome[account]; !ok {
		return false
	}
	delete(s.Chrome, account)
	s.touchChrome(account)
	return true
}

// ChromeOf is an account's Chrome profile, nil when it has none.
func (s *State) ChromeOf(account string) *ChromeProfile {
	if account == "" {
		return nil
	}
	return s.Chrome[account]
}

// ChromeList is every mapping, by account.
func (s *State) ChromeList() []ChromeEntry {
	out := make([]ChromeEntry, 0, len(s.Chrome))
	for id, cp := range s.Chrome {
		if cp != nil {
			out = append(out, ChromeEntry{Account: id, ChromeProfile: *cp})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// ChromeHinted is the rotation a profile's browser-tool hint was last given
// for (an opaque key the caller builds), "" when never.
func (s *State) ChromeHinted(profile string) string { return s.ChromeHints[profile] }

// SetChromeHinted records that the hint was given for that rotation.
func (s *State) SetChromeHinted(profile, key string) {
	if s.ChromeHints == nil {
		s.ChromeHints = map[string]string{}
	}
	s.ChromeHints[profile] = key
	if s.hintTouched == nil {
		s.hintTouched = map[string]bool{}
	}
	s.hintTouched[profile] = true
}

// SetEmail records the email an account's credential belongs to, as learned
// when it was vaulted or identified. An empty email records nothing.
func (s *State) SetEmail(account, email string) {
	if account == "" || email == "" {
		return
	}
	if s.Emails == nil {
		s.Emails = map[string]string{}
	}
	s.Emails[account] = email
	s.touchEmail(account)
}

// EmailOf is an account's recorded email, "" when none was recorded.
func (s *State) EmailOf(account string) string { return s.Emails[account] }

// SetPlan records the subscription behind an account ("Max 20x"), as the
// profile endpoint or a vault entry named it. An empty plan records nothing,
// and recording the plan already recorded changes nothing.
func (s *State) SetPlan(account, plan string) {
	if account == "" || plan == "" || s.Plans[account] == plan {
		return
	}
	if s.Plans == nil {
		s.Plans = map[string]string{}
	}
	s.Plans[account] = plan
	s.touchPlan(account)
}

// PlanOf is an account's recorded plan, "" when none was recorded.
func (s *State) PlanOf(account string) string { return s.Plans[account] }

// ForgetIdentity drops the email and plan recorded for an account, as its
// deletion does; a save then removes them from disk too.
func (s *State) ForgetIdentity(account string) {
	delete(s.Emails, account)
	s.touchEmail(account)
	delete(s.Plans, account)
	s.touchPlan(account)
}

func (s *State) touchPlan(account string) {
	if s.planTouched == nil {
		s.planTouched = map[string]bool{}
	}
	s.planTouched[account] = true
}

// RenameChromeAccount moves an account's Chrome mapping, email and plan to
// its new id. The Chrome profile directory keeps its name: it is Chrome's.
func (s *State) RenameChromeAccount(oldID, newID string) {
	if cp, ok := s.Chrome[oldID]; ok {
		delete(s.Chrome, oldID)
		s.Chrome[newID] = cp
		s.touchChrome(oldID)
		s.touchChrome(newID)
	}
	if e, ok := s.Emails[oldID]; ok {
		delete(s.Emails, oldID)
		s.Emails[newID] = e
		s.touchEmail(oldID)
		s.touchEmail(newID)
	}
	if p, ok := s.Plans[oldID]; ok {
		delete(s.Plans, oldID)
		s.Plans[newID] = p
		s.touchPlan(oldID)
		s.touchPlan(newID)
	}
}

func (s *State) touchEmail(account string) {
	if s.emailTouched == nil {
		s.emailTouched = map[string]bool{}
	}
	s.emailTouched[account] = true
}

func (s *State) touchChrome(account string) {
	if s.chromeTouched == nil {
		s.chromeTouched = map[string]bool{}
	}
	s.chromeTouched[account] = true
}

// mergeChrome keeps what is on disk and applies only this process's own
// changes on top, the way ghosts merge: the daemon never changes either map,
// so its saves adopt a CLI's add or forget instead of undoing it.
func (s *State) mergeChrome(disk *State) {
	merged := map[string]*ChromeProfile{}
	for id, cp := range disk.Chrome {
		if cp != nil {
			merged[id] = cp
		}
	}
	for id := range s.chromeTouched {
		if cp := s.Chrome[id]; cp != nil {
			merged[id] = cp
		} else {
			delete(merged, id)
		}
	}
	s.Chrome = merged
	if len(s.Chrome) == 0 {
		s.Chrome = nil
	}

	hints := map[string]string{}
	for p, k := range disk.ChromeHints {
		hints[p] = k
	}
	for p := range s.hintTouched {
		if k, ok := s.ChromeHints[p]; ok {
			hints[p] = k
		} else {
			delete(hints, p)
		}
	}
	s.ChromeHints = hints
	if len(s.ChromeHints) == 0 {
		s.ChromeHints = nil
	}

	emails := map[string]string{}
	for id, e := range disk.Emails {
		emails[id] = e
	}
	for id := range s.emailTouched {
		if e, ok := s.Emails[id]; ok {
			emails[id] = e
		} else {
			delete(emails, id)
		}
	}
	s.Emails = emails
	if len(s.Emails) == 0 {
		s.Emails = nil
	}

	plans := map[string]string{}
	for id, p := range disk.Plans {
		plans[id] = p
	}
	for id := range s.planTouched {
		if p, ok := s.Plans[id]; ok {
			plans[id] = p
		} else {
			delete(plans, id)
		}
	}
	s.Plans = plans
	if len(s.Plans) == 0 {
		s.Plans = nil
	}
}
