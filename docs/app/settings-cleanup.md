# Settings → Cleanup

What Claude left behind once its work merged (worktrees, branches, lane
clones, ended sessions' scratch, old plugin versions), moved to the Trash,
reversibly.

See also: [cs clean](../cli/clean.md) · [cs clean → the rules](../cli/clean.md#the-rules) · [Settings → Daemon](settings-daemon.md)

<p align="center"><img src="../images/settings-cleanup.png" width="880" alt="Settings, Cleanup pane: a plan with Worktrees 1 · 512 MB, Records of worktrees whose folder is gone 1, Local branches 1, Ended sessions' scratch 1 · 3 MB, Each item and why, 1 kept, and Clean now…; Runs with two runs and Undo; then the Schedule settings"></p>

**Preview** runs `cs clean --json`: it looks through every repository Claude
Code has run in, in every profile, and changes nothing. It can take a minute
on a machine with hundreds of worktrees. The plan lists what would go by
kind, with counts and sizes; **Each item, and why** opens the list (the first
500), and the line under it counts what is kept: unmerged, in use, protected
or changed recently. `cs clean --all` in a terminal says why for each one.

**Clean now…** asks first, with the total and the counts, then runs
`cs clean --apply --json`, which judges each item again before it moves:
anything that changed since the preview stays. The result says what moved,
what stayed and what failed.

**Runs** lists the last five runs, newest first, with what each removed and
what has been put back. **Undo** runs `cs clean undo <run> --json`: branches
come back from `refs/archive`, folders from the Trash while they are still
there.

| row | key | what it does | default |
|---|---|---|---|
| **Clean up on a schedule** | `clean` | the daemon runs the cleanup itself, every **Every**; a dry-run daemon only plans | off |
| **Every** | `clean_every` | how often the scheduled cleanup runs | 24h (at least 1h) |
| **Only once unchanged for** | `clean_after` | only what has not changed (a commit, a merge, a file written) for this long | 72h |
| **Worktrees and branches** | `clean_worktrees` | merged worktrees and local branches, and git's records of worktrees whose folder is gone | on |
| **Lane clones** | `clean_clones` | second clones beside a project whose every branch is merged | on |
| **Temp files** | `clean_temp` | ended sessions' scratch folders, plugin versions no session uses, old subagent transcripts | on |
| **Subagent transcripts after** | `clean_transcripts_after` | with temp files on, an ended session's subagent transcripts this old go (0 keeps them) | 720h (30 days) |
| **Branches on GitHub** | `clean_remote_branches` | also delete a merged pull request's branch on GitHub, when it is exactly the merged tip | off |
| **Never remove** | `clean_keep` | globs matched against paths, folder names and branch names (comma-separated) | none |
| **Also look in** | `clean_roots` | more folders to look for repositories in (comma-separated) | none |

The on/off rows are switches; changing one runs `cs config set`, like every
other setting. With a CLI that predates cleanup the pane says it cannot read
the plan, and the rows are absent.
