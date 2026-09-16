---
name: session
description: Report token usage for a span of work across every Claude account it touched, since rotation splits one session over several accounts. Use when asked "how much have I used today", "what did this session cost", or "usage across accounts".
allowed-tools:
  - Bash(claudeswitch session:*)
---

# claudeswitch session

```bash
claudeswitch session
```

With no flags it covers the span since the first switch today, or the last
eight hours. When the user names a span, pass it: `--since 3h`, `--since 24h`.
`--detail` adds the per-model breakdown.

This reads Claude Code's local transcripts; it makes no API calls.

Report the total, the split between accounts, and the span covered.
