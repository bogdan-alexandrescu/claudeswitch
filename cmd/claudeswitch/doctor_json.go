package main

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// `cs doctor --json` (IMPROVEMENTS F12): every check doctor prints, as
// {name, status, message, fix}. It is read from the text doctor writes, so
// the two can never disagree and the text stays exactly as it was: each
// "[mark] name  message" row is a check, its └ lines are its details. The
// rows that name an account (each vaulted account's refresh token, and
// --verify's verdicts) are detail lines in the text; the JSON gives each
// its own check, with the account to sign in as the fix.

// Fixes: the actions the app knows how to take.
const (
	fixDaemonRestart     = "daemon restart"
	fixStatuslineInstall = "statusline install"
	fixKeychainAllow     = "keychain allow"
	fixSigninPrefix      = "signin "
)

// refreshSoon is how close a refresh token's expiry must be to warn: the
// figure the re-login reminder uses (IMPROVEMENTS F5).
const refreshSoon = 5 * 24 * time.Hour

// doctorGOOS is runtime.GOOS, a seam: "keychain allow" is a macOS fix.
var doctorGOOS = runtime.GOOS

// doctorCheck is one check of the JSON report.
type doctorCheck struct {
	name, status, message string
	fix                   string // "" is null
	level                 string // the text's mark: ok, warn, fail or info
	details               []string
	account, profile      string
}

func (c doctorCheck) json() map[string]any {
	details := c.details
	if details == nil {
		details = []string{}
	}
	return map[string]any{"name": c.name, "status": c.status, "message": c.message, "fix": orNull(c.fix),
		"level": c.level, "details": details, "account": orNull(c.account), "profile": orNull(c.profile)}
}

// doctorReport collects what the text cannot carry: the account checks,
// each at the offset of the text written when it was made, so it follows
// the row it details.
type doctorReport struct {
	extra  []placedCheck
	failed int
}

type placedCheck struct {
	at int
	c  doctorCheck
}

// add records an account check made after at bytes of text. Nil-safe: the
// text form runs with no report.
func (r *doctorReport) add(at int, c doctorCheck) {
	if r != nil {
		r.extra = append(r.extra, placedCheck{at, c})
	}
}

// doctorJSON is `cs doctor [--verify] --json`. Failed checks are in the
// report (and counted in "failed"); the exit status is 0 whenever the report
// was made, since an error exit is an error object for the app.
func doctorJSON(w io.Writer, cfgPath string, deep bool) error {
	rep := &doctorReport{}
	var text bytes.Buffer
	_ = runDoctorTo(&text, cfgPath, deep, rep)
	checks := []map[string]any{}
	for _, c := range rep.checks(text.String()) {
		checks = append(checks, c.json())
	}
	return emitTo(w, map[string]any{"checks": checks, "failed": rep.failed})
}

// checks reads doctor's text into checks, the account checks placed after
// the row they belong to.
func (r *doctorReport) checks(text string) []doctorCheck {
	var out []doctorCheck
	extra := r.extra
	flush := func(upTo int) {
		for len(extra) > 0 && extra[0].at <= upTo {
			out = append(out, extra[0].c)
			extra = extra[1:]
		}
	}
	cur := -1   // index in out of the row taking details, -1 for none
	offset := 0 // bytes of text before this line
	for _, line := range strings.SplitAfter(text, "\n") {
		start := offset
		offset += len(line)
		line = strings.TrimRight(line, "\n")
		if mark, rest, ok := doctorRow(line); ok {
			flush(start)
			if mark == "" {
				cur = -1 // a header, such as --verify's "verifying…"
				continue
			}
			out = append(out, rowCheck(mark, rest))
			cur = len(out) - 1
			continue
		}
		detail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "└"))
		if cur < 0 || detail == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "└") || len(out[cur].details) == 0 {
			out[cur].details = append(out[cur].details, detail)
		} else {
			// A line continuing the detail above it.
			out[cur].details[len(out[cur].details)-1] += "\n" + detail
		}
	}
	flush(len(text))
	return out
}

// doctorRow splits "  [mark] name   message" into its mark and the rest.
func doctorRow(line string) (mark, rest string, ok bool) {
	if !strings.HasPrefix(line, "  [") || len(line) < 9 || line[7] != ']' {
		return "", "", false
	}
	return strings.TrimSpace(line[3:7]), strings.TrimPrefix(line[8:], " "), true
}

// rowCheck is one text row as a check. A name is everything before the
// first run of two spaces.
func rowCheck(mark, rest string) doctorCheck {
	name, msg := rest, ""
	if i := strings.Index(rest, "  "); i >= 0 {
		name, msg = rest[:i], strings.TrimSpace(rest[i:])
	}
	c := doctorCheck{name: name, message: msg, level: strings.ToLower(mark)}
	switch c.level {
	case "ok":
		c.status = "ok"
	case "fail":
		c.status = "fail"
	default:
		// A warning, and the notes ([info]) that name something to do.
		c.status = "warn"
	}
	switch {
	case c.name == "daemon" && c.status == "fail":
		c.fix = fixDaemonRestart
	case c.name == "credentials" && c.status == "fail" && c.message == keychain.Backend && doctorGOOS == "darwin":
		c.fix = fixKeychainAllow
	case c.name == "status line" && c.level == "info":
		c.fix = fixStatuslineInstall
	case c.name == "profile":
		c.profile, _, _ = strings.Cut(c.message, " ")
	}
	return c
}

// refreshCheck is one vaulted account's refresh token: a warning, signing
// the account in, when it is gone, expired, or expires within refreshSoon.
func refreshCheck(id string, o *keychain.OAuth, line string, now time.Time) doctorCheck {
	c := doctorCheck{name: "refresh token", status: "ok", level: "ok", account: id,
		message: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "└"))}
	e := o.RefreshExpiry()
	if o.RefreshToken == "" || (!e.IsZero() && e.Sub(now) < refreshSoon) {
		c.status, c.level, c.fix = "warn", "warn", fixSigninPrefix+id
	}
	return c
}

// verifyCheck is --verify's verdict on one account.
func verifyCheck(id, status, line string) doctorCheck {
	c := doctorCheck{name: "credential", status: status, level: status, account: id,
		message: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "└"))}
	if status != "ok" {
		c.fix = fixSigninPrefix + id
	}
	return c
}
