# claudeswitch guide

The full reference behind the [README](../README.md): every command, every
setting, and how each part behaves. The README is the quick tour; this is
the manual. Design reasoning is in [DESIGN.md](DESIGN.md) and the measured
facts behind it in [GROUND_TRUTH.md](GROUND_TRUTH.md).

## Contents

- [Commands](#commands)
- [Adding an account](#adding-an-account)
- [Keeping credentials alive](#keeping-credentials-alive)
  - [Recovery copies](#recovery-copies)
- [Sessions that span accounts](#sessions-that-span-accounts)
- [Usage history](#usage-history)
- [Inside Claude Code](#inside-claude-code)
  - [Every session starts knowing where quota stands](#every-session-starts-knowing-where-quota-stands)
  - [Skills](#skills)
  - [Status line](#status-line)
- [Claude in Chrome](#claude-in-chrome)
- [Notifications](#notifications)
- [Running as a daemon](#running-as-a-daemon)
- [Configuration](#configuration)
  - [Advanced](#advanced)
- [Multiple Claude Code profiles](#multiple-claude-code-profiles)
  - [Making a profile, and starting Claude Code in one](#making-a-profile-and-starting-claude-code-in-one)
  - [Which profile a command acts on](#which-profile-a-command-acts-on)
  - [Picking a profile by folder](#picking-a-profile-by-folder)
  - [Upgrading to the multi-profile release](#upgrading-to-the-multi-profile-release)
- [What it will not do](#what-it-will-not-do)
- [Platforms](#platforms)
- [Layout](#layout)

## Commands

Every command also works as `cs <command>`.

**Looking**

```sh
claudeswitch status              # every account's utilization, both windows
claudeswitch status --detail     # ...with reading age, burn rate, binding limit, trigger ETAs
claudeswitch top                 # the same, redrawn in place (ctrl-c to leave)
claudeswitch whoami              # which Claude account is live right now
claudeswitch accounts            # what is in the vault
claudeswitch session             # token usage across every account used in a span
claudeswitch history -days 21    # deduped rejection history from your transcripts
claudeswitch history --usage     # each account's utilization over 30 days, and the switches
```

**Deciding**

```sh
claudeswitch plan                # the rotation decision right now (changes nothing)
claudeswitch why                 # the same decision, account by account, with reasons
claudeswitch audit               # what the daemon observed, decided and did
claudeswitch audit --kind switch --since 24h
```

**Acting**

```sh
claudeswitch use <id>            # swap onto a vaulted account (hot; no restart)
claudeswitch login <id> --direct # sign in to an account, vault it, add it to the config
claudeswitch add [<id>]          # vault the credential that is live right now (after /login)
claudeswitch refresh <id>        # renew a vaulted credential (never the live one)
```

**Managing**

```sh
claudeswitch setup               # guided first run
claudeswitch config              # every setting in force; `config <name> <value>` changes one
claudeswitch config clean        # delete the scope lines and [project] tables older configs carry
claudeswitch doctor              # config, vault, keychain, usage API, daemon, Claude Code
                                 # (--json: every check, with the fix the app can run)
claudeswitch identify            # record which seat each vaulted credential belongs to
claudeswitch rename <old> <new>  # re-file a vaulted account under another id
claudeswitch forget <id>         # drop an account's recorded observations
claudeswitch remove <id>         # delete an account everywhere: credential, config block,
                                 # pool and priority entries, observations
claudeswitch priority <id>...    # set the rotation order
claudeswitch account pin <id>    # stop automatic rotation in its profile (`unpin` resumes);
                                 # lifted if the account is refused, runs out or needs a
                                 # sign-in, unless pinned with --hard
claudeswitch profile pool <p> add|remove <id> [--to <other>]
claudeswitch profile set <p> <key> <value|inherit>   # per-profile thresholds and models
claudeswitch profile set <p> paths "~/src/work/**"   # the folders that pick the profile
claudeswitch profile which       # the profile this folder picks
claudeswitch daemon status|start|stop|restart|live|dry-run|install|uninstall
claudeswitch watch [--live]      # run the daemon in the foreground
claudeswitch uninstall           # stop the daemon and remove what was installed
```

**Claude Code**

```sh
claudeswitch statusline          # one line for Claude Code's status line (read-only)
claudeswitch statusline install  # add it to ~/.claude/settings.json
claudeswitch context             # the quota summary the plugin gives each session
```

Most read commands take `--json`. So does every command the menu-bar app
runs, which then never prompts and fails with a stable error object;
[docs/APP_CLI.md](APP_CLI.md) is that contract.

`status` on a machine with one account configured:

```
  ACCOUNT      5-HOUR                          7-DAY                           RESETS    STATE
  personal     [#########.............]  40.0% [#######...............]  30.0% 3h10m     ACTIVE available
                 ↳ binding: session (normal) 40%, resets 3h10m

  thresholds  switch ≥85% session / ≥98% weekly   hard floor ≥99%   swap idle, forced after 30s
  api budget  11 scheduled call(s) available now (12 per 5m0s, one held for swaps)
```

## Adding an account

The quickest way, if you can sign in through Claude Code:

```sh
# in Claude Code: /login, as the account you want to add
cs add              # suggests a name from the account, asks you to confirm it
cs add work-b       # or name it yourself
```

That is all: the credential is verified and vaulted, and the account is added
to your config — pinned to its seat, last in `priority`. With several profiles,
`--from <profile>` saves the login that profile's Claude Code is signed into,
and `--profile` names the pool the account joins. A running daemon notices the config change and
starts polling the new account without a restart. Running `add` again for the
same account refreshes its stored credential in place.

To add an account **without** touching the session you are working in, sign in
to it directly instead. Any name works, including one your config has never
seen; it is written in, pinned to whichever seat actually signed in:

```sh
cs login work-b --direct                     # sign in, verify, vault, add to config
cs login work-b --direct --browser Safari    # ...in a browser signed into that account
cs login work-b --code <code>                # finish, with the code the browser shows
cs login work-b --sso                        # an SSO-backed organization
```

A login returns a credential for **whichever account your browser is signed
into**; nothing in the request can override that. So sign in using a browser
that holds the account you want — separate browser applications keep separate
cookies, which is what `--browser` is for.

`login` and `add` check the part that is easy to get wrong. If your config
already pins the name to a seat, the credential that came back is verified
against it and a mismatch is refused, naming the account actually got. A seat
already vaulted under a **different** name is refused too, and nothing is
written. `--direct` obtains the credential without touching your live session
at all; without it, `login` signs in through Claude Code and puts your previous
account back afterwards.

A token from `claude setup-token` cannot be used: it carries the
`user:inference` scope only, and the usage endpoint refuses it with a 403
(measured 2026-10-07), so there is nothing to rotate on. Sign in with
`cs login <id> --direct` instead.

Identity is the **seat** — one person within one organization — never the
email. One address can belong to several organizations with separate quota
pools, and one team organization holds a separate pool for each member. Only
`claudeAiOauth` is ever vaulted; your MCP tokens stay with the machine.

## Keeping credentials alive

Refreshing an access token **revokes the previous one**, so a vaulted snapshot
goes stale within hours of that account's session being refreshed elsewhere. The
daemon therefore renews them itself:

```toml
auto_refresh   = true     # keep vaulted credentials alive at all
refresh_window = "1h"     # renew this long before a token expires
refresh_probe  = "24h"    # also refresh each idle account this often; "0s" disables
```

Those are the defaults. It never refreshes the account currently in use — that
would revoke the token your session is holding; Claude Code renews that one
itself and `SyncActive` re-captures it. Refreshing runs in dry-run too: dry-run
means "do not rotate", and letting every stored credential expire while watching
would be a strange reading of it.

`refresh_probe` exists because `/v1/oauth/token` never reports when a *refresh*
token expires. Without a periodic probe a dead one stays invisible until the
moment you need that account; with it, you find out within a day. `cs doctor`
shows the policy and every account's standing, and `status` says when an
account cannot be renewed.

### Recovery copies

Before a swap overwrites the live credential it saves it: into its account's
vault entry when it can tell whose it is, otherwise into a recovery slot (up to
five per profile), so a login is never destroyed by a swap. `cs doctor` warns
when any slot holds something.

```sh
cs recovery                       # list kept credentials: slot, profile, when, seat, expiry
cs recovery --identify            # also ask whose each one is (one API call each)
cs recovery restore work-1 w1     # vault slot work-1 as w1, after checking its seat; clears the slot
cs recovery clear work-1          # delete a slot (asks first; --yes without a terminal)
```

`restore` refuses a credential whose seat is not the account's pinned one, and
will not replace a better vaulted credential without `--force`. No command ever
prints a token.

## Sessions that span accounts

Rotation makes "how much have I used?" a question no single account can answer:
the work continues across swaps, and each account only knows its own share.

```
$ cs session
  Thu 10:51 → 18:51  (8h0m0s)

  ACCOUNT      TOKENS  SHARE  MESSAGES   ACTIVE
  personal      1.20B    56%      4276   5h4m0s
  work-team    947.5M    44%      4115  2h56m0s

  total         2.15B             8391
              ↳ out 1.1M · thinking 131k

  1 switch(es) in this span:
    15:55:30  personal → work-team   active account at 85%, over the 85% trigger
```

It joins the transcripts (per-message token counts and timestamps) with the audit
log (when the active account changed), attributing every message to whichever
account was live when it was written. No network calls, so it costs no quota.
With several profiles it covers them all: each profile's messages go to the
account live in that profile, and the switch list labels each switch with its
profile. `--profile work` reports one.
`--since 2h` narrows the window, `--detail` breaks out input/output/cache and models.

## Usage history

The daemon keeps every reading it takes, so you can see how each account's
session and weekly windows moved, and when it switched:

```sh
cs history --usage               # per account: readings, peaks, the last one
cs history --usage --days 7 --json
```

The readings go to `~/.local/state/claudeswitch/readings.jsonl` (0600), one
JSON line per new reading of an account:

```json
{"at":"2026-10-09T11:58:02Z","account":"work-1","five_hour":42,"seven_day":61.5}
```

`at` is when the usage API reported the figures (UTC); a window it did not
report is `null`. Only the daemon writes the file, and it keeps it bounded:
readings older than 30 days go, those older than a day are thinned to the
last one per account per 15 minutes, and the file stays under 4 MiB (the
oldest go first). It compacts when it starts, every 6 hours and whenever it
grows past the limit, writing a new file and renaming it into place.
`cs uninstall` removes it with the rest of the state. The switches come from
the audit log. The menu-bar app's History charts read `history --usage
--json` ([cs history](cli/claude-code.md#cs-history---usage)).

## Inside Claude Code

With the [plugin installed](../README.md#claude-code-plugin), three things change.

### Every session starts knowing where quota stands

```
[claudeswitch] active personal · session 24% (resets 3h47m) · week 44% (resets 4d15h)
[claudeswitch] rotates at session 85% / week 98%, mid-turn at 99% · daemon running, rotates automatically
```

That is `claudeswitch context`, run by a SessionStart hook. It reads saved state
only — no keychain, no API calls — and never fails a session start. Two lines is
the normal case; anything more needs attention:

| extra | means |
|---|---|
| `· reading 12m old` | no fresh reading for over 10 minutes: the poller is stuck or refused |
| `personal was refused until 14:30` | the account hit a limit |
| `daemon NOT running` / `daemon in dry-run` | nothing will rotate automatically |
| `next: switch to work-a …` | a switch is due; the line gives the `use` command |
| `next: wait …` | every account is out; says which recovers first |
| `not on PATH` / `older than this plugin` | install or update the binary |

### Skills

`/cs` works like `cs` in a terminal: `/cs status`, `/cs why`,
`/cs switch work-a`. With no command it shows status. It is the plugin's only
slash command. You can also simply ask, and Claude runs the matching command.

| command | ask something like | changes anything |
|---|---|---|
| `/cs status` | "how much quota is left?" | no |
| `/cs why` | "why didn't it switch?" | no |
| `/cs session` | "how much have I used today?" | no |
| `/cs doctor` | "claudeswitch isn't polling" | no |
| `/cs switch <id>` | "move me to the account with most room" | yes, without asking: a swap is hot and reversible |
| `/cs login <id>` | "add my work account" | yes, after confirming account and browser |
| `/cs setup` | "set up claudeswitch" | settings.json; asks before replacing a status line |

`/status`, `/login` and `/doctor` belong to Claude Code itself, which is why the
plugin's commands live under `/cs`.

A switch made from a skill takes effect from Claude's next request, in the
current session included. First-time account setup stays in a terminal
(`claudeswitch setup`): it is interactive in ways a skill cannot be.

### Status line

`claudeswitch statusline` prints one compact line and is strictly read-only —
no polling, no API calls, no state writes — so it is safe to run on every
render:

```
personal  session ▓▓░░░░░░░░ 24% 3h48m  ▸week ▓▓▓▓░░░░░░ 44% 4d15h
```

`▸` marks the window that binds, and an arrow after its figure shows which way
it is moving. Bars turn yellow at 60%, and at the switch threshold and hard
floor take the colours for "rotation coming" and "mid-turn swap". Past the
threshold the line says what happens next — `· rotating to work-a`,
`· all out, quota at 14:30`, `· holding` — and a refused account reads
`refused until 14:30`. `· read 12m ago` means the readings have stopped moving.

`claudeswitch statusline install` writes it to `~/.claude/settings.json`
(honouring `CLAUDE_CONFIG_DIR`). It leaves an existing status line alone unless
you pass `--force`, keeps the previous file as `settings.json.claudeswitch.bak`,
and preserves the order of your keys. `uninstall` removes it only if it is
claudeswitch's. By hand, it is:

```json
"statusLine": { "type": "command", "command": "claudeswitch statusline" }
```

## Claude in Chrome

The Claude in Chrome extension keeps its own claude.ai login, pinned to one
account, and Claude Code finds it on a channel keyed by Claude Code's own
account. After a rotation Claude Code is on another account, so the browser
tools stop answering ("not connected", or "both must use the same claude.ai
account"). claudeswitch cannot move the extension's login, and does not try:
it says which Chrome profile Claude in Chrome needs, and opens it for you.

Which Chrome profile an account uses is decided in this order:

1. **the account's own Chrome profile**, if it has one — a new one
   (`cs chrome add`) or one you already have (`cs chrome add --existing`);
2. **its profile's Chrome profile**, if the profile names one
   (`cs profile set <profile> chrome <name>`);
3. **Chrome's last-used profile**.

claudeswitch never creates a Chrome profile unless you run `cs chrome add`
without `--existing`.

```sh
cs chrome profiles [--json]             # your Chrome profiles, and which profiles
                                        # and accounts use each
cs profile set work chrome "Work"       # the work profile's accounts use the
                                        # Chrome profile called Work
cs profile set work chrome inherit      # back to Chrome's last used
cs chrome add work-a --existing "Work 2"  # work-a uses your "Work 2" profile
cs chrome add work-a                    # a new Chrome profile for work-a, opened at
                                        # the claude.ai login and the extension's
                                        # Web Store page
cs chrome work-a                        # open work-a's Chrome profile
cs chrome                               # ... of the account live in this shell's profile
cs chrome signin work-b                 # open it at the sign-in pages, to sign
                                        # Claude in Chrome in as work-b
cs chrome list [--json]                 # which account has its own Chrome profile
cs chrome forget work-a                 # drop work-a's own (the Chrome profile stays)
```

A Chrome profile is named by Chrome's name for it ("Work") or by its folder
(`Default`, `Profile 2`); the config stores the folder. `cs config` lists each
profile's `chrome`, and `cs config get chrome --profile work` prints it.

**The honest limit.** The extension holds one login per Chrome profile and
claudeswitch cannot move it. So:

- **an account with its own Chrome profile routes by itself**: once that
  profile's claude.ai and extension are signed in as the account, browser
  tasks follow every rotation to it (confirmed 2026-10-08);
- **a Chrome profile shared by a profile's accounts needs one sign-in per
  rotation**: after a switch from work-a to work-b, the extension there is
  still signed in as work-a until you sign it in as work-b
  (`cs chrome signin work-b`, or the button on the app's card).

In the window `add` or `signin` opens: sign in to claude.ai as that account,
add (or enable) Claude in Chrome, and sign the extension in as the same
account. Then:

- after a rotation to an account with its own Chrome profile, the daemon and
  `cs use` say `Claude in Chrome: use the work-b Chrome profile (cs chrome
  work-b)`;
- to one that uses its profile's Chrome profile, they say
  `Claude in Chrome in "Work" is still signed in as work-a; sign it in as
  work-b (cs chrome signin work-b)`, and the app's card shows the same with a
  **Sign in as work-b** button (dismissable until the next rotation);
- to one with neither, once you have set up a Chrome profile for any account,
  they say `Claude in Chrome: work-b has no Chrome profile — cs chrome add
  work-b`;
- when a browser tool fails with the same-account or not-connected error, the
  plugin tells Claude which account Claude Code is on and the command, once
  per rotation (it looks only at failures, never at page content).

The steps `add` and `signin` print name the account's email when claudeswitch recorded
one (at `add`, `login`, `setup` or `identify`); it never reads the keychain
for it. Accounts vaulted before this release show their id until
`cs identify` records the email. `cs rename` carries the mapping
over.

The profiles live inside Chrome's normal user-data directory (chosen with
`--profile-directory`), because Claude in Chrome's native-messaging host is
registered there; a separate `--user-data-dir` would not find it.
claudeswitch records which account has which profile directory in its own
state, and a profile's `chrome` in the config. It never writes Chrome's files.
The one thing of Chrome's it reads is the profile list in Chrome's `Local
State` file (each profile's folder and name, and which was last used),
read-only: on macOS `~/Library/Application Support/Google/Chrome/Local State`,
on Linux `~/.config/google-chrome/Local State`, then
`~/.config/chromium/Local State`. It never reads cookies, preferences or
extension storage, and never decrypts anything. When `Local State` cannot be
read, name Chrome profiles by folder.

**Confirmed on a real machine (2026-10-08):** after a switch, browser tasks
from Claude Code run in the new account's Chrome profile without you opening
it by hand — provided each account has its own Chrome profile (`cs chrome add
<account>`) signed in to claude.ai and to the extension as that account.
macOS and Linux (`google-chrome` or `chromium` on PATH) only.

## Notifications

The daemon sends a desktop notification when it switches accounts, when every
account is burnt (naming which recovers first), and before an idle account's
refresh token expires — a dead refresh token means that account can no longer
be swapped to *or* polled. Once you use [Chrome profiles](#claude-in-chrome),
a switch also says which profile Claude in Chrome needs, that the shared one
needs signing in as the new account, or that the new account has none yet.
It also notifies once when a profile's pool is forecast to run dry within two
hours at the pace it is being spent (`cs status` shows the same forecast:
`work pool: runs dry Thu 14:00 at this pace`), and when it lifts a pin
because the pinned account was refused, ran out or needs a sign-in
(`pin on work-1 lifted: it was refused`; `cs account pin --hard` keeps a pin
even then). Nothing else notifies; `--quiet` disables them.

## Running as a daemon

```sh
./install.sh          # builds, installs, loads the service in DRY-RUN
./install.sh --live   # ...or let it actually perform swaps
claudeswitch plan     # the current decision, and whether anything will act on it
claudeswitch audit    # what it has decided and done
```

Dry-run is the default and exercises the entire decision path, logging the swap it
*would* make. Run that way for a day first; `claudeswitch audit --kind decision` shows
whether its judgement matches yours. **Re-running `./install.sh` without `--live`
puts a live daemon back into dry-run.**

Once installed, `claudeswitch daemon live` / `daemon dry-run` switch the mode,
and `daemon status|start|stop|restart` manage it, without a rebuild;
`daemon install` registers the binary you run (it builds nothing). The
plist and unit are the ones `install.sh` writes.

On macOS a rebuilt binary is a new program to the Keychain, so `install.sh`
triggers the access prompt while you are at the keyboard — click **Always
Allow**. A daemon cannot answer that prompt and would otherwise sit silently.

One daemon per machine, enforced with a lock file. While it runs it owns the
polling and the state file, and the CLI reports its readings instead of making
its own API calls — so checking `status` never costs you API budget or races
the daemon's writes. Its log is `~/.local/state/claudeswitch/daemon.log`.

## Configuration

`~/.config/claudeswitch/config.toml`, written by `setup`:

```toml
switch_at        = 85     # rotate away at this session (5-hour) utilization
switch_at_weekly = 98     # ...and at this weekly utilization
hard_floor       = 99     # above this, swap mid-turn rather than wait for an idle gap
switch_when      = "idle"
hot_threshold    = 60     # poll_hot only above this (or burning fast), and only while moving toward the trigger
poll_hot         = "1m"   # the default; faster drains the usage API's burst allowance
poll_active      = "3m"   # the default: the account in use when not moving; under 2m runs at 2m and warns
cooldown         = "10m"
max_switch_wait  = "30s"  # how long a due switch waits for an idle gap
landing_margin   = 10     # a switch target's session window needs this many points below its trigger
prefer           = "room" # the default; "expiring" spends the soonest-resetting weekly quota first
blind_failover_polls = 3  # unreadable polls of the account in use before failing over; 0 holds

priority = ["work-a", "work-b", "personal"]

[[account]]
id           = "personal"
reserve      = 70         # never auto-used above this utilization
account_uuid = "…"        # the seat: this person...
org_id       = "…"        # ...in this organization
```

The two triggers differ on purpose: 85% of a 5-hour window is nearly gone and
refills the same afternoon, while 85% of a weekly one still holds days of work.

| setting | default | what it does |
|---|---|---|
| `landing_margin` | `10` (0–50) | A switch only lands on an account whose **session (5-hour)** window has at least this many points of room below `switch_at`, so it never lands on one it must leave again at once. The weekly window has no margin: any account under `switch_at_weekly` qualifies. When every account with room is inside the margin, an ordinary rotation holds until `hard_floor`; past it, or after a refusal, it takes the best of them anyway. Settable per profile. `0` turns it off. Written to the file only once set. |
| `prefer` | `"room"` | Which eligible account a rotation (and **Switch to best**) takes. `"room"`: the one with the most room, priority breaking ties. `"expiring"`: the one whose **weekly** window resets soonest while it still has quota unused, so quota that would expire unused is spent first; room breaks ties (resets in the same minute), and an account whose weekly reset is unknown comes after every one whose is known. Eligibility is unchanged: under its triggers, clear of the landing margin, not refused, reserved or needing a sign-in. `cs why` says when it decided (`work-team resets in 9h with 40% unused, so it goes first`). Settable per profile. Written to the file only once set. |
| `models` | `[]` | Model names (as `cs status --detail` shows them) whose **per-model weekly limit** counts like the weekly window: when one is higher than the account's weekly figure it is what `switch_at_weekly` triggers on and what eligibility judges, and a counted limit the API reports without a figure makes the account unknown (never a target). Empty: per-model limits are shown, never acted on. Settable per profile; `models = []` in a profile turns the global list off there. |
| `blind_failover_polls` | `3` | When the usage of the account in use has failed to read for this many polls in a row, over at least that many `poll_active` intervals counted from the first failure, switch to an account that can be read and clears the landing margin — only in an idle gap, never mid-turn. A 429 from the usage endpoint does not count (it clears by itself within 15 minutes), nor a 401 on a stale stored copy of a token Claude Code has since refreshed, nor an access token that merely expired on an idle session. A daemon restart starts the count afresh. `0` always holds. Written to the file only once set. |

### Advanced

The per-account usage model (DESIGN 4.3c). The defaults are the measured
model; change them only if you have measured better. `doctor`'s "account
rate" row shows the values in force.

| setting | default | what it does |
|---|---|---|
| `hot_reserve` | `10` (0–15) | Usage calls per account that routine polling leaves untouched, so the account in use can be read every `poll_hot` while it moves toward its trigger. A 15-minute hot spell at 60 s needs 7.5. |
| `unseen_calls_per_hour` | `2` (0–20) | Usage calls an hour a live account is assumed to lose to Claude Code's own reads of the same endpoint, which claudeswitch cannot see. Its allowance is modelled as refilling this much slower. |

`cs config` lists every setting with its value, and `cs config <name> <value>`
changes one with validation. A live credential that matches no pinned seat is
reported as `ACTIVE, unattributed` rather than filed under a guess.

## Multiple Claude Code profiles

Claude Code can run as several independent profiles, one per config
directory, each with its own live credential:

```sh
claude                                   # CLAUDE_CONFIG_DIR unset: ~/.claude
CLAUDE_CONFIG_DIR=~/.claude-work claude  # a second profile
```

Declare each one with an `[[profile]]` block and give it a pool of accounts.
One daemon drives them all, rotating each profile only within its own pool:

```toml
[[profile]]
name = "default"        # no dir: Claude Code runs with CLAUDE_CONFIG_DIR unset
pool = ["personal", "a4", "a5"]

[[profile]]
name      = "work"
dir       = "~/.claude-work"
pool      = ["work-1", "work-2"]
switch_at = 75          # overrides the global 85 for this profile only
```

- **`dir` is `CLAUDE_CONFIG_DIR` exactly as you launch Claude Code with it.**
  Leaving it out means `CLAUDE_CONFIG_DIR` unset, which is *not* the same as
  `dir = "~/.claude"`: Claude Code keeps a different keychain item for each.
  Only one profile may leave it out.
- **Pools must not overlap.** Accounts in no pool join the profile named
  `default`; with no `default` declared, an unlisted account is a config error.
- `switch_at`, `switch_at_weekly`, `hard_floor`, `landing_margin`, `prefer` and `models`
  may be set per profile; anything unset falls back to the global value.
- No `[[profile]]` blocks at all means one profile holding every account,
  which behaves exactly as before.
- Profile names and account ids are plain names: letters, digits, `.`, `_`
  and `-`, not starting with `-` or `.`, at most 64 characters. A config
  with an older id that breaks this does not load; the error names it, and
  `cs rename <old> <new>` fixes it everywhere (config, state and vault).
- Removing a profile, or changing its `dir`, keeps its old credential
  guarded: the account last live there is installed in no other profile
  until that credential no longer holds it, or `cs profile forget <name>`.
  This holds for edits made while the daemon is stopped too: the next daemon
  start, and every `use`, `login`, `refresh` and `remove`, notice them. For
  a profile whose credential was never recorded, the account itself stays
  guarded until `cs profile forget <name>`. `status` and `doctor` list what
  is still guarded.
- A new account signed in (`login <id>`) or added (`add <id>`) through an
  profile joins that profile's pool: the config edit writes the account and
  the pool entry together.
- Editing the profiles while the daemon runs needs no restart: a profile
  added is started, one removed is stopped, and one whose `dir` changed is
  stopped and started again, between swaps, never during one.

**One credential is live in at most one profile.** Claude Code refreshes its
own token, and a refresh revokes the previous one, so an account live in two
profiles is logged out of whichever refreshes second. claudeswitch therefore
never swaps an account into a profile while it is, or might be, live in
another; when it cannot tell, it refuses and tries again later.

### Making a profile, and starting Claude Code in one

```sh
cs profile create work --pool work-1,work-2 --seed work-1
cs run work                    # claude, with CLAUDE_CONFIG_DIR set for work
cs run work -- --resume        # arguments after -- go to claude
cs profile list                # each profile's dir, pool and live account
```

`profile create` does, in one step:

- makes the dir (`~/.claude-<name>`, or `--dir PATH`), written to the config
  as an absolute path. It may not be, hold or sit inside `~/.claude` or
  another profile's dir, through links or (on macOS) a different case; no
  two profiles may share a folder, which the config also refuses at load.
  It warns about an existing dir that is not 0700, or one in a git work
  tree;
- links your `settings.json`, `CLAUDE.md`, `skills/`, `commands/` and
  `agents/` from `~/.claude` into it, so one edit serves both profiles.
  Anything already in the dir is kept, never replaced. Transcripts, history,
  plugins and credentials are not shared;
- copies your user-scope MCP servers (`mcpServers` from `~/.claude.json`, and
  nothing else from it) into the new dir's `.claude.json`. It is a copy, and
  servers that sign in with OAuth need signing in again there (`/mcp`);
- writes the `[[profile]]` block, refusing an account another profile's pool
  lists. With no `[[profile]]` blocks yet, it declares `default` too — on
  your shell's `CLAUDE_CONFIG_DIR` if it is set, with no `dir` if not —
  which keeps every other account where it was;
- with `--seed <account>`, signs the new profile in with that account's
  vaulted credential, so Claude Code starts signed in. The account must be in
  the new pool and live in no other profile, nor possibly live in one (the
  same check as `use`); the profile must have no credential yet. With a
  daemon running it first waits up to 10 seconds for the daemon to load the
  new profile, then checks again; if the daemon does not, the profile is
  made but not signed in, and `cs profile seed <name> <account>` finishes
  it. It is written holding Claude Code's credential locks and never
  overwrites an existing item.

`cs run` on a profile that is not signed in yet starts Claude Code anyway,
with a note to use /login. A running daemon picks up the new profile without
a restart.

### Which profile a command acts on

`use`, `add`, `login` and `whoami` act on one profile:

- `--profile NAME` names it.
- Without the flag, the profile your shell's own `CLAUDE_CONFIG_DIR` belongs
  to: unset is the profile with no `dir`, and a set one is matched against each
  `dir` (as written first, then as the directory it names). So `/cs use x`
  typed inside a work session switches the work profile, with no flag. A shell
  whose `CLAUDE_CONFIG_DIR` matches no profile gets an error saying so, never a
  guess.

`use` (and `login`, and `add` for a configured id) refuses an account from
another profile's pool, naming the profile that owns it. There is no
`--force`: a swap across pools is how one credential ends up live in two
profiles. `cs use work-1 --profile work` is how to switch the other one.

`status`, `top`, `why` and `plan` show one block per profile — its pool, its
active account, its decision and its effective thresholds — and take
`--profile NAME` to show only one. The status line and the session-start
context show the profile of the session they run in. `refresh` and `remove`
check every profile's live credential before touching an account. `doctor`
checks each profile: its directory, its live credential, and its pool.

### Picking a profile by folder

A profile may name the folders it is for:

```toml
[[profile]]
name  = "work"
dir   = "~/.claude-work"
pool  = ["work-1", "work-2"]
paths = ["~/src/work/**", "~/clients/*"]
```

or `cs profile set work paths "~/src/work/**,~/clients/*"`. Then, in any
folder under those, `cs run` with no name starts `work`; anywhere else it
starts `default`. `cs profile which` says which profile a folder picks and
why. Patterns are absolute or from `~/`, with `*`, `?` and `**` (any number
of folders); a folder counts when a pattern matches it or one of its parents.
Two profiles whose paths could match the same folder are refused when the
config loads, like overlapping pools.

To have plain `claude` follow the folder too, add the optional shell hook:

```sh
eval "$(claudeswitch profile hook zsh)"     # ~/.zshrc (bash: profile hook bash)
claudeswitch profile hook fish | source     # ~/.config/fish/config.fish
```

On each change of folder it sets `CLAUDE_CONFIG_DIR` to the picked profile's
`dir`, or unsets it for the profile without one, and does nothing else. With
no profile picked it leaves the variable alone.
[cs profile](cli/profiles.md#profile-hook) has the details.

### Upgrading to the multi-profile release

`state.json` now records the active account per profile and no longer writes
the old top-level `active_account`, `pinned`, `last_switch` and `active_at`
fields. Old files are still read and migrated. **Restart the daemon once
after upgrading, before adding `[[profile]]` blocks**: the new daemon
records which credential each profile uses, which is what lets a later
config edit keep guarding an account still live in a profile you removed.
Restart it (`./install.sh`, or `--live` if it was live) so no older daemon is
left reading fields that are no longer written. Until you do, every command
that reads state prints one line to stderr saying the running daemon is older
than `cs` and how to restart it, and `doctor` reports it as a FAIL. The daemon
records its build in `state.json` when it starts; builds are compared by commit
time, or by release version, and a `dev` build with no git information is not
compared at all.

## What it will not do

- **Guess.** An account it cannot read is `unknown`, and `unknown` is never
  treated as available. A percentage is shown only if the API reported it.
- **Estimate quota from token counts.** That was tested against 24 real limit
  hits and produced 40–79% spread. See ground truth, Round 2.
- **Hammer the usage API.** The endpoint refuses an account for 10–15 minutes
  after a burst of about 24 calls. Every claudeswitch process shares one budget
  of 12 calls per 5 minutes, plus an allowance per account (20 calls, refilling
  one every 2 minutes, 3 held back to verify a swap); the account in use is
  read every `poll_hot` only while its usage is moving toward its trigger.
- **Touch your MCP tokens.** The Keychain item holds Notion and Slack OAuth
  alongside the Claude credential; only the `claudeAiOauth` subtree is ever
  swapped.
- **Prompt for the Keychain from Claude Code.** The status line and the session
  hook read saved state only.

## Platforms

| | macOS | Linux |
|---|---|---|
| credentials | Keychain (`security`) | `~/.claude/` files, `0600` in a `0700` dir |
| service | launchd agent | systemd `--user` unit |
| notifications | `osascript` | `notify-send` (absent on servers; harmless) |

Both are exercised by the same test suite. The Linux side was developed against
a real Linux container rather than cross-compiled and hoped for — which caught
two faults a cross-compile would have missed: re-indented JSON breaking the
byte-for-byte mcpOAuth guarantee, and a path check that accepted `..`.

## Layout

```
cmd/claudeswitch      CLI
internal/usage        the /api/oauth/usage adapter + call budget
internal/poller       scheduling within the call budget
internal/policy       whether to rotate, and where to
internal/vault        per-account credentials, and the swap
internal/credstore    where the platform keeps credentials
internal/keychain     credential read, mcpOAuth-aware
internal/oauth        login and token refresh
internal/detector     transcript rejection watcher (safety net)
internal/state        durable observations (the 7-day window outlives restarts)
internal/readings     the daemon's past readings, for history --usage
internal/audit        what was observed, decided and done
internal/session      work that spans several accounts
internal/config       declarative accounts
internal/render       the status view
internal/notify       desktop notifications
plugin/               the Claude Code plugin: hooks (session start, Chrome hint) and skills
macos/                the menu-bar app (SwiftUI); install-app.sh builds it
.claude-plugin/       the marketplace entry that lists the plugin
packaging/homebrew/   the Homebrew formula and cask, filled in per release by update.sh
```
