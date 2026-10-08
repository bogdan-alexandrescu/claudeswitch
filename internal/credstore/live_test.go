package credstore

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func unsetenv(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

// fakeLookup replaces the metadata lookup so no test reaches the keychain.
// present lists the service names that exist; every lookup is recorded.
type fakeLookup struct {
	present map[string]bool
	err     error
	asked   []string
}

func (f *fakeLookup) install(t *testing.T) {
	t.Helper()
	old := liveLookup
	resetLiveCache()
	t.Cleanup(func() { liveLookup = old; resetLiveCache() })
	liveLookup = func(svc string) (bool, error) {
		f.asked = append(f.asked, svc)
		if f.err != nil {
			return false, f.err
		}
		return f.present[svc], nil
	}
}

func liveEnv(t *testing.T, home, cfg string) {
	t.Helper()
	t.Setenv("HOME", home)
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if cfg == "" {
		unsetenv(t, "CLAUDE_CONFIG_DIR")
	} else {
		t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	}
}

func resolve(t *testing.T) string {
	t.Helper()
	s, err := LiveService()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Vectors computed with `printf %s <dir> | shasum -a 256 | cut -c1-8`.
func TestLiveServiceNameVector(t *testing.T) {
	if got := liveServiceFor("/Users/x/.claude-work"); got != "Claude Code-credentials-74ed04d5" {
		t.Fatalf("liveServiceFor = %s", got)
	}
}

func TestLiveServiceIsBareWithoutLookupWhenUnset(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "")
	if got := resolve(t); got != "Claude Code-credentials" {
		t.Fatalf("LiveService() = %s", got)
	}
	if len(f.asked) != 0 {
		t.Fatalf("no lookup is needed with CLAUDE_CONFIG_DIR unset, asked %v", f.asked)
	}
}

func TestLiveServiceHashesTheConfigDir(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-55414178": true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	if got := resolve(t); got != "Claude Code-credentials-55414178" {
		t.Fatalf("LiveService() = %s", got)
	}
}

// The person launched Claude Code with the shell's expansion; claudeswitch got
// the literal ~. Only the absolute spelling exists.
func TestLiveServiceFindsTheExpandedSpelling(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-55414178": true}}
	f.install(t)
	liveEnv(t, "/h", "~/.claude-work")
	if got := resolve(t); got != "Claude Code-credentials-55414178" {
		t.Fatalf("LiveService() = %s", got)
	}
}

func TestLiveServiceFindsTheSpellingWithoutATrailingSlash(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-55414178": true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work/")
	if got := resolve(t); got != "Claude Code-credentials-55414178" {
		t.Fatalf("LiveService() = %s", got)
	}
}

// When several spellings exist, the one as given is what a Claude Code with
// this same environment would use.
func TestLiveServicePrefersTheSpellingAsGiven(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{
		"Claude Code-credentials-250d1b22": true, // ~/.claude-work
		"Claude Code-credentials-55414178": true, // /h/.claude-work
	}}
	f.install(t)
	liveEnv(t, "/h", "~/.claude-work")
	if got := resolve(t); got != "Claude Code-credentials-250d1b22" {
		t.Fatalf("LiveService() = %s", got)
	}
}

func TestLiveServiceNamesTheCandidatesWhenNoneExists(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "~/.claude-work/")
	_, err := LiveService()
	if err == nil {
		t.Fatal("no candidate exists, so there is no live item to resolve")
	}
	for _, want := range []string{"~/.claude-work/", "/h/.claude-work", "Claude Code-credentials-55414178"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestLiveServicePassesALookupFailureOn(t *testing.T) {
	f := &fakeLookup{err: ErrUnavailable}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	if _, err := LiveService(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a keychain that is not answering must not read as a missing item: %v", err)
	}
}

func TestSecureStorageDirOverridesTheHashedDir(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-bf2faee2": true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/other")
	if got := resolve(t); got != "Claude Code-credentials-bf2faee2" {
		t.Fatalf("LiveService() = %s", got)
	}
}

func TestSecureStorageDirSetButEmptyMeansNoSuffix(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	if got := resolve(t); got != "Claude Code-credentials" {
		t.Fatalf("LiveService() = %s", got)
	}
	if len(f.asked) != 0 {
		t.Fatalf("an unsuffixed name needs no lookup, asked %v", f.asked)
	}
}

// The daemon resolves on every poll; each lookup is a security(1) child.
func TestLiveServiceResolvesOncePerEnvironment(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-55414178": true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	resolve(t)
	n := len(f.asked)
	resolve(t)
	if len(f.asked) != n {
		t.Fatalf("second resolution looked up again: %v", f.asked)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/other")
	f.present["Claude Code-credentials-bf2faee2"] = true
	if got := resolve(t); got != "Claude Code-credentials-bf2faee2" {
		t.Fatalf("a changed environment must resolve afresh, got %s", got)
	}
}

// A profile never logged in has no suffixed item while the default one has
// the plain item. Still refused, never a fallback to the plain item, but the
// error says what is most likely wrong.
func TestNoCandidateWithThePlainItemPresentSaysLogIn(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials": true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	_, err := LiveService()
	if err == nil {
		t.Fatal("the plain item belongs to another profile and must not be used")
	}
	for _, want := range []string{"the plain item exists", "/login", "Claude Code-credentials-55414178"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
}

func TestNoCandidateWithoutThePlainItemSaysNothingOfIt(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	_, err := LiveService()
	if err == nil {
		t.Fatal("no candidate exists")
	}
	if strings.Contains(err.Error(), "plain item") {
		t.Errorf("the plain item is absent, so the hint is wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "Claude Code-credentials-55414178") {
		t.Errorf("error should still name the candidates: %v", err)
	}
}

// Claude Code hashes dir.normalize("NFC"), so a decomposed spelling (as macOS
// file APIs can return) names the same item as the composed one.
func TestLiveServiceNormalisesToNFC(t *testing.T) {
	// Escaped, not literal: an editor that normalises on save would turn literal
	// spellings into the same string and the test into a no-op.
	const nfc = "/x/caf\u00e9"  // U+00E9 as one code point
	const nfd = "/x/cafe\u0301" // e + U+0301 combining acute
	if nfc == nfd {
		t.Fatal("the NFC and NFD spellings are byte-identical; the test proves nothing")
	}
	if got := liveServiceFor(nfc); got != "Claude Code-credentials-6c1608c2" {
		t.Fatalf("liveServiceFor(NFC) = %s", got)
	}
	if liveServiceFor(nfd) != liveServiceFor(nfc) {
		t.Fatalf("NFD %s and NFC %s differ", liveServiceFor(nfd), liveServiceFor(nfc))
	}
}

func TestLiveServiceResolvesAnNFDConfigDir(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-6c1608c2": true}}
	f.install(t)
	liveEnv(t, "/h", "/x/cafe\u0301") // NFD, escaped as above
	if got := resolve(t); got != "Claude Code-credentials-6c1608c2" {
		t.Fatalf("LiveService() = %s", got)
	}
}

// 2.1.293: suffix = SECURESTORAGE !== undefined ? !!SECURESTORAGE : !!CLAUDE_CONFIG_DIR.
// A non-empty secure-storage dir gets a suffix even with CLAUDE_CONFIG_DIR unset.
func TestSecureStorageDirAloneStillSuffixes(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{"Claude Code-credentials-df59a83e": true}}
	f.install(t)
	liveEnv(t, "/h", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/keys")
	if got := resolve(t); got != "Claude Code-credentials-df59a83e" {
		t.Fatalf("LiveService() = %s", got)
	}
}
