---
name: setup
description: Set up claudeswitch for Claude Code — check the binary is installed, add the quota status line to Claude Code's settings, and check the installation. Use after installing the claudeswitch plugin, or when asked to "set up claudeswitch" or "add the status line".
allowed-tools:
  - Bash(command -v claudeswitch:*)
  - Bash(claudeswitch version:*)
  - Bash(claudeswitch statusline install:*)
  - Bash(claudeswitch doctor:*)
  - AskUserQuestion
---

# claudeswitch setup

## 1. Is the binary installed?

```bash
command -v claudeswitch || ls ~/.local/bin/claudeswitch
claudeswitch version
```

If neither finds it, stop and give the install steps from
https://github.com/bogdan-alexandrescu/claudeswitch#install : download a release, or build
from source with `./install.sh`.

## 2. Status line

```bash
claudeswitch statusline install
```

It writes `statusLine` to `~/.claude/settings.json` only when none is set,
and keeps a backup. If it reports that another status line is already
configured, ask with AskUserQuestion whether to keep it or replace it. Only
run `claudeswitch statusline install --force` if the user chooses to replace it.

## 3. Accounts

Signing in to accounts for the first time is interactive and needs a real
terminal. Tell the user to run this in a separate terminal window:

```
claudeswitch setup
```

It vaults each account, writes the config, and offers to install the daemon.
Single accounts can be added later from here with `/claudeswitch:login`.

## 4. Check

```bash
claudeswitch doctor
```

Summarise what is OK and what still needs doing.
