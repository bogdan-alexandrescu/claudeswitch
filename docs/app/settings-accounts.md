# Settings → Accounts

Every stored account, grouped by the pool it is in and in rotation order within each, with what you can do to each, and the recovery copies.

See also: [cs accounts](../cli/accounts.md) · [cs pool](../cli/profiles.md#pool) · [Settings → Profiles](settings-profiles.md) · [Add account](add-account.md) · [Claude in Chrome in the app](chrome.md) · [GUIDE → Recovery copies](../GUIDE.md#recovery-copies)

<p align="center"><img src="../images/settings-accounts-pools.png" width="880" alt="Settings, Accounts pane grouped by pool: PERSONAL used by default (1st), personal in use by default; TEAM used by default (2nd) and work (2nd), team-1 needing sign-in, team-2 in use by work, team-3 available; OPS used by work (1st), ops-1 available; SPARE, which no profile uses, empty; and loose-1 in no pool, which default uses last. Each row has its rings, its state, a line with who uses it, its figure and its login, and its plan; each pool header has a ⋯ menu, and New Pool is at the foot"></p>

## Pools

Accounts are grouped into named pools, one pool per account. Each profile
lists the pools it uses, in order: it takes an account from a later pool
only when nothing in an earlier one is usable. Profiles that list the same
pool share its accounts, one profile at a time
([PROFILES.md §10](../PROFILES.md)). A profile with `pool = [...]` in its
block has its own pool, named after it. Accounts no pool lists are used by
the profile named `default`, after its pools.

The pane has one section per pool, in the order `cs pool list` gives them,
then **In no pool** when some account is in none:

```
PERSONAL · used by default (1st)
TEAM · used by default (2nd), work (2nd)
OPS · used by work (1st)
SPARE · no profile uses it
In no pool · default uses them last
```

A section's header says which profiles use the pool and, when a profile
uses more than one, where this pool comes in its order. A pool no profile
lists says so: its accounts rotate nowhere until one does. With no
`[[profile]]` blocks there is nothing to group, and the pane is one list in
rotation order, under **ROTATION ORDER**; so it is with a `claudeswitch`
from before pools, with a line saying to update it. Every section's header,
the pools', **ROTATION ORDER** and **RECOVERY COPIES**, is drawn alike:
capitals, semibold, inside the pane's margins, with no rule under it.

| control | what it does | CLI |
|---|---|---|
| drag a row within its pool | changes the rotation order: within a pool, the order rotation tries them | `cs priority <ids...>` |
| drag a row onto another pool | moves the account there, after a confirmation saying which profiles gain and lose it | `cs pool move <id> --to <pool>` |
| drag a row onto **In no pool** | takes the account out of its pool: `default` uses it, after its pools | `cs pool remove <pool> <id>` |
| **New Pool** | a name, and accounts to start with. Each account says the pool it is in (`in “team”`, `in no pool`), and once checked, that it leaves it (`leaves “team”`) or joins the new one (`joins “lab”`); a line with a warning sign then says which profiles stop rotating it until one uses the new pool, and the button is **Create…**, which asks first (**Create** when no account leaves a pool). No profile uses a new pool until you add it to one ([Profiles → Change…](settings-profiles.md)). When the pool is made but an account cannot be moved in, the sheet says so ("The pool “ops” was created, but “team-2” could not be moved in: …"), keeps its name and accounts, and its button moves only the accounts still to come (**Move “team-2” In**); **Cancel** becomes **Close**, since the pool stays | `cs pool create <name> [<ids>]`, then `cs pool move <id> --to <name>` for each that was in a pool |
| **Rename…** (a pool's ⋯ menu) | a sheet, **Rename the Pool “team”**, with a new name. Profiles that use it keep it, in the same place in their order. A profile's own pool becomes a separate pool with the same accounts, which that profile keeps using; the sheet says so ("Renamed to “laptop”, it becomes a separate pool with the same accounts, which “default” keeps using.") | `cs pool rename <pool> <new>` |
| **Delete…** (a pool's ⋯ menu) | a sheet, **Delete the pool “team”?**, that asks where its accounts go (it starts on a pool the same profiles use, or No pool for default's) and says what happens: only the pool is deleted, where its accounts go and that no account is deleted, then what each profile that used it is left using ("The profile “work” then uses only “ops”."). Its **Delete “team”** button deletes it | `cs pool delete <name> --to <pool>` or `--no-pool` |

## The list

One row per account. Each row has:

- small rings: the account's session (outer) and week (inner), as in the
  [menu-bar glyph](menu-bar.md#the-glyph), with a warning mark when it needs
  sign-in or was refused;
- its name, its email when claudeswitch recorded one, and its state:
  `available`, `no headroom`, `refused · 5h · clears in 36m`, `needs
  sign-in` and so on. The account live in a profile says `in use: work` in
  place of `available` (trouble, such as `needs sign-in`, stays beside it):
  a profile that shares its pool takes another account meanwhile. When the
  row is narrow the email goes first, whole, then the name is cut in the
  middle; the state never is;
- a second line: every profile that may use it (`used by default, work`),
  its figure with the window it is judged by (`55% 7d`), its own Chrome
  profile if it has one, its seat (shortened), and when its login renews
  (`login renews until Nov 7, 2026 at 6:45 PM`; nothing once it needs
  sign-in). When the line is too long, whole parts give way: the seat
  first, then Chrome, then the login, then who uses it (named, then
  counted); the figure stays;
- its plan, in the Plan column: the part before any parenthesis
  (`Max 5x` of `Max 5x (team, enterprise seat)`), the whole plan in its
  tooltip. A long plan wraps to a second line rather than being cut in the
  middle.

Each account is judged by the profile it is live in, else the first profile
that uses it, with that profile's own thresholds: an account live in a
profile that switches at 75% reads `no headroom` at 80%, not `available`.
The figure is the window nearest its own trigger, as the daemon judges it.

**Add Account** at the top right opens [Add account](add-account.md) for
the first profile.

## An account's ⋯ menu

| item | what it does | CLI |
|---|---|---|
| **Rename…** | a sheet, **Rename the Account “personal”**: renames it everywhere (config, vault, state, Chrome mapping). A running daemon is stopped for a moment and started again; if it does not start, an alert says so, else a note says it was done (`Renamed “personal” to “home”`). Return renames only when the button would: the name changed and not empty | `cs account rename <old> <new>` |
| **Move to Pool** → **ops…** | moves it to another pool, after a confirmation, as a drag onto that pool does. **In No Pool…** takes it out of its pool | `cs pool move <id> --to <pool>`, `cs pool remove <pool> <id>` |
| **Sign In…** | opens [Add account](add-account.md) for this account, to replace a dead credential | `cs login <id> --direct` |
| **Move Up** / **Move Down** | one step in the rotation order, within the account's pool: it swaps places with the account above or below it there, the others keeping theirs, as a drag does. Greyed out at the pool's ends, and while the order is being saved, so two quick moves never start two calls. VoiceOver's Move Up and Move Down actions do the same | `cs priority` |
| **Open Chrome** | opens the Chrome profile this account uses; greyed out while it opens | `cs chrome open <id>` |
| **Chrome Profile: …** | which Chrome profile Claude in Chrome uses for this account: **Same as Its Profile** (the default), one of **Your Chrome Profiles**, or **Create a New One** | `cs chrome forget <id>`, `cs chrome add <id> --existing <name>`, `cs chrome add <id>` |
| **Delete…** | removes the account and its stored credential, after a confirmation ("Delete the account “team-3”?") that gives the CLI's own text, naming the account and seat | `cs account delete <id>` |

## Recovery copies

When a swap or a sign-in replaced a live credential that claudeswitch could
not attribute to an account, it kept the old one aside as a recovery copy.

| control | what it does | CLI |
|---|---|---|
| **Show** / **Reload** | reads the list | `cs recovery` |
| **Identify** | looks up who each copy signs in as, one identity call each | `cs recovery --identify` |
| **Restore** → account | puts the copy back as that account's stored credential, after a confirmation | `cs recovery restore <slot> <id>` |
| **Clear…** | deletes the copy, after a confirmation | `cs recovery clear <slot>` |

**Show**/**Reload** and **Identify** are small bordered buttons. While one
of these calls runs, they, **Restore** and **Clear…** are greyed out. A copy
that cannot be read says so in red and cannot be restored.

## Re-login reminders

When an account's sign-in expires within 5 days (its `refresh_expires_at` in
`cs account list --json`), the app posts a notification once a day for that
account, **Sign personal in again**: "personal needs sign-in within 2 days",
or "personal needs sign-in: its login has expired" once it has. **Sign In…** on the notification, or clicking it, opens Settings
with [Add account](add-account.md) signing that account in again. An account
whose expiry is unknown, or that is disabled, is never reminded. macOS asks
once whether ClaudeSwitch may post notifications.

[← Documentation index](../README.md)
