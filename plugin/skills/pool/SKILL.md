---
name: pool
user-invocable: false
description: Show and change claudeswitch's pools — which accounts each pool holds and which Claude Code profiles use it — and share accounts between profiles. Use when asked "which profiles can use X", "share this account with my other profile", "move X to another pool", or to create, rename or delete a pool.
allowed-tools:
  - Bash(claudeswitch pool:*)
  - Bash(claudeswitch profile pools:*)
  - Bash(claudeswitch profile list:*)
  - AskUserQuestion
---

# claudeswitch pool

Reached as `/cs pool [args]`. An account is in one pool. A profile lists the
pools it uses, in order, and takes an account from a later pool only when no
account in an earlier one is usable. Profiles that list the same pool share
its accounts, one profile at a time: an account live in one shows as "in use
by" it in the others, which take another.

## 1. Look

```bash
claudeswitch pool list
```

This changes nothing. It lists every pool with its accounts and the profiles
that use it (a profile's own pool goes by the profile's name), then the
accounts in no pool, which the profile named `default` uses after its pools.
`claudeswitch profile list` shows each profile's pools in the order it uses
them. With no arguments, or a question, answer from these and stop.

## 2. Change

Every other command edits the config, so **confirm first** with
AskUserQuestion: say what changes and which profiles gain or lose which
accounts, and include an option to cancel.

| to | run |
|---|---|
| make a pool | `claudeswitch pool create <pool> [<account>...]` (accounts in no pool; move others in after) |
| move an account into another pool | `claudeswitch pool move <account> --to <pool>` |
| put an account in no pool into a pool | `claudeswitch pool add <pool> <account>` |
| take an account out of its pool | `claudeswitch pool remove <pool> <account>` (`default` then uses it last) |
| rename a pool | `claudeswitch pool rename <pool> <new-name>` |
| delete a pool | `claudeswitch pool delete <pool> --to <other-pool>`, or `--no-pool` |
| set the pools a profile uses, in order | `claudeswitch profile pools <profile> <pool>...` |

No profile uses a new pool until `profile pools` lists it. A profile's own
pool leads its list unless the list names it somewhere else.

To share an account that is in one profile's own pool with another profile,
do it in this order, so the profile using it never loses it: create the
pool, add it to that profile's list (`profile pools <profile> <pool>`), move
the account into it, then add the pool to the other profile's list.

## 3. Report

Pass on what the command printed. A running daemon picks the change up by
itself; no restart is needed.

A refusal changes nothing. Say what its message says:

- `live` — the edit would leave an account in use in a profile that could no
  longer use it. Suggest switching that profile off it first (`/cs switch`);
- `in_other_pool` — the account is already in another pool: move it instead;
- `would_orphan` — the account would be in no pool with no profile named
  `default` to use it;
- `not_found`, `invalid_value` — a pool or profile that does not exist, or a
  name already taken.
