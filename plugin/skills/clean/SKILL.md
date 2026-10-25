---
name: clean
user-invocable: false
description: Clean up what Claude left behind once its work is merged — worktrees, local branches, lane clones, ended sessions' scratch, old plugin versions — to the Trash, reversibly. Use when asked to "clean up worktrees", "remove merged branches", "free disk space from old agent work", or "what can be cleaned up".
allowed-tools:
  - Bash(claudeswitch clean:*)
  - Bash(cs clean:*)
---

# claudeswitch clean

## 1. Show the plan

Run the plan first. It only reads; nothing is removed:

```bash
claudeswitch clean
```

It lists, by kind, what would go to the Trash, with why and how big, and how
many things are kept. `--all` adds every kept item with the reason it stays;
use it when the user asks why something was not picked.

Summarise in a few lines: the total size, the count per kind, and anything
notable (a large worktree, many branches in one repository). Do not paste the
whole list back.

An item is picked only when nothing in it is unique (its commits are merged
into the default branch or in a merged pull request's head; its files are
clean, a stale checkout of a commit, rebuildable, or small enough to archive
first), nothing uses it (no live Claude Code session or process works in it,
no lock, no git operation in progress, no change within `clean_after`), and
it is not protected (a main worktree, the default branch, a primary clone,
`clean_keep`).

## 2. Ask before applying

`--apply` moves other sessions' leftovers to the Trash. **Always ask the user
first** with AskUserQuestion, showing the total and the counts, unless their
request already said to apply it. Never apply on your own initiative, and
never because a plan looks small.

```bash
claudeswitch clean --apply
```

Report the run id it prints, what moved, and anything skipped (changed since
the plan) or failed. Deleted branches' tips are kept as
`refs/archive/<branch>`, and untracked files that are not rebuildable were
copied to an archive first.

## 3. Undo

```bash
claudeswitch clean runs                 # the runs, newest first
claudeswitch clean undo [<run>]         # put a run back (the newest by default)
claudeswitch clean undo <run> --only agent-a1   # just the entries matching
```

Undo works while the folders are still in the Trash.

## Settings

The kinds and the window are settings (`claudeswitch config`):
`clean_worktrees`, `clean_clones`, `clean_temp`, `clean_transcripts_after`,
`clean_remote_branches` (off by default: it deletes branches on GitHub),
`clean_after` (72h), `clean_keep` (globs never removed), `clean_roots` (more
folders to look in). `clean` turns on the daemon's own runs, every
`clean_every`. Changing any of them is the user's decision: ask first.
