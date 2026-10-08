//go:build darwin

package credstore

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// I4: security -i truncates long input lines. A truncated write stores a
// broken credential, and putting the secret on argv instead would show it to
// every process on the machine (DESIGN 4.2). So an oversized write is refused,
// with the size in the error, and nothing is run.
func TestAnOversizedWriteIsRefusedWithoutRunningSecurity(t *testing.T) {
	f := &fakeKeychain{exists: true}
	f.install(t)
	b := vaultBlob()
	b.ClaudeAIOAuth.RefreshToken = strings.Repeat("x", SecurityLineMax)

	err := Write(VaultService("work"), b)
	if err == nil {
		t.Fatal("an oversized write was accepted")
	}
	if len(f.commands) != 0 {
		t.Fatalf("security was run with an oversized line")
	}
	line := addCommand(VaultService("work"), currentUser(), "", mustJSON(t, b))
	for _, want := range []string{fmt.Sprint(len(line)), fmt.Sprint(SecurityLineMax), "argv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "xxxxxxxx") || strings.Contains(err.Error(), "new-token") {
		t.Error("the error carries the secret")
	}
}

// The measurement is of the whole line as security reads it, escapes and
// newline included: a line exactly at the limit goes through, one byte more
// does not.
func TestTheLimitIsMeasuredOnTheWholeLine(t *testing.T) {
	f := &fakeKeychain{exists: true}
	f.install(t)
	b := vaultBlob()
	base := len(addCommand(VaultService("work"), currentUser(), "", mustJSON(t, b)))
	b.ClaudeAIOAuth.RefreshToken = strings.Repeat("y", SecurityLineMax-base)
	if n := len(addCommand(VaultService("work"), currentUser(), "", mustJSON(t, b))); n != SecurityLineMax {
		t.Fatalf("fixture line is %d bytes, want exactly %d", n, SecurityLineMax)
	}
	if err := Write(VaultService("work"), b); err != nil {
		t.Fatalf("a line at the limit was refused: %v", err)
	}
	b.ClaudeAIOAuth.RefreshToken += "y"
	if err := Write(VaultService("work"), b); err == nil {
		t.Fatal("a line one byte over the limit was accepted")
	}
}

// A swap asks before writing whether a blob fits, for the blob it would roll
// back to as well as the one it installs.
func TestALiveItemSaysWhetherABlobFits(t *testing.T) {
	c, ok := LiveItem("Claude Code-credentials-abcd1234", "").(WriteChecker)
	if !ok {
		t.Fatal("a live item cannot check a write")
	}
	if err := c.CheckWrite(vaultBlob()); err != nil {
		t.Fatalf("an ordinary credential does not fit: %v", err)
	}
	big := vaultBlob()
	big.ClaudeAIOAuth.RefreshToken = strings.Repeat("z", SecurityLineMax)
	if err := c.CheckWrite(big); err == nil {
		t.Fatal("an oversized credential fits")
	}
	if _, ok := EnvLive().(WriteChecker); !ok {
		t.Fatal("the environment's item cannot check a write")
	}
}

func mustJSON(t *testing.T, b *Blob) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSecurityIsRunByAbsolutePath(t *testing.T) {
	if defaultSecurityBinary != "/usr/bin/security" {
		t.Fatalf("security binary %q", defaultSecurityBinary)
	}
	src, err := os.ReadFile("store_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`exec\.Command(Context)?\([^)]*"security"`).Find(src); m != nil {
		t.Fatalf("security run by name, found through PATH: %s", m)
	}
}
