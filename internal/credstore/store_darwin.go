//go:build darwin

package credstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Backend names where credentials live on this platform, for doctor output.
const Backend = "macOS Keychain"

// Read returns the parsed blob for a service.
func Read(service string) (*Blob, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("reading keychain item %q (is it present, and did you approve access?): %w",
			service, err)
	}
	return parse(service, out)
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
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n",
		escapeForSecurity(service), escapeForSecurity(currentUser()), escapeForSecurity(string(payload)))

	c := exec.Command("security", "-i")
	c.Stdin = strings.NewReader(cmd)
	var errb bytes.Buffer
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		// Never include the payload in an error.
		return fmt.Errorf("writing keychain item %q: %w (%s)", service, err, strings.TrimSpace(errb.String()))
	}
	return verifyWrite(service, b)
}

// Delete removes an item. Missing is not an error.
func Delete(service string) error {
	out, err := exec.Command("security", "delete-generic-password", "-s", service).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "could not be found") {
		return fmt.Errorf("deleting keychain item %q: %w", service, err)
	}
	return nil
}
