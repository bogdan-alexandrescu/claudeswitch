---
name: why
user-invocable: false
description: Explain claudeswitch's rotation decision — why it did or did not switch accounts, account by account. Use when asked "why didn't it switch", "why did it switch to X", "what will it do next", or when rotation looks wrong.
allowed-tools:
  - Bash(claudeswitch why:*)
  - Bash(claudeswitch plan:*)
  - Bash(claudeswitch audit:*)
---

# claudeswitch why

```bash
claudeswitch why
```

This reads saved state only; it makes no API calls. It prints the decision
(stay, switch or wait) and a verdict for every account: whether it is eligible,
its binding window and utilization, and the reason.

With several Claude Code profiles configured, it prints one block per
profile: each decides only within its own pool and with its own thresholds.
Answer about the profile the user means — this session's unless they name
another — and use `claudeswitch why --profile <name>` to show just that one.
An account missing from a block belongs to another profile's pool, which is
by design, not a fault.

If the question is about something that already happened ("why did it switch
at 14:00"), also run:

```bash
claudeswitch audit
```

which lists what the daemon observed, decided and did.

Answer the user's actual question in one or two sentences first, then the
supporting detail. Common causes worth naming explicitly when they apply:

- the daemon is not running, or is in dry-run (it reports swaps but does not make them)
- a cooldown after a recent switch
- rotation is pinned to one account
- the only accounts with room are burnt, unrenewable, or have stale readings
