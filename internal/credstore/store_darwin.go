//go:build darwin

package credstore

import (
	"bytes"
	"context"
	"encoding/json"
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
				"  click Always Allow.",
			service, readTimeout)
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
	// -T names an application allowed to read this item without prompting.
	//
	// Without it, macOS asks for approval on every read from a binary it does
	// not recognise — and a daemon under launchd has no GUI session to answer,
	// so the read hangs until it times out and the daemon is left blind. The
	// approval is matched on path for an unsigned binary, so naming the install
	// path survives rebuilds, which is exactly what kept breaking.
	//
	// This applies only to claudeswitch's own vault items. Claude Code's live
	// credential is left alone: rewriting its access control to suit us could
	// break Claude Code's own access, which is not ours to risk.
	trust := ""
	if IsVaultService(service) {
		if self := selfPath(); self != "" {
			trust = " -T " + escapeForSecurity(self)
		}
	}
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s%s -w %s\n",
		escapeForSecurity(service), escapeForSecurity(currentUser()), trust,
		escapeForSecurity(string(payload)))

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "security", "-i")
	c.Stdin = strings.NewReader(cmd)
	var errb bytes.Buffer
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("writing keychain item %q timed out — approval is probably being "+
				"asked for; run a claudeswitch command in a terminal and click Always Allow", service)
		}
		// Never include the payload in an error.
		return fmt.Errorf("writing keychain item %q: %w (%s)", service, err, strings.TrimSpace(errb.String()))
	}
	return verifyWrite(service, b)
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
