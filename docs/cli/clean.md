# cs clean

Claude leaves things behind: a worktree for every agent and lane, a branch
for each, whole second clones for integrations, a scratch folder per
session, a copy of every plugin version it ever installed. `cs clean` finds
what is merged, unused and unchanged, and moves it to the Trash with a
journal, so a run can be put back.

```
cs clean [--all] [--json]          the plan: what would go, and why (changes nothing)
cs clean --apply [--json]          move it to the Trash
cs clean --plan <file> [--all]     show a plan saved with cs clean --json
cs clean --apply --plan <file>     move what that plan marks, and nothing else
cs clean runs [--json]             the runs, newest first
cs clean undo [<run>] [--only <text>] [--json]
                                   put a run back (the newest by default)
```

`cs clean` works by hand whatever the settings say about the daemon. With
[`clean`](#settings) on, the daemon runs it every `clean_every` (24 hours).

## What it looks at

- **Worktrees and local branches** of every repository Claude Code has run
  in, in every profile (the `projects` of each profile's `.claude.json`),
  plus any under `clean_roots`; and git's records of worktrees whose folder
  is already gone (`git worktree prune`).
- **Lane clones**: a second clone of one of those repositories beside it
  (`app-integrate-n` next to `app`). The clone named after its repository is
  the primary and is never removed; without one, nothing beside it is.
- **Claude's temp files**: the folder an ended session used in Claude Code's
  temp folder (`/tmp/claude-<uid>/<project>/<session>`), plugin versions in
  the plugin cache that no session can be using, and an ended session's
  `subagents/` transcripts older than `clean_transcripts_after` (30 days).
  The main transcripts are Claude Code's own to expire (its
  `cleanupPeriodDays`).
- **Branches on GitHub**, only with `clean_remote_branches` on: a merged pull
  request's branch on origin.

## The rules

Something goes only when every one of these holds; the plan says, for each
thing kept, which one did not.

1. **No unique commits.** Its commits are in the default branch, or in the
   head of a pull request that merged (`gh pr list`), under any branch name,
   and nothing was committed after. A clone also needs every branch to pass,
   no stash, no tag the primary lacks, no other refs, and no worktree of its
   own.
2. **No unique files.** `git status` is clean, or the folder is a *stale
   checkout*: its branch moved on and its index and files are exactly an
   older commit that stays reachable. Untracked and ignored files that a
   build or package manager makes again (`node_modules`, `.venv`, `dist`,
   `build`, caches, `*.tsbuildinfo`, `test-results`) are dropped; anything
   else, an agent's `scratchpad/` or a `.env` included, is copied to the
   archive first. More than 50 MB of those keeps the worktree.
3. **Not in use.** No live Claude Code session works inside it (any
   profile's `sessions/<pid>.json`, its process alive), no process has its
   working directory there, it is not locked (`git worktree lock`), no
   merge, rebase, cherry-pick, revert or bisect is in progress, and nothing
   in it changed (a commit, a merge, a file written, the branch moved)
   within `clean_after` (72 hours). A branch moving is read from its
   reflog's newest entry, not the file's time, which `git gc` rewrites.
   The lock Claude Code puts on an agent's worktree (`claude agent <name>
   (pid N start <time>)`) stops counting once that process has ended, or
   its pid belongs to a process started at another time; any other lock
   keeps the worktree.
4. **Not protected.** Not a repository's main worktree, not the default
   branch, not a primary clone, not matched by `clean_keep`.

`--apply` judges each item again just before acting on it, and leaves one
that changed since the plan (`skipped`).

## Reading a plan before it runs

`cs clean --apply` makes a new plan and acts on it, so it also removes what
became removable since you last looked. To remove exactly what you read,
save the plan and apply that file:

```bash
cs clean --json > plan.json          # one scan
cs clean --plan plan.json --all      # read it: every item, removed and kept
cs clean --apply --plan plan.json    # acts on that plan's items alone
```

Each item is still judged again just before it moves: one that changed, is
gone, or whose kind was turned off since stays (`skipped`). Nothing the
plan does not list is touched. `--plan -` reads the plan from stdin. The
app's **Clean now…** works this way with the plan it shows. The daemon's
runs make their own plan each time.

## What --apply does

- **Folders go to the Trash** (`~/.Trash` on macOS, the freedesktop.org
  Trash on Linux) as `<name> (cs clean <run>)`. It only moves: a folder on
  another volume than the Trash is left in place. Finder's *Put Back* does
  not know these; `cs clean undo` does.
- **Branches are archived, then deleted**: the tip stays as
  `refs/archive/<branch>` in the repository, and the branch is deleted only
  if it still points there. A worktree's branch goes with it.
- **A worktree's git record** (`.git/worktrees/<id>`) is kept in
  `~/.local/state/claudeswitch/clean/admin/<run>/`, so undo can relink it.
- **Untracked files** that are not rebuildable are copied to
  `~/.local/state/claudeswitch/clean/archive/<run>/` before the folder goes.
  The copy stays after the Trash is emptied.
- **A branch on GitHub** is deleted with `git push origin --delete`, only if
  origin still has it at the merged tip and no open pull request targets it.
- **The journal** is `~/.local/state/claudeswitch/clean/runs/<run>.json`,
  written after every step. One run at a time: a second, from the CLI, the
  daemon or the app, is refused (`clean_running`).

Undo puts a run back newest first: branches from `refs/archive`, folders
from the Trash (while they are still there), worktree records, branches on
GitHub. `--only` limits it to entries whose path or branch contains the text.

## Example

```
$ cs clean
cs clean: what would go to the Trash (nothing is removed without --apply)

worktrees (3, 1.4 GB)
  worktree ~/src/app/.claude/worktrees/agent-a1 [lane/a1] — PR #212 merged; a stale checkout of 3f2a91c0d; scratchpad archived (512 MB)
  worktree ~/src/app/.claude/worktrees/agent-b7 [worktree-agent-b7] — merged into main (466 MB)
  worktree ~/src/app/.claude/worktrees/agent-c3 [lane/c3] — its commits are in merged pull requests (440 MB)

records of worktrees whose folder is gone (2)
  prune    ~/src/app/.claude/worktrees/agent-d9 — its folder is gone; git still lists it

local branches (41)
  ~/src/app                                                    41

ended sessions' scratch (4, 12 MB)
  scratch  /private/tmp/claude-501/-Users-me-src-app/5b1e… — the scratch folder of an ended session (3 MB)

12 kept (cs clean --all says why, one by one)
off: clean_remote_branches
cs clean --apply moves 1.4 GB to the Trash; cs clean undo puts a run back

$ cs clean --apply
cs clean: run 20261025-160212 moved 1.4 GB to the Trash: 3 worktrees, 2 records of worktrees whose folder is gone, 41 local branches, 4 ended sessions' scratch
  deleted branches' tips are kept as refs/archive/<branch>
cs clean undo 20261025-160212 puts it back
```

`--all` adds every kept item with its reason, for example `keep worktree
~/src/app/.claude/worktrees/agent-e2 [lane/e2] — 2 changed files match no
commit`, or `— a live Claude Code session (app-3f) works in it`.

## JSON

`cs clean --json` (the plan):

```json
{"plan": {"at": "2026-10-25T16:02:12-07:00",
          "items": [{"kind": "worktree", "path": "/Users/me/src/app/.claude/worktrees/agent-a1",
                     "repo": "/Users/me/src/app", "branch": "lane/a1", "sha": "9c0e…",
                     "remove": true, "why": "PR #212 merged; scratchpad archived",
                     "bytes": 536870912, "archive": ["scratchpad"],
                     "admin": "/Users/me/src/app/.git/worktrees/agent-a1", "state": "…"}]},
 "kinds": {"worktrees": true, "clones": true, "temp": true, "remote_branches": false}}
```

`kind` is `worktree`, `prune`, `branch`, `remote`, `clone`, `scratch`,
`plugin` or `transcript`. Kept items have `"remove": false` and the reason in
`why`.

`cs clean --apply --json`: `{"run": {"id", "at", "done": [entry…],
"skipped": [item…], "failed": [{…item, "error"}]}, "bytes"}`; an entry is the
item plus `trash`, `admin_to`, `archive_to`, `ref` and `undone`.

`cs clean runs --json`: `{"runs": [{"id", "at", "removed", "undone",
"bytes"}]}`. `cs clean undo --json`: `{"run": {…}}`.

`--plan` takes the `cs clean --json` answer as printed, or the bare
`{"at", "items"}` object inside it.

Errors: `clean_running` (another cleanup holds the lock), `not_found` (no
such run, or none yet), `usage` (also: a `--plan` file that cannot be read
or holds no plan).

## Settings

| setting | default | what it does |
|---|---|---|
| `clean` | `false` | the daemon cleans up every `clean_every` (a dry-run daemon only plans) |
| `clean_every` | `24h0m0s` | at least 1h |
| `clean_after` | `72h0m0s` | only what has not changed for this long |
| `clean_worktrees` | `true` | worktrees, local branches, records of missing worktrees |
| `clean_clones` | `true` | lane clones |
| `clean_temp` | `true` | ended sessions' scratch, unused plugin versions, old subagent transcripts |
| `clean_transcripts_after` | `720h0m0s` | with `clean_temp`; `0s` keeps them |
| `clean_remote_branches` | `false` | merged pull requests' branches on GitHub |
| `clean_keep` | empty | globs never removed, matched on paths, folder names and branch names |
| `clean_roots` | empty | more folders to look for repositories in, three levels down |

```bash
cs config clean true
cs config clean_keep '*-rel,release/*'
```

## Exit status

0 when the plan was made or the run finished (some items may have been
skipped or failed: the output says which); 1 on an error.
