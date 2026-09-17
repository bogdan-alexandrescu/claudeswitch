//go:build darwin

package credstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Backend names where credentials live on this platform, for doctor output.
const Backend = "macOS Keychain"

// readTimeout bounds every call to security(1).
//
// Without it a Keychain read can block forever. macOS ties an item's access
// control to the exact binary that was approved, so a rebuilt binary is a
// stranger and prompts — and under launchd there is no GUI session to answer,
// so `security` waits indefinitely. The daemon then sits at 0% CPU with a child
// process, having made no API calls and logged nothing, and the watchdog never
// fires because it never got far enough to notice it was blind.
//
// Observed for hours on 2026-09-11. A timeout turns a hang into an error, which
// the rest of the program already knows how to report.
// A keychain read costs ~0.1s from a shell but ~2s from a launchd agent, and
// spikes well above that under load. The timeout only exists to turn a hang
// into a diagnosable error, so it is set far above any healthy read.
const readTimeout = 30 * time.Second

// Read returns the parsed blob for a service.
func Read(service string) (*Blob, error) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-w")
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf(
			"reading keychain item %q timed out after %s.\n"+
				"  The usual cause is a service definition with ProcessType set to\n"+
				"  Background: in that QoS band a `security` child never completes a\n"+
				"  read at all. Run `claudeswitch doctor` to check.\n"+
				"  Failing that, macOS may be asking to approve access with nobody\n"+
				"  there to answer — run `claudeswitch status` in a terminal once and\n"+
				"  click Always Allow: %w",
			service, readTimeout, ErrUnavailable)
	}
	if err != nil {
		return nil, fmt.Errorf("reading keychain item %q (is it present, and did you approve access?): %w",
			service, err)
	}
	return parse(service, out)
}

// selfPath is the installed binary, resolved through any symlink, so the access
// control names a stable path rather than whatever `os.Args[0]` happened to be.
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

func currentUser() string {
	out, err := exec.Command("id", "-un").Output()
	if err != nil {
		return "claudeswitch"
	}
	return strings.TrimSpace(string(out))
}

// escapeForSecurity quotes a value for security(1)'s interactive parser.
func escapeForSecurity(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// Write stores a blob, replacing any existing item.
//
// The secret goes through security(1)'s interactive mode on STDIN rather than on
// the command line: an argv-borne token is visible to every process on the
// machine via ps.
func Write(service string, b *Blob) error {
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	// -T names an application allowed to read this item without prompting. It
	// is set when a vault item is CREATED, and never when one is updated.
	//
	// Passing it on an update changes the item's access list, and changing an
	// access list always asks for approval. After a rebuild that meant a
	// prompt on every token refresh — at 23:53 on 2026-09-16 with nobody
	// there. The write timed out, which reported a stored token as lost, and
	// the unanswered prompt left securityd at its thread limit, so no keychain
	// read on the machine completed for the next hour (ground truth §41).
	//
	// Updating the content alone raises no prompt: reads go through
	// /usr/bin/security, which is what the access list is checked against, and
	// the log that night showed no read prompts at all.
	//
	// This applies only to claudeswitch's own vault items. Claude Code's live
	// credential is left alone: rewriting its access control to suit us could
	// break Claude Code's own access, which is not ours to risk.
	trust := ""
	if IsVaultService(service) && !itemExists(service) {
		trust = selfPath()
	}
	timedOut, stderr, err := runSecurity(addCommand(service, currentUser(), trust, string(payload)))
	if err != nil {
		if timedOut {
			// The content can be stored even though the command never
			// returned — observed: the token landed and only a later step was
			// left waiting. Check before calling the credential lost, because
			// "lost" sends someone to log in again for nothing.
			if readBack(service, b) == nil {
				return nil
			}
			return fmt.Errorf("writing keychain item %q timed out — approval is probably "+
				"being asked for; run a claudeswitch command in a terminal and click "+
				"Always Allow: %w", service, ErrUnavailable)
		}
		// Never include the payload in an error.
		return fmt.Errorf("writing keychain item %q: %w (%s)", service, err, stderr)
	}
	return readBack(service, b)
}

// addCommand is the security(1) interactive command that stores payload. An
// empty trust leaves the item's access list as it is.
func addCommand(service, account, trust, payload string) string {
	t := ""
	if trust != "" {
		t = " -T " + escapeForSecurity(trust)
	}
	return fmt.Sprintf("add-generic-password -U -s %s -a %s%s -w %s\n",
		escapeForSecurity(service), escapeForSecurity(account), t, escapeForSecurity(payload))
}

// Seams, so tests never reach the real keychain: a test that did would raise
// the very prompts this file exists to avoid.
var (
	itemExists  = securityItemExists
	runSecurity = runSecurityInteractive
	readBack    = verifyWrite
)

// securityItemExists reports whether an item is already stored. It reads
// attributes only, which needs no approval. Anything but a clear "not found"
// counts as present, since the cost of wrongly thinking so is only a missing
// -T, while the cost of wrongly thinking otherwise is a prompt.
func securityItemExists(service string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
		return false
	}
	return true
}

func runSecurityInteractive(stdin string) (timedOut bool, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "security", "-i")
	c.Stdin = strings.NewReader(stdin)
	var errb bytes.Buffer
	c.Stderr = &errb
	err = c.Run()
	return ctx.Err() == context.DeadlineExceeded, strings.TrimSpace(errb.String()), err
}

// Delete removes an item. Missing is not an error.
func Delete(service string) error {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "security", "delete-generic-password", "-s", service).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "could not be found") {
		return fmt.Errorf("deleting keychain item %q: %w", service, err)
	}
	return nil
}
