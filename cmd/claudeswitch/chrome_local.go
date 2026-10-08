package main

// IMPROVEMENTS C2: your own Chrome profiles, one per claudeswitch profile,
// overridable per account.
//
// Owner decision 2026-10-08: claudeswitch may read Chrome's Local State,
// read-only, for the profile list only — profile.info_cache (folder →
// display name) and profile.last_used. Nothing else of Chrome's is read
// (no cookies, Preferences or extension data), and nothing of Chrome's is
// ever written. A missing or unreadable Local State is "no list": folder
// names still work wherever a Chrome profile is named.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// chromeLocalStatePaths is where Local State is looked for, in order; the
// first one that reads is used. A seam, so tests never touch the real file.
var chromeLocalStatePaths = defaultChromeLocalStatePaths

func defaultChromeLocalStatePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	switch chromeGOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Local State")}
	case "linux":
		return []string{
			filepath.Join(home, ".config", "google-chrome", "Local State"),
			filepath.Join(home, ".config", "chromium", "Local State"),
		}
	}
	return nil
}

// chromeLocalStateMax bounds the read; Local State is a few hundred KB.
const chromeLocalStateMax = 32 << 20

// chromeLocalProfile is one Chrome profile as Local State lists it.
type chromeLocalProfile struct {
	Folder   string
	Name     string
	LastUsed bool
}

// chromeLocal is Chrome's profile list. Found is false when there is none.
type chromeLocal struct {
	Found    bool
	Profiles []chromeLocalProfile
	LastUsed string
}

// readChromeLocal reads the profile list from the first Local State that
// reads and parses.
func readChromeLocal() chromeLocal {
	for _, p := range chromeLocalStatePaths() {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, chromeLocalStateMax+1))
		_ = f.Close()
		if err != nil || len(b) > chromeLocalStateMax {
			continue
		}
		if l, ok := parseChromeLocal(b); ok {
			return l
		}
	}
	return chromeLocal{}
}

// parseChromeLocal decodes profile.info_cache's names and profile.last_used
// and nothing else. A folder that could not be passed to Chrome safely is
// left out.
func parseChromeLocal(b []byte) (chromeLocal, bool) {
	var doc struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
			LastUsed string `json:"last_used"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return chromeLocal{}, false
	}
	l := chromeLocal{Found: true}
	for folder, info := range doc.Profile.InfoCache {
		if config.ValidChromeFolder(folder) != nil {
			continue
		}
		l.Profiles = append(l.Profiles, chromeLocalProfile{Folder: folder, Name: info.Name})
	}
	sort.Slice(l.Profiles, func(i, j int) bool { return chromeFolderLess(l.Profiles[i].Folder, l.Profiles[j].Folder) })
	if lu := doc.Profile.LastUsed; lu != "" && config.ValidChromeFolder(lu) == nil {
		l.LastUsed = lu
	} else if len(l.Profiles) > 0 && l.has("Default") {
		l.LastUsed = "Default" // Chrome's own fallback when last_used is unset
	}
	for i := range l.Profiles {
		l.Profiles[i].LastUsed = l.Profiles[i].Folder == l.LastUsed
	}
	return l, true
}

// chromeFolderLess orders Chrome's folders as Chrome makes them: Default,
// then Profile 1, Profile 2 … by number, then anything else by name.
func chromeFolderLess(a, b string) bool {
	rank := func(s string) (int, int) {
		if s == "Default" {
			return 0, 0
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "Profile ")); err == nil && strings.HasPrefix(s, "Profile ") {
			return 1, n
		}
		return 2, 0
	}
	ra, na := rank(a)
	rb, nb := rank(b)
	if ra != rb {
		return ra < rb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

func (l chromeLocal) has(folder string) bool {
	return slices.ContainsFunc(l.Profiles, func(p chromeLocalProfile) bool { return p.Folder == folder })
}

// nameOf is a folder's display name, "" when Local State does not list it.
func (l chromeLocal) nameOf(folder string) string {
	for _, p := range l.Profiles {
		if p.Folder == folder {
			return p.Name
		}
	}
	return ""
}

// label is how a Chrome profile is named to a person: its display name
// when known, else its folder.
func (l chromeLocal) label(folder string) string {
	if n := l.nameOf(folder); n != "" {
		return n
	}
	return folder
}

// known lists Chrome's profiles for a hint: "Person 1" (Default), …
func (l chromeLocal) known() string {
	var parts []string
	for _, p := range l.Profiles {
		parts = append(parts, fmt.Sprintf("%q (%s)", p.Name, p.Folder))
	}
	return strings.Join(parts, ", ")
}

// lookup turns what a person typed — a folder or a display name — into a
// folder. With Local State, a folder it lists wins, then an exact name, then
// a name in another case; anything else is refused with the list. Without
// it, the argument is taken as a folder.
func (l chromeLocal) lookup(arg string) (string, *appError) {
	arg = strings.TrimSpace(arg)
	if !l.Found || len(l.Profiles) == 0 {
		if err := config.ValidChromeFolder(arg); err != nil {
			return "", appErr(codeInvalidValue, "name the Chrome profile by its folder (Default, Profile 1, …): "+
				"Chrome's profile list could not be read", "%v", err)
		}
		return arg, nil
	}
	if l.has(arg) {
		return arg, nil
	}
	for _, fold := range []bool{false, true} {
		var hits []string
		for _, p := range l.Profiles {
			if p.Name == arg || fold && strings.EqualFold(p.Name, arg) {
				hits = append(hits, p.Folder)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil
		default:
			return "", appErr(codeInvalidValue, "name it by its folder: "+strings.Join(hits, ", "),
				"more than one Chrome profile is called %q", arg)
		}
	}
	return "", appErr(codeNotFound, "Chrome's profiles are: "+l.known(), "no Chrome profile called %q", arg)
}

// chromeResolved is the Chrome profile Claude in Chrome is used from for an
// account, and the rule that chose it.
type chromeResolved struct {
	Account string
	// Profile is the claudeswitch profile the rule was judged in.
	Profile string
	Dir     string
	Name    string
	// Rule is "account" (its own mapping), "profile" (the profile's chrome),
	// "last_used" (Chrome's last-used profile) or "none" (nothing known).
	Rule string
}

// chromeProfileFor is the claudeswitch profile an account is judged in when
// none is named: where it is live, else the profile whose pool holds it.
func chromeProfileFor(cfg *config.Config, st *state.State, account string) string {
	for _, in := range cfg.EffectiveProfiles() {
		if ps := st.Profiles[in.Name]; ps != nil && ps.Active == account {
			return in.Name
		}
	}
	if p, ok := cfg.ProfileOf(account); ok {
		return p
	}
	return ""
}

// resolveChrome is the one resolution every caller uses (`cs chrome`,
// `chrome signin`, the notices, the app's JSON): the account's own mapping,
// then the profile's chrome, then Chrome's last-used profile. It never
// creates anything.
func resolveChrome(cfg *config.Config, st *state.State, local chromeLocal, profile, account string) chromeResolved {
	if profile == "" {
		profile = chromeProfileFor(cfg, st, account)
	}
	r := chromeResolved{Account: account, Profile: profile, Rule: "none"}
	switch {
	case st.ChromeOf(account) != nil:
		r.Dir, r.Rule = st.ChromeOf(account).Dir, "account"
	case profile != "" && cfg.ChromeFor(profile) != "":
		r.Dir, r.Rule = cfg.ChromeFor(profile), "profile"
	case local.LastUsed != "":
		r.Dir, r.Rule = local.LastUsed, "last_used"
	}
	r.Name = local.nameOf(r.Dir)
	return r
}

// label names the resolved Chrome profile to a person.
func (r chromeResolved) label() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Dir
}

// why says, in words, which rule chose it.
func (r chromeResolved) why() string {
	switch r.Rule {
	case "account":
		return r.Account + "'s own Chrome profile"
	case "profile":
		return "profile " + r.Profile + "'s Chrome profile"
	case "last_used":
		return "Chrome's last used"
	}
	return ""
}

// json is the resolution as the app reads it.
func (r chromeResolved) json() map[string]any {
	return map[string]any{"account": r.Account, "profile": orNull(r.Profile), "profile_dir": orNull(r.Dir),
		"name": orNull(r.Name), "rule": r.Rule}
}

// chromeSwitchNotice is the line said after a rotation in profile to the
// account `to` (IMPROVEMENTS C2). When only the profile's chrome applies,
// the extension there is still signed in as the account before; anything
// else is lane 13's notice.
func chromeSwitchNotice(cfg *config.Config, st *state.State, local chromeLocal, profile, from, to string) string {
	if to == "" || from == to {
		return ""
	}
	r := resolveChrome(cfg, st, local, profile, to)
	if r.Rule != "profile" {
		return chromeRotationNotice(st, from, to)
	}
	// The account before used this Chrome profile too, so its extension
	// was signed in as it; otherwise who it is signed in as is not known.
	if from != "" && st.ChromeOf(from) == nil {
		return fmt.Sprintf("Claude in Chrome in %q is still signed in as %s; sign it in as %s (cs chrome signin %s)",
			r.label(), from, to, to)
	}
	return fmt.Sprintf("Claude in Chrome in %q may be signed in as another account; sign it in as %s (cs chrome signin %s)",
		r.label(), to, to)
}

// useChromeNoteIn is `cs use`'s form of the notice.
func useChromeNoteIn(w io.Writer, cfg *config.Config, st *state.State, profile, from, to string) {
	if msg := chromeSwitchNotice(cfg, st, readChromeLocal(), profile, from, to); msg != "" {
		fmt.Fprintf(w, "    %s\n", msg)
	}
}

// chromeProfiles is `cs chrome profiles`: Chrome's profiles from Local
// State, and which claudeswitch profiles and accounts point at each. A
// folder something points at that Local State does not list is shown too.
func chromeProfiles(stdout io.Writer, cfg *config.Config, st *state.State, asJSON bool) error {
	local := readChromeLocal()
	byProfile := map[string][]string{}
	for _, in := range cfg.EffectiveProfiles() {
		if f := cfg.ChromeFor(in.Name); f != "" {
			byProfile[f] = append(byProfile[f], in.Name)
		}
	}
	byAccount := map[string][]string{}
	for _, e := range st.ChromeList() {
		byAccount[e.Dir] = append(byAccount[e.Dir], e.Account)
	}
	type row struct {
		folder, name       string
		lastUsed, inChrome bool
	}
	var rows []row
	for _, p := range local.Profiles {
		rows = append(rows, row{p.Folder, p.Name, p.LastUsed, true})
	}
	var extra []string
	for f := range byProfile {
		if !local.has(f) && !slices.Contains(extra, f) {
			extra = append(extra, f)
		}
	}
	for f := range byAccount {
		if !local.has(f) && !slices.Contains(extra, f) {
			extra = append(extra, f)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return chromeFolderLess(extra[i], extra[j]) })
	for _, f := range extra {
		rows = append(rows, row{folder: f})
	}
	orEmpty := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}

	if asJSON {
		list := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			list = append(list, map[string]any{"folder": r.folder, "name": orNull(r.name), "last_used": r.lastUsed,
				"in_chrome": r.inChrome, "used_by_profiles": orEmpty(byProfile[r.folder]),
				"used_by_accounts": orEmpty(byAccount[r.folder])})
		}
		return emitJSONTo(stdout, map[string]any{"chrome_profiles": list, "last_used": orNull(local.LastUsed),
			"local_state": local.Found, "supported": chromeSupported(chromeGOOS)})
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "\n  Chrome's profile list could not be read; name a Chrome profile by its folder "+
			"(Default, Profile 1, …)\n\n")
		return nil
	}
	fmt.Fprintln(stdout)
	for _, r := range rows {
		name := r.name
		if !r.inChrome {
			name = "(not in Chrome's list)"
		}
		var uses []string
		if r.lastUsed {
			uses = append(uses, "Chrome's last used")
		}
		for _, p := range byProfile[r.folder] {
			uses = append(uses, "profile "+p)
		}
		if a := byAccount[r.folder]; len(a) > 0 {
			uses = append(uses, "accounts "+strings.Join(a, ", "))
		}
		fmt.Fprintf(stdout, "  %-22s %-14s %s\n", name, r.folder, strings.Join(uses, "; "))
	}
	fmt.Fprintf(stdout, "\n  use one for a profile:   cs profile set <profile> chrome <name>\n")
	fmt.Fprintf(stdout, "  or for one account:      cs chrome add <account> --existing <name>\n\n")
	return nil
}

// chromeAddExisting is `cs chrome add <account> --existing <folder|name>`:
// it records the mapping to a Chrome profile the person already has. It
// creates nothing and opens nothing.
func chromeAddExisting(stdout, human io.Writer, cfg *config.Config, st *state.State, id, arg string, asJSON bool) error {
	if !hasAccount(cfg, id) && !slices.Contains(st.Vaulted, id) {
		return chromeFail("not_found", "the accounts are listed by: cs accounts",
			"no account %q in the config or the vault", id)
	}
	local := readChromeLocal()
	dir, e := local.lookup(arg)
	if e != nil {
		return e
	}
	st.SetChromeExisting(id, dir, chromeNow())
	if err := st.Save(); err != nil {
		return chromeFail("failed", "", "could not save the state: %v", err)
	}
	email := st.EmailOf(id)
	if asJSON {
		return emitJSONTo(stdout, map[string]any{"account": id, "profile_dir": dir, "name": orNull(local.nameOf(dir)),
			"created": false, "existing": true, "opened": false, "email": orNull(email), "urls": []string{}})
	}
	as := id
	if email != "" {
		as = email + " (" + id + ")"
	}
	fmt.Fprintf(human, "\n  %s now uses the Chrome profile %q (folder %q)\n", id, local.label(dir), dir)
	fmt.Fprintf(human, "  Claude in Chrome there must be signed in as %s: cs chrome signin %s\n\n", as, id)
	return nil
}

// chromeSignin is `cs chrome signin [<account>]`: it opens the account's
// resolved Chrome profile at the claude.ai login and Claude in Chrome's
// page, and says what to do there. It never reads the keychain: the email is
// the one state.json recorded.
func chromeSignin(stdout, human io.Writer, cfg *config.Config, st *state.State, id, via string, asJSON bool) error {
	if !hasAccount(cfg, id) && !slices.Contains(st.Vaulted, id) {
		return chromeFail("not_found", "the accounts are listed by: cs accounts",
			"no account %q in the config or the vault", id)
	}
	local := readChromeLocal()
	r := resolveChrome(cfg, st, local, via, id)
	if r.Rule == "none" {
		return chromeNothingToOpen(r)
	}
	urls := []string{chromeLoginURL, chromeStoreURL}
	if err := launchChrome(r.Dir, urls); err != nil {
		return err
	}
	email := st.EmailOf(id)
	if asJSON {
		m := r.json()
		m["opened"], m["email"], m["urls"] = true, orNull(email), urls
		return emitJSONTo(stdout, m)
	}
	as := id
	if email != "" {
		as = email + " (" + id + ")"
	}
	fmt.Fprintf(human, "\n  opened the Chrome profile %q for %s (%s)\n", r.label(), id, r.why())
	fmt.Fprintf(human, "\n  In that Chrome window:\n")
	fmt.Fprintf(human, "    1. on claude.ai, sign out if another account shows, then sign in as %s\n", as)
	fmt.Fprintf(human, "    2. open Claude in Chrome (its toolbar icon; the Web Store tab adds it if it is missing),\n")
	fmt.Fprintf(human, "       sign it out of the account it is on, and sign it in as %s\n", as)
	if r.Rule != "account" {
		fmt.Fprintf(human, "\n  A Chrome profile shared by a profile's accounts needs this after every rotation.\n")
		fmt.Fprintf(human, "  Give %s a Chrome profile of its own and Claude in Chrome follows rotations by itself:\n", id)
		fmt.Fprintf(human, "    cs chrome add %s --existing <name>   (or without --existing, a new one)\n", id)
	}
	fmt.Fprintln(human)
	return nil
}

// chromeNothingToOpen is the refusal when no rule names a Chrome profile.
func chromeNothingToOpen(r chromeResolved) *appError {
	hint := "cs chrome add " + r.Account
	if r.Profile != "" {
		hint = "cs profile set " + r.Profile + " chrome <name>, or " + hint
	}
	return chromeFail("not_found", hint,
		"no Chrome profile for %s: it has none of its own, its profile names none, and Chrome's last-used "+
			"profile could not be read", r.Account)
}

// chromeLiveJSON is, per claudeswitch profile with a live account, that
// account's resolved Chrome profile and the rotation that made it live:
// what the app's card needs for its sign-in notice.
func chromeLiveJSON(cfg *config.Config, st *state.State, local chromeLocal) []map[string]any {
	out := []map[string]any{}
	for _, in := range cfg.EffectiveProfiles() {
		ps := st.Profiles[in.Name]
		if ps == nil || ps.Active == "" {
			continue
		}
		m := resolveChrome(cfg, st, local, in.Name, ps.Active).json()
		var at any
		if !ps.LastSwitch.IsZero() {
			at = ps.LastSwitch.UTC().Format(time.RFC3339)
		}
		m["last_from"], m["last_switch"] = orNull(ps.LastFrom), at
		out = append(out, m)
	}
	return out
}

// profileSetChrome is `cs profile set <profile> chrome <folder|name|inherit>`.
// The value is stored as the folder; a display name resolves through Local
// State.
func profileSetChrome(w io.Writer, cfg *config.Config, i int, name, value string, asJSON bool) error {
	local := readChromeLocal()
	unset := value == "" || value == "inherit"
	folder := ""
	if !unset {
		f, e := local.lookup(value)
		if e != nil {
			return e
		}
		folder = f
	}
	cfg.Profiles[i].Chrome = folder
	if err := cfg.Validate(); err != nil {
		return appErr(codeInvalidValue, "", "profile %q: chrome would make the config invalid: %v", name, err)
	}
	ref := blockRef{"profile", name}
	err := editConfigText(cfg.Path, func(text string) (string, error) {
		if unset {
			return deleteKey(text, ref, "chrome")
		}
		return setKey(text, ref, "chrome", strconv.Quote(folder))
	}, func(back *config.Config) error {
		if got := back.ChromeFor(name); got != folder {
			return fmt.Errorf("the edit parsed but profile %q's chrome read back as %q, not %q; discarded",
				name, got, folder)
		}
		return nil
	})
	if err != nil {
		return err
	}
	effective := folder
	if effective == "" {
		effective = local.LastUsed
	}
	if asJSON {
		return emitTo(w, map[string]any{"profile": name, "key": "chrome", "override": orNull(folder),
			"effective": orNull(effective), "name": orNull(local.nameOf(folder)), "path": cfg.Path})
	}
	if folder != "" {
		fmt.Fprintf(w, "  profile %s: chrome = %s\n", name, chromeShow(local, folder))
	} else {
		fmt.Fprintf(w, "  profile %s: chrome is Chrome's last-used profile\n", name)
	}
	return nil
}

// chromeShow is a folder as `cs config` shows it: "Work" (Profile 1).
func chromeShow(local chromeLocal, folder string) string {
	if folder == "" {
		return "Chrome's last used"
	}
	if n := local.nameOf(folder); n != "" {
		return fmt.Sprintf("%q (%s)", n, folder)
	}
	return folder
}
