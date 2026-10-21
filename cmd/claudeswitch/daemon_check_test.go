package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// checkItem is a profile's live item as `daemon check` meets it: readable,
// or failing with err. Each read is recorded by profile name.
type checkItem struct {
	profile string
	err     error
	reads   *[]string
}

func (c checkItem) Name() string { return "item-" + c.profile }
func (c checkItem) Read() (*keychain.Blob, error) {
	*c.reads = append(*c.reads, c.profile)
	if c.err != nil {
		return nil, c.err
	}
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + c.profile}}, nil
}
func (c checkItem) Write(*keychain.Blob) error { return errors.New("daemon check never writes") }

// stubCheckItems makes every profile's item readable except those named in
// readErr (whose read fails) and resolveErr (which do not resolve at all). It
// returns the profiles read, in order, and the profiles resolved.
func stubCheckItems(t *testing.T, readErr, resolveErr map[string]error) (reads *[]string, resolved *[]config.Profile) {
	t.Helper()
	reads, resolved = &[]string{}, &[]config.Profile{}
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) {
		*resolved = append(*resolved, in)
		if err := resolveErr[in.Name]; err != nil {
			return nil, err
		}
		return checkItem{profile: in.Name, err: readErr[in.Name], reads: reads}, nil
	}
	return reads, resolved
}

// twoDirProfiles declares two profiles that both set CLAUDE_CONFIG_DIR, and
// none standing for it unset: the shape where install.sh's old whoami check
// refused before reading anything.
func twoDirProfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	return writeConfig(t, fmt.Sprintf(`
[[account]]
id = "w1"
[[account]]
id = "w2"

[[profile]]
name = "work"
dir  = %q
pool = ["w1"]

[[profile]]
name = "side"
dir  = %q
pool = ["w2"]
`, filepath.Join(root, ".claude-work"), filepath.Join(root, ".claude-side")))
}

// The daemon reads every profile's item unattended, so the check reads every
// one, whatever this shell's CLAUDE_CONFIG_DIR says.
func TestDaemonCheckReadsEveryProfileWhateverTheShell(t *testing.T) {
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	reads, _ := stubCheckItems(t, nil, nil)
	var out bytes.Buffer
	if err := daemonCheck(&out, twoDirProfiles(t), false); err != nil {
		t.Fatalf("check failed with every item readable: %v\n%s", err, out.String())
	}
	if want := []string{"work", "side"}; !reflect.DeepEqual(*reads, want) {
		t.Errorf("read %v, want every profile's item: %v", *reads, want)
	}
	for _, name := range []string{"work", "side", "item-work", "item-side"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("output does not name %q:\n%s", name, out.String())
		}
	}
}

// A profile nobody has signed in to yet holds nothing to approve; it must not
// stop the daemon being installed.
func TestDaemonCheckSkipsAProfileNotSignedInYet(t *testing.T) {
	notYet := fmt.Errorf("no Claude Code credential found for config dir: %w", keychain.ErrNotFound)
	reads, _ := stubCheckItems(t, nil, map[string]error{"side": notYet})
	var out bytes.Buffer
	if err := daemonCheck(&out, twoDirProfiles(t), false); err != nil {
		t.Fatalf("a profile not signed in yet failed the check: %v\n%s", err, out.String())
	}
	if want := []string{"work"}; !reflect.DeepEqual(*reads, want) {
		t.Errorf("read %v, want %v", *reads, want)
	}
	if !strings.Contains(out.String(), "side") {
		t.Errorf("output does not mention the skipped profile:\n%s", out.String())
	}
}

// A read that fails for any other reason (a denied Keychain prompt, a store
// that did not answer) fails the check, and says what actually happened and
// where, rather than leaving the reader to guess.
func TestDaemonCheckReportsTheRealErrorByProfile(t *testing.T) {
	denied := errors.New("security: SecKeychainSearchCopyNext: user interaction is not allowed")
	stubCheckItems(t, map[string]error{"side": denied}, nil)
	var out bytes.Buffer
	err := daemonCheck(&out, twoDirProfiles(t), false)
	if err == nil {
		t.Fatalf("an unreadable item passed the check:\n%s", out.String())
	}
	all := out.String() + err.Error()
	for _, want := range []string{"side", "user interaction is not allowed"} {
		if !strings.Contains(all, want) {
			t.Errorf("neither output nor error carries %q:\n%s\nerror: %v", want, out.String(), err)
		}
	}
}

// Before setup there is no config: install.sh still gets the Keychain prompt
// for the credential Claude Code uses in this environment, as it always has.
func TestDaemonCheckWithNoConfigReadsTheEnvironmentsItem(t *testing.T) {
	reads, resolved := stubCheckItems(t, nil, nil)
	var out bytes.Buffer
	if err := daemonCheck(&out, filepath.Join(t.TempDir(), "absent.toml"), false); err != nil {
		t.Fatalf("check failed with no config: %v\n%s", err, out.String())
	}
	if len(*resolved) != 1 || !(*resolved)[0].FromEnv {
		t.Fatalf("resolved %+v, want the one profile this environment names", *resolved)
	}
	if len(*reads) != 1 {
		t.Errorf("read %v, want the environment's item once", *reads)
	}
}

// --json, for the app: one row per profile, and the failures counted rather
// than turned into an error, as doctor --json does.
func TestDaemonCheckJSONRowsPerProfile(t *testing.T) {
	notYet := fmt.Errorf("not signed in: %w", keychain.ErrNotFound)
	root := t.TempDir()
	path := writeConfig(t, fmt.Sprintf(`
[[account]]
id = "w1"
[[account]]
id = "w2"
[[account]]
id = "w3"

[[profile]]
name = "work"
dir  = %q
pool = ["w1"]

[[profile]]
name = "side"
dir  = %q
pool = ["w2"]

[[profile]]
name = "fresh"
dir  = %q
pool = ["w3"]
`, filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")))
	stubCheckItems(t, map[string]error{"side": errors.New("denied")}, map[string]error{"fresh": notYet})
	var out bytes.Buffer
	if err := daemonCheck(&out, path, true); err != nil {
		t.Fatalf("--json returned an error instead of counting the failure: %v", err)
	}
	var got struct {
		Profiles []struct {
			Profile string  `json:"profile"`
			Item    *string `json:"item"`
			Status  string  `json:"status"`
			Error   *string `json:"error"`
		} `json:"profiles"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.Failed != 1 {
		t.Errorf("failed = %d, want 1", got.Failed)
	}
	type row struct{ profile, item, status, err string }
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	var rows []row
	for _, r := range got.Profiles {
		rows = append(rows, row{r.Profile, str(r.Item), r.Status, str(r.Error)})
	}
	want := []row{
		{"work", "item-work", "ok", "<null>"},
		{"side", "item-side", "fail", "denied"},
		{"fresh", "<null>", "skip", "not signed in: no such credential"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows\n got %+v\nwant %+v", rows, want)
	}
}

// `cs daemon check` reaches the check: it reads before any service file is
// looked at, so it works where the service is not installed yet.
func TestDaemonCheckVerbRunsTheCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	reads, _ := stubCheckItems(t, nil, nil)
	var out bytes.Buffer
	if err := runDaemonCmd(&out, "check", "", false); err != nil {
		t.Fatalf("daemon check: %v\n%s", err, out.String())
	}
	if len(*reads) != 1 {
		t.Errorf("read %v, want the one profile's item", *reads)
	}
}
