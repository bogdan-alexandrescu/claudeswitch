---
name: cs
description: Short form for every claudeswitch skill, like the `cs` command in a terminal. `/cs status`, `/cs why`, `/cs switch <id>`.
argument-hint: "[status|why|session|doctor|switch|login|setup] [args]"
disable-model-invocation: true
allowed-tools:
  - Skill
---

# /cs

The user typed `/cs $ARGUMENTS`.

Take the first word as the command and everything after it as that command's
arguments. With no command, use `status`.

| command | skill |
|---|---|
| `status` | `cs:status` |
| `why` | `cs:why` |
| `session` | `cs:session` |
| `doctor` | `cs:doctor` |
| `switch`, `use` | `cs:switch` |
| `login`, `add` | `cs:login` |
| `setup` | `cs:setup` |

Invoke that skill with the Skill tool, passing the remaining arguments as its
`args`, and follow its instructions as if the user had invoked it directly.

If the first word is not in the table, list the commands above in one line and
stop.
