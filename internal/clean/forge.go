package clean

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// PR is a pull request as the rules need it.
type PR struct {
	Number   int       `json:"number"`
	Head     string    `json:"headRefName"`
	HeadSHA  string    `json:"headRefOid"`
	Base     string    `json:"baseRefName"`
	State    string    `json:"state"`
	MergedAt time.Time `json:"mergedAt"`
}

// Forge lists a repository's pull requests: the merged ones and the open
// ones.
type Forge interface {
	PRs(ctx context.Context, repo string) ([]PR, error)
}

// forge is GitHub through the gh command; tests replace it.
var forge Forge = ghForge{}

type ghForge struct{}

// errNoGH is why a GitHub repository's pull requests were not read.
var errNoGH = errors.New("gh not found")

// ghDirs are where installers put gh, looked in after PATH: the daemon runs
// under launchd's PATH (/usr/bin:/bin:/usr/sbin:/sbin), which has none of
// them, and its first run checked no pull request (2026-10-26).
func ghDirs(home string) []string {
	return []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin",
		filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin")}
}

// findGH is gh on pathEnv, else in the first of dirs that has it; "" for
// none.
func findGH(pathEnv string, dirs []string) string {
	for _, d := range append(filepath.SplitList(pathEnv), dirs...) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, "gh")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// PRs asks gh for the repository's merged and open pull requests. A
// repository not on GitHub has none. Without gh, or when it fails, the
// error says so: then only merges into the default branch count, which
// keeps more, never less, and the plan says why.
func (ghForge) PRs(ctx context.Context, repo string) ([]PR, error) {
	slug := githubSlug(originURL(ctx, repo))
	if slug == "" {
		return nil, nil
	}
	home, _ := os.UserHomeDir()
	gh := findGH(os.Getenv("PATH"), ghDirs(home))
	if gh == "" {
		return nil, errNoGH
	}
	var all []PR
	for _, q := range []struct {
		state, limit, fields string
	}{
		{"merged", "5000", "number,headRefName,headRefOid,baseRefName,state,mergedAt"},
		{"open", "1000", "number,headRefName,headRefOid,baseRefName,state"},
	} {
		cmd := exec.CommandContext(ctx, gh, "pr", "list", "-R", slug, "--state", q.state, "--limit", q.limit,
			"--json", q.fields)
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
				return nil, errors.New("gh: " + string(bytes.SplitN(msg, []byte("\n"), 2)[0]))
			}
			return nil, err
		}
		var prs []PR
		if err := json.Unmarshal(out.Bytes(), &prs); err != nil {
			return nil, err
		}
		all = append(all, prs...)
	}
	return all, nil
}
