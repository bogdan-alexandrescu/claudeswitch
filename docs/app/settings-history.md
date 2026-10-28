# Settings → History

Each account's session and week over the last 30 days, with the switches to and away from it.

See also: [cs history](../cli/claude-code.md#cs-history) · [Concepts → how rotation decides](../concepts.md#how-rotation-decides) · [Settings → Rotation](settings-rotation.md)

<p align="center"><img src="../images/settings-history.png" width="880" alt="Settings, History pane: the legend (Week, Session, Switched to it, Switched away) and Refresh, then a chart per account (personal, research, work-1 and on), each with its plan and its latest week and switch count: the week as a thick line and the session as a thin one over 30 days, from 0% to 100%, with solid rules where rotation switched to the account and dashed rules where it switched away"></p>

The pane runs `cs history --usage --days 30 --json` when it opens, unless it
read it in the last five minutes (the Overview's Recent uses the same read
and rereads it the same way), and again with **Refresh**, which spins while
it reads. That is a series of readings per account, recorded by the daemon
as it polls, plus every switch. A refresh that fails keeps the charts it
has and says so above them, with the CLI's message.

## A chart per account

| mark | what it shows |
|---|---|
| thick line, in the accent | the week: the account's weekly utilization |
| thin line, in the second accent | the session: its 5-hour utilization |
| solid grey rule | rotation switched a profile **to** this account |
| dashed grey rule | rotation switched a profile **away** from it |
| beside the name | the account's plan |
| top right | the week's latest figure and how many switches touched the account (`week 59% · 8 switches`) |

The week's line is in the accent and the session's in the second accent.
Each line is one point per hour (the hour's highest reading). Where the daemon recorded nothing for more than six hours (it was
stopped, or the Mac slept) the line breaks rather than bridging the gap, and
a reading the daemon could not take is left out: an unknown figure is never
drawn as 0.

An account with no readings in the 30 days has no chart. With none at all the
pane says the daemon records them as it polls.

## With an older claudeswitch

`history --usage` is new in the release that adds this pane. With an older
binary the pane says it needs a newer claudeswitch, and nothing else changes.

The screenshot is drawn from fixture data (`macos/scripts/gen-feature-fixtures.py`).

[← Documentation index](../README.md)
