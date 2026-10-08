---
name: doctor
user-invocable: false
description: Diagnose a claudeswitch installation — config, vault, keychain access, usage API, daemon, refresh policy, status line and plugin — and explain how to fix what fails. Use when claudeswitch misbehaves, a command errors, or accounts are not polling.
allowed-tools:
  - Bash(claudeswitch doctor:*)
  - Bash(claudeswitch version:*)
  - Bash(tail:*)
---

# claudeswitch doctor

```bash
claudeswitch version
claudeswitch doctor
```

`doctor` reads the live credential, so macOS may show a keychain prompt the
first time a newly built binary runs it. That is expected: tell the user to
choose **Always Allow**.

Add `--verify` only if the user wants every vaulted credential checked — it
makes one API call per account.

`doctor` exits non-zero when any line says `FAIL`; `warn` lines never fail it.
With several Claude Code profiles configured it prints one `profile` check
per profile (its directory, its live credential, its pool).

For each `FAIL` or `warn` line, say what it means and give the exact fix the
output suggests. If the daemon looks stuck, its log is at
`~/.local/state/claudeswitch/daemon.log`; read the last lines with `tail -50`.

A `[warn] recovery` line means a swap kept credentials it could not file
under an account. `claudeswitch recovery` lists them (never a token);
suggest `claudeswitch recovery restore <slot> <account>` for one whose seat
names a configured account, and leave `clear` to the user: a slot may hold the
only copy of a login.

Do not run `login`, `remove`, `forget` or `recovery clear` from here. Suggest
`/cs login` when an account needs signing in again.
