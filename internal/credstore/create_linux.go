//go:build linux

package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
)

// LiveExistsFor reports whether the profile with CLAUDE_CONFIG_DIR=dir has a
// credential file. A configured profile's secure storage is its dir.
func LiveExistsFor(dir string) (bool, error) {
	p, err := ccdir.For(dir)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(p.SecureStorage, ".credentials.json"))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// CreateLiveItem stores b as a new profile's credential file, 0600 from the
// start, and never replaces one: it is written beside the file and linked
// into place, which fails if the name is taken. service names it in errors.
func CreateLiveItem(service, file string, b *Blob) error {
	if file == "" {
		return fmt.Errorf("not creating %q: no credential file named", service)
	}
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".credentials.json.tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(name, file); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s: %w", file, ErrExists)
		}
		return err
	}
	got, err := readAt(service, file)
	if err != nil {
		return fmt.Errorf("created %s but could not read it back: %w", file, err)
	}
	if got.ClaudeAIOAuth.AccessToken != b.ClaudeAIOAuth.AccessToken {
		return fmt.Errorf("%s read back different from what was written; not trusting it", file)
	}
	return nil
}
