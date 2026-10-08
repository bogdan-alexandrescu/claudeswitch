# cs chrome

Give each account (or each profile) a Chrome profile, so Claude in Chrome can be signed in to the account Claude Code is on.

See also: [app: Claude in Chrome](../app/chrome.md) · [popover banner](../app/popover.md) · [concepts](../concepts.md) · [cs profile](profiles.md) · [GUIDE → Claude in Chrome](../GUIDE.md#claude-in-chrome)

The Claude in Chrome extension keeps its own claude.ai login. claudeswitch
cannot move it, so it routes each account to a Chrome profile instead. Which
Chrome profile an account uses is decided in this order, by every command on
this page, by the daemon's notices and by the app:

1. **the account's own** Chrome profile (`cs chrome add`), rule `account`;
2. **its profile's** Chrome profile (`cs profile set <profile> chrome`), rule
   `profile`;
3. **Chrome's last-used** profile, rule `last_used`.

When none applies (no mapping, and Chrome's profile list cannot be read) the
rule is `none` and the commands that open Chrome refuse. Why the order is
what it is, and the one sign-in per rotation a shared Chrome profile needs:
[GUIDE → Claude in Chrome](../GUIDE.md#claude-in-chrome).

claudeswitch never writes Chrome's files. It reads only the profile list in
Chrome's `Local State` (`profile.info_cache` names and `profile.last_used`),
from `~/Library/Application Support/Google/Chrome/Local State` on macOS, or
`~/.config/google-chrome/Local State` then `~/.config/chromium/Local State` on
Linux. No `cs chrome` command reads the keychain. macOS and Linux only.

## Synopsis

```
cs chrome [open] [<account>]                    [--config PATH] [--json]
cs chrome signin [<account>]                    [--config PATH] [--json]
cs chrome add <account> [--existing <name|folder>] [--config PATH] [--json]
cs chrome profiles                              [--config PATH] [--json]
cs chrome list                                  [--config PATH] [--json]
cs chrome forget <account>                      [--config PATH] [--json]
cs profile set <profile> chrome <name|folder|inherit> [--config PATH] [--json]
```

Flags shared by every `chrome` subcommand:

| flag | default | meaning |
|---|---|---|
| `--config PATH` | `~/.config/claudeswitch/config.toml` | the config file to read |
| `--json` | off | answer with one JSON object on stdout; human text, if any, goes to stderr |
| `--existing NAME` | none | `add` only: map the account to a Chrome profile you already have, by its name ("Work") or folder (`Profile 1`). Any other subcommand with `--existing` is a usage error |

A Chrome profile is named either by Chrome's display name for it or by its
folder (`Default`, `Profile 1`, ...). With Chrome's profile list available, a
folder it lists wins, then an exact name, then a name in another case. Two
profiles with the same name are refused ("name it by its folder"). Without
the list, the argument is taken as a folder.

## cs chrome [open] [\<account\>]

Opens the account's Chrome profile, chosen by the order above. With no
account, it opens the one for the account live in this shell's profile (the
profile your `CLAUDE_CONFIG_DIR` belongs to). `open` is the default
subcommand, so `cs chrome work-1` and `cs chrome open work-1` are the same.

On macOS it runs `open -na "Google Chrome" --args --profile-directory=<folder>`.
On Linux it runs the first of `google-chrome`, `google-chrome-stable`,
`chromium`, `chromium-browser` found on `PATH`, with the same flag.

```
$ cs chrome work-1

  opened Chrome profile "claudeswitch-work-1" for work-1 (work-1's own Chrome profile)
```

With no account the line reads
`opened Chrome profile "<label>" (<rule>): <account> is live in profile <profile>`.

`--json` adds `"opened": true` to the resolution. The fixture the app is
tested with (`cli-chrome-open.json`):

```json
{
  "account": "work-1",
  "opened": true,
  "profile_dir": "claudeswitch-work-1"
}
```

The current binary also includes `profile`, `name` and `rule`, as in
`chrome signin` below.

## cs chrome signin [\<account\>]

Opens the account's Chrome profile (same order, same default account) at the
claude.ai login page and Claude in Chrome's Web Store page, and says what to
do there:

```
  opened the Chrome profile "Work" for work-team (profile work's Chrome profile)

  In that Chrome window:
    1. on claude.ai, sign out if another account shows, then sign in as work-team
    2. open Claude in Chrome (its toolbar icon; the Web Store tab adds it if it is missing),
       sign it out of the account it is on, and sign it in as work-team

  A Chrome profile shared by a profile's accounts needs this after every rotation.
  Give work-team a Chrome profile of its own and Claude in Chrome follows rotations by itself:
    cs chrome add work-team --existing <name>   (or without --existing, a new one)
```

The last three lines appear only when the rule is not `account`. When
claudeswitch has recorded the account's email, it reads
`sign in as <email> (<account>)`. This is what the app's **Sign in as ...**
button runs.

`--json` (`cli-chrome-signin.json`):

```json
{
  "account": "work-team",
  "email": "team@example.com",
  "name": "Work",
  "opened": true,
  "profile": "work",
  "profile_dir": "Profile 1",
  "rule": "profile",
  "urls": [
    "https://claude.ai/login",
    "https://chromewebstore.google.com/detail/fcoeoabgfenejglbffodgkkbkcdhcgfn"
  ]
}
```

## cs chrome add \<account\> [--existing \<name|folder\>]

Gives one account a Chrome profile of its own. An account with its own
Chrome profile routes by itself: browser tasks follow every rotation to it.
The account must be in the config or the vault.

**Without `--existing`**, it opens a new Chrome profile in the folder
`claudeswitch-<account>` at the claude.ai login and the Web Store page, and
records the mapping. Chrome creates the profile when it opens it. Running it
again for the same account reopens that profile. If the account was mapped to
one of your own profiles with `--existing`, it is replaced by a new one.

```
  opened a new Chrome profile for personal (directory "claudeswitch-personal")

  In the new Chrome window:
    1. sign in to claude.ai as personal
    2. in the Web Store tab, add (or enable) Claude in Chrome
    3. open the extension and sign it in, as personal too

  Chrome names the profile itself; rename it there if you like.
  From now on, `cs chrome personal` opens this profile.
```

A second run says `opened the Chrome profile for personal again`.

**With `--existing`**, it records a mapping to a Chrome profile you already
have. It creates nothing and opens nothing:

```
$ cs chrome add research --existing "Research"

  research now uses the Chrome profile "Research" (folder "Profile 2")
  Claude in Chrome there must be signed in as research: cs chrome signin research
```

`--json`, new profile (`cli-chrome-add.json`, an account outside this page's
four):

```json
{
  "account": "work-2",
  "created": true,
  "email": "person2@example.com",
  "opened": true,
  "profile_dir": "claudeswitch-work-2",
  "urls": [
    "https://claude.ai/login",
    "https://chromewebstore.google.com/detail/fcoeoabgfenejglbffodgkkbkcdhcgfn"
  ]
}
```

`--json`, existing profile (`cli-chrome-add-existing.json`):

```json
{
  "account": "work-team",
  "created": false,
  "email": null,
  "existing": true,
  "name": "Work 2",
  "opened": false,
  "profile_dir": "Profile 2",
  "urls": []
}
```

## cs chrome profiles

Lists Chrome's own profiles, from `Local State`, and which claudeswitch
profiles and accounts use each. A folder something points at that Chrome no
longer lists is shown as `(not in Chrome's list)`.

```
$ cs chrome profiles

  Person 1               Default        Chrome's last used
  Work                   Profile 1      profile work
  Research               Profile 2      accounts research
  (not in Chrome's list) claudeswitch-personal accounts personal

  use one for a profile:   cs profile set <profile> chrome <name>
  or for one account:      cs chrome add <account> --existing <name>
```

The layout is the command's own (name, folder, uses); the rows are
reconstructed from the app's `cli-chrome-profiles.json` fixture with this
page's names, not captured from a run. When the
list cannot be read it says so and asks you to name Chrome profiles by folder.

`--json` returns `chrome_profiles` (each with `folder`, `name`, `last_used`,
`in_chrome`, `used_by_profiles`, `used_by_accounts`), `last_used`,
`local_state` (whether `Local State` was read) and `supported` (macOS or
Linux). The app's pickers read it.

## cs chrome list

Lists the accounts that have a Chrome profile of their own, with where each
is live:

```
$ cs chrome list

  personal         claudeswitch-personal  live in default
  research         "Research" (Profile 2)
```

With none: `no Chrome profiles yet; make one with: cs chrome add <account>`.

`--json` returns `chrome_profiles` (each with `account`, `profile_dir`,
`added`, `live_in`, `existing`, `name`), `supported`, and `live`: for each
claudeswitch profile with a live account, that account's resolved Chrome
profile (`account`, `profile`, `profile_dir`, `name`, `rule`) and the rotation
that made it live (`last_from`, `last_switch`). The app's sign-in banner is
built from `live`: a `profile` rule with a `last_from` means the shared Chrome
profile is still signed in as the account before.

## cs chrome forget \<account\>

Drops the account's own mapping. The account falls back to its profile's
Chrome profile, or Chrome's last used. The Chrome profile itself stays in
Chrome.

```
  forgot the Chrome profile "claudeswitch-work-1" for work-1
  the profile itself is still in Chrome; remove it there if you no longer want it
```

`--json` (`cli-chrome-forget.json`):

```json
{
  "account": "work-1",
  "forgotten": true,
  "profile_dir": "claudeswitch-work-1"
}
```

An account with no mapping is refused with `not_found`.

## cs profile set \<profile\> chrome \<name|folder|inherit\>

Sets the Chrome profile a claudeswitch profile's accounts use when they have
none of their own. The config stores the folder; a display name is resolved
through `Local State`. `inherit` (or an empty value) removes the setting,
going back to Chrome's last used. The profile must be declared with a
`[[profile]]` block: with none, give accounts their own Chrome profile instead.
The other `profile set` keys are on [cs profile](profiles.md).

```
$ cs profile set work chrome "Work"
  profile work: chrome = "Work" (Profile 1)

$ cs profile set work chrome inherit
  profile work: chrome is Chrome's last-used profile
```

`--json` (`cli-profile-set-chrome.json`):

```json
{
  "effective": "Profile 1",
  "key": "chrome",
  "name": "Work",
  "override": "Profile 1",
  "path": "/tmp/config.toml",
  "profile": "work"
}
```

With `inherit`, `override` and `name` are `null` and `effective` is Chrome's
last-used folder (`cli-profile-set-chrome-inherit.json`).

## After a rotation

The daemon's notification and `cs use` add one line when Chrome profiles are
in use:

- to an account with its own Chrome profile:
  `Claude in Chrome: use the work-team Chrome profile (cs chrome work-team)`;
- to one that uses its profile's Chrome profile:
  `Claude in Chrome in "Work" is still signed in as work-1; sign it in as work-team (cs chrome signin work-team)`
  (or `may be signed in as another account` when the account before had its
  own);
- to one with neither, once any account has a Chrome profile:
  `Claude in Chrome: work-team has no Chrome profile — cs chrome add work-team`.

Nothing is said when nobody has set up a Chrome profile.

## cs chrome hint (plugin hook)

`cs chrome hint [--config PATH]` is run by the plugin's `PostToolUseFailure`
hook after a Claude in Chrome tool call fails. It reads the hook's JSON on
stdin, and only when the tool's `error` says "same claude.ai account" or "not
connected" does it tell Claude which account Claude Code is on and the
command to run, once per rotation of the session's profile. It never reads a
successful call's output (that is page content). It is not meant to be run by
hand. See [cs and Claude Code](claude-code.md#the-plugin).

## Exit status

| status | when |
|---|---|
| 0 | success |
| 1 | any error. With `--json` the error is printed on stdout as `{"error": {"code", "message", "hint"}}`; otherwise `claudeswitch: <message>` on stderr |
| 2 | `chrome hint` only: it printed a hint for Claude on stderr (every other case of `hint` exits 0) |

Error codes: `usage` (wrong arguments), `config_invalid`, `not_found` (no such
account, no mapping to forget, no Chrome profile resolvable, no Chrome on
`PATH` on Linux), `invalid_value` (a bad folder, or an ambiguous name),
`unsupported_platform` (not macOS or Linux), `failed` (Chrome could not be
started, or the state could not be saved).

[← Documentation index](../README.md)
