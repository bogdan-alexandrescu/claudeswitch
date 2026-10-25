package clean

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
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

// PRs asks gh for the repository's merged and open pull requests. A
// repository not on GitHub, or no gh, has none: then only merges into the
// default branch count, which keeps more, never less.
func (ghForge) PRs(ctx context.Context, repo string) ([]PR, error) {
	slug := githubSlug(originURL(ctx, repo))
	if slug == "" {
		return nil, nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, nil
	}
	var all []PR
	for _, q := range []struct {
		state, limit, fields string
	}{
		{"merged", "5000", "number,headRefName,headRefOid,baseRefName,state,mergedAt"},
		{"open", "1000", "number,headRefName,headRefOid,baseRefName,state"},
	} {
		cmd := exec.CommandContext(ctx, "gh", "pr", "list", "-R", slug, "--state", q.state, "--limit", q.limit,
			"--json", q.fields)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
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
