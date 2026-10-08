package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// recordSeat makes the config name the seat a credential was just verified to
// belong to. It is the step that used to be left to the person: a seat uuid is
// only knowable after signing in, so a block written in advance is unpinned by
// construction, and an unpinned account is one the daemon refuses to sync.
//
//   - an id the config does not have gets a new [[account]] block, pinned, and
//     is named last in priority;
//   - an id the config has but has not pinned gets the seat written into its
//     existing block, in place;
//   - a pinned id is left alone (the login was verified against that pin).
//
// The message says what changed, and is empty when nothing did. Every edit is
// textual and parsed back before it replaces the file, so comments and order
// survive and a config this could not produce cleanly is never left on disk.
func recordSeat(cfg *config.Config, id, scope string, e *vault.Entry, pool string) (string, error) {
	if e == nil || e.AccountUUID == "" || e.OrgID == "" {
		return "", fmt.Errorf("the credential did not say which seat it is, so %s cannot be pinned", id)
	}
	if scope == "" {
		scope = "work"
	}
	seat := e.AccountUUID + "@" + e.OrgID

	var existing *config.Account
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == id {
			existing = &cfg.Accounts[i]
		}
	}
	switch {
	case existing == nil:
		block := fmt.Sprintf("\n[[account]]\nid           = %q\nscope        = %q\n"+
			"account_uuid = %q\norg_id       = %q\n", id, scope, e.AccountUUID, e.OrgID)
		place, err := appendAccountInPool(cfg.Path, id, block, pool)
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("added %s to %s, pinned to seat %s (scope %s)", id, cfg.Path, usage.ShortSeat(seat), scope)
		if pool != "" {
			msg += fmt.Sprintf(", in profile %q's pool", pool)
		}
		switch place {
		case priorityNamed:
			msg += ", last in priority"
		case priorityNoList:
			msg += ", last in rotation order (there is no priority list)"
		case priorityMultiLine:
			msg += fmt.Sprintf(";\n      priority is written across several lines, which this does not edit:\n"+
				"      add %q to it yourself — until then it rotates after every listed account", id)
		}
		return msg, nil
	case existing.Seat() == seat:
		return "", nil
	case existing.Seat() != "":
		// vault.Store verifies against a full pin before anything is stored, so
		// reaching here means the caller skipped that check. Never rewrite it.
		return "", fmt.Errorf("%s is pinned to seat %s in %s, not %s; the config was left alone",
			id, usage.ShortSeat(existing.Seat()), cfg.Path, usage.ShortSeat(seat))
	}
	// Half a pin, written by hand. If it agrees, complete it; if not, one of the
	// two is wrong and only the person knows which.
	if existing.OrgID != "" && existing.OrgID != e.OrgID {
		return "", fmt.Errorf("%s names organization %s in %s, but the credential is organization %s; "+
			"the config was left alone — fix or remove that org_id", id, existing.OrgID, cfg.Path, e.OrgID)
	}
	if existing.AccountUUID != "" && existing.AccountUUID != e.AccountUUID {
		return "", fmt.Errorf("%s names account %s in %s, but the credential is account %s; "+
			"the config was left alone — fix or remove that account_uuid", id, existing.AccountUUID, cfg.Path, e.AccountUUID)
	}
	if err := pinAccount(cfg.Path, id, e.AccountUUID, e.OrgID); err != nil {
		return "", err
	}
	return fmt.Sprintf("pinned %s to seat %s in %s", id, usage.ShortSeat(seat), cfg.Path), nil
}

var (
	tableHeader = regexp.MustCompile(`^\s*\[`)
	accountHdr  = regexp.MustCompile(`^\s*\[\[\s*account\s*\]\]\s*(#.*)?$`)
	idLine      = regexp.MustCompile(`^\s*id\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	pinLine     = regexp.MustCompile(`^\s*(account_uuid|org_id)\s*=`)
	profileHdr  = regexp.MustCompile(`^\s*\[\[\s*profile\s*\]\]\s*(#.*)?$`)
	nameLine    = regexp.MustCompile(`^\s*name\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	poolLine    = regexp.MustCompile(`^\s*pool\s*=\s*\[`)
)

// addToPool names id last in the pool of the [[profile]] block called prof,
// editing the text in place: comments and layout survive. A pool written on
// one line or across several is extended where its list ends; a block with
// no pool line gets one under its name line. The caller parses the result
// back before it is written (writeConfigFile), so a layout this misreads is
// discarded rather than installed.
func addToPool(text, prof, id string) (string, error) {
	lines := strings.Split(text, "\n")
	start, end, nameAt := -1, len(lines), -1
	for i := 0; i < len(lines); i++ {
		if !profileHdr.MatchString(lines[i]) {
			continue
		}
		j := i + 1
		found := -1
		for ; j < len(lines) && !tableHeader.MatchString(lines[j]); j++ {
			if m := nameLine.FindStringSubmatch(lines[j]); m != nil && m[1]+m[2] == prof {
				found = j
			}
		}
		if found >= 0 {
			start, end, nameAt = i, j, found
			break
		}
		i = j - 1
	}
	if start < 0 {
		return "", fmt.Errorf("no [[profile]] block named %q to add %s to", prof, id)
	}
	poolAt := -1
	for k := start + 1; k < end; k++ {
		if poolLine.MatchString(lines[k]) {
			poolAt = k
			break
		}
	}
	quoted := strconv.Quote(id)
	if poolAt < 0 {
		out := append([]string{}, lines[:nameAt+1]...)
		out = append(out, "pool = ["+quoted+"]")
		out = append(out, lines[nameAt+1:]...)
		return strings.Join(out, "\n"), nil
	}

	// Offset of the pool line's '[' in the whole text, then scan to its
	// closing ']' past strings and comments, remembering the last character
	// that was neither space nor comment.
	off := 0
	for k := 0; k < poolAt; k++ {
		off += len(lines[k]) + 1
	}
	open := off + strings.Index(lines[poolAt], "[")
	last := open // index of the last significant character before ']'
	for i := open + 1; i < len(text); i++ {
		switch c := text[i]; c {
		case '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case '"', '\'':
			j := i + 1
			for j < len(text) && text[j] != c && text[j] != '\n' {
				if c == '"' && text[j] == '\\' {
					j++
				}
				j++
			}
			i, last = j, j
		case ' ', '\t', '\r', '\n':
		case ']':
			sep := ", "
			switch text[last] {
			case '[':
				sep = ""
			case ',':
				sep = " "
			}
			return text[:last+1] + sep + quoted + text[last+1:], nil
		default:
			last = i
		}
	}
	return "", fmt.Errorf("profile %q's pool list is not closed", prof)
}

// pinAccount writes account_uuid and org_id into the existing [[account]] block
// for id, directly under its id line, replacing any partial pin there. Only
// that block's lines change.
func pinAccount(path, id, accountUUID, orgID string) error {
	target, err := configTarget(path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")

	// Find the block: from its [[account]] header to the next table header.
	start, end, idAt := -1, len(lines), -1
	for i := 0; i < len(lines); i++ {
		if !accountHdr.MatchString(lines[i]) {
			continue
		}
		j := i + 1
		found := -1
		for ; j < len(lines) && !tableHeader.MatchString(lines[j]); j++ {
			if m := idLine.FindStringSubmatch(lines[j]); m != nil && m[1]+m[2] == id {
				found = j
			}
		}
		if found >= 0 {
			start, end, idAt = i, j, found
			break
		}
		i = j - 1
	}
	if start < 0 {
		return fmt.Errorf("no [[account]] block with id %q in %s", id, path)
	}

	var out []string
	out = append(out, lines[:start]...)
	for k := start; k < end; k++ {
		if pinLine.MatchString(lines[k]) {
			continue
		}
		out = append(out, lines[k])
		if k == idAt {
			out = append(out,
				fmt.Sprintf("account_uuid = %s", strconv.Quote(accountUUID)),
				fmt.Sprintf("org_id       = %s", strconv.Quote(orgID)))
		}
	}
	out = append(out, lines[end:]...)

	return writeConfigFile(target, []byte(strings.Join(out, "\n")), func(cfg *config.Config) error {
		if got := cfg.SeatOf(id); got != accountUUID+"@"+orgID {
			return fmt.Errorf("the edit parsed but %q did not come out pinned; discarded", id)
		}
		return nil
	})
}
