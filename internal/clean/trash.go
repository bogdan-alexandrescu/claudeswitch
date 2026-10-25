package clean

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// trashDir is the platform's Trash for files: ~/.Trash on macOS, the
// freedesktop.org one elsewhere.
func trashDir(opt Options) (dir string, xdg bool, err error) {
	if opt.TrashDir != "" {
		return opt.TrashDir, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash"), false, nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash"), true, nil
}

// toTrash moves a folder to the Trash under a name that says which run put
// it there, and returns where it went. It only renames: a folder on another
// volume than the Trash is refused rather than copied.
func toTrash(opt Options, run, path string) (string, error) {
	dir, xdg, err := trashDir(opt)
	if err != nil {
		return "", err
	}
	files := dir
	if xdg {
		files = filepath.Join(dir, "files")
	}
	if err := os.MkdirAll(files, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s (cs clean %s)", filepath.Base(path), run)
	dst := filepath.Join(files, name)
	for i := 2; exists(dst); i++ {
		dst = filepath.Join(files, fmt.Sprintf("%s %d", name, i))
	}
	if xdg {
		info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", (&url.URL{Path: path}).EscapedPath(),
			time.Now().Format("2006-01-02T15:04:05"))
		infoDir := filepath.Join(dir, "info")
		if err := os.MkdirAll(infoDir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(infoDir, filepath.Base(dst)+".trashinfo"), []byte(info), 0o600); err != nil {
			return "", err
		}
	}
	if err := os.Rename(path, dst); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return "", fmt.Errorf("it is on another volume than the Trash; left in place")
		}
		return "", err
	}
	return dst, nil
}
