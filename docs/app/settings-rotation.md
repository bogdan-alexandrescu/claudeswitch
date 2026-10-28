# Settings → Rotation

When an account is rotated away from, and where to: the global rotation settings.

See also: [cs config](../cli/config.md) · [Concepts → how rotation decides](../concepts.md#how-rotation-decides) · [GUIDE → Configuration](../GUIDE.md#configuration)

<p align="center"><img src="../images/settings-rotation.png" width="880" alt="Settings, Rotation pane: Switch at session 85, Switch at week 98, Hard floor 99, Switch when Between turns, Max switch wait 30s, Hold for claude.ai 5m, Settle after swap 45s and Release idle after 30m, each with its description, default and range"></p>

Every row is one `cs config` setting. The app builds the pane from
`cs config schema --json`, so the description, default and range below each
field are the CLI's own; a description names other settings by their labels
(“Switch at session”), not their keys. Values read as you type them:
durations in the units `cs config set` takes (`72h`, never `3d`), choices in
words (**Between turns**, not `idle`).

A typed value is saved with `cs config set <key> <value>` when you press
Return or when you leave the field, if it is valid and changed; a choice is
saved when you pick it. A value out of range is refused under the row,
before anything runs, or with the CLI's message; a saved one says "Saved:
old → new" and whether the running daemon picks it up. A current value outside its own range (one written into the config by
hand, say) is said under its row: "The current value, 1m, is outside its
range: it must be …". These are the global values: a profile can override
the ones marked "per profile" in [Settings → Profiles](settings-profiles.md).

The rows are greyed out until the current values are read. When they cannot
be read, the pane shows why with **Try Again**, rather than the defaults
dressed as the values.

| row | key | what it does | default | range |
|---|---|---|---|---|
| **Switch at session** | `switch_at` | rotate away at this much of the 5-hour window. Per profile | 85% | above 0 to 100 |
| **Switch at week** | `switch_at_weekly` | ...and at this much of the weekly one. Per profile | 98% | above 0 to 100 |
| **Hard floor** | `hard_floor` | above this, swap mid-turn rather than wait for an idle gap (at or above **Switch at session**). Per profile | 99% | above 0 to 100 |
| **Switch when** | `switch_when` | **Between turns** (`idle`) prefers swapping between turns; **At once** (`immediate`) does not wait | Between turns | Between turns, At once |
| **Spend first** | `prefer` | which eligible account rotation moves to: **Most room**, or **Expiring quota**, the account whose weekly window resets soonest while it still has unused quota (ties go to room). Per profile. Hidden with a CLI that predates it | Most room (`room`) | Most room (`room`), Expiring quota (`expiring`) |
| **Max switch wait** | `max_switch_wait` | stop waiting for an idle gap after this | 30s | at least 0s |
| **Hold for claude.ai** | `hold_for_claude_ai` | hold a rotation this long after a session's last claude.ai call: Claude in Chrome, an artifact, a Doc (0 = off). Hidden with a CLI that predates it (before 0.8.0 the row was **Hold for browser**, `hold_for_browser`) | 5m | at least 0s |
| **Settle after swap** | `settle_after_swap` | after a switch, hold a claude.ai call until this long after it, so it acts as the new account (0 = off). Hidden with a CLI that predates it | 45s | 0s to 5m |
| **Release idle after** | `release_idle_after` | give up an account another profile wants when this profile's sessions have been idle this long (0 = never). Hidden with a CLI that predates it | 30m | at least 0s |
| **Cooldown** | `cooldown` | minimum gap between rotations, to stop flapping | 10m | at least 0s |
| **Landing margin** | `landing_margin` | a switch target needs this many points below its own trigger (0 = off). Per profile | 10 | 0 to 50 |
| **Count model limits** | `models` | models whose per-model weekly limit counts like the weekly window (comma-separated; empty = none). Per profile | none | |

The screenshot shows the defaults; each row also prints its default, so a
changed value stands out. The reasoning behind each default:
[GUIDE → Configuration](../GUIDE.md#configuration).

[← Documentation index](../README.md)
