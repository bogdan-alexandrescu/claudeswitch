# claudeswitch documentation

Everything the claudeswitch app and CLI can do, by section.

See also: [the project README](../README.md) (install and a short tour)

## Tutorial

New to claudeswitch? [TUTORIAL.md](TUTORIAL.md) takes you from install to
everyday use: first run, a second account, watching it decide, a work
profile, Claude in Chrome, and using it from inside Claude Code, with
screenshots at every step.

## Concepts

[concepts.md](concepts.md) explains the ideas everything else uses: accounts
and seats, profiles and pools, the two windows and their thresholds, how
rotation decides, hot swaps, dry run and live, credentials and recovery, the
call budget and 429s, and Claude in Chrome routing.

## The macOS app

One page per surface, each with screenshots and every control: what it does,
the CLI command it runs, and its default.

- [Menu bar](app/menu-bar.md): the label, the live glyph's four states, and
  Icon only.
- [Popover](app/popover.md): the cards, dials and bars, Switch to best, Open
  Claude Code, the Chrome globe, Pin, banners, and the ⋯ menu.
- [Settings → Profiles](app/settings-profiles.md): pools, per-profile
  thresholds, the Chrome profile picker, new and removed profiles.
- [Settings → Accounts](app/settings-accounts.md): rotation order, each
  account's menu, recovery copies.
- [Settings → Rotation](app/settings-rotation.md): when and where rotation
  moves a profile.
- [Settings → Polling](app/settings-polling.md): how often accounts are read,
  and the call budget.
- [Settings → Daemon](app/settings-daemon.md): status, live or dry run, and
  managing the service.
- [Settings → History](app/settings-history.md): each account's session and
  week over 30 days, with its switches.
- [Settings → Health](app/settings-health.md): every `cs doctor` check, with a
  Fix button for what fails.
- [Settings → Advanced](app/settings-advanced.md): Appearance, dials or bars,
  the terminal, Icon only, the binary, and the advanced settings.
- [Add account](app/add-account.md): signing in with a browser, or saving the
  current login.
- [Claude in Chrome in the app](app/chrome.md): Chrome profiles per profile
  and per account, and the sign-in banner.
- [First-run setup](app/first-run.md): the welcome window's four steps, and
  Set up… to open it again.
- [Updates](app/updates.md): the daily check, and Update, which verifies and
  installs the app and the binary together.
- [Shortcuts, Raycast and the hotkey](app/shortcuts.md): the
  `claudeswitch://` URLs, the Switch to best hotkey, and the notifications'
  Undo, Pin here and Sign in.

## The cs CLI

One page per command group, with every subcommand and flag, an example, the
`--json` form and the exit status.

- [Status](cli/status.md): `status`, `top`, `why`, `plan`, `whoami`.
- [Accounts](cli/accounts.md): `use`, `login`, `add`, `refresh`, `accounts`,
  `account …`, `priority`, `rename`, `remove`, `forget`, `identify`,
  `recovery`.
- [Profiles](cli/profiles.md): `profile …` and `run`.
- [Config](cli/config.md): `config`, and every setting with its default and
  range.
- [Daemon](cli/daemon.md): `daemon …`, `watch`, `uninstall`.
- [Chrome](cli/chrome.md): `chrome …` and a profile's `chrome` setting.
- [Claude Code](cli/claude-code.md): `statusline`, `context`, `session`,
  `history`, `audit`, and the plugin's `/cs` commands.
- [Setup](cli/setup.md): `setup`, `init`, `doctor`, `version`.

## In-depth documents

- [GUIDE.md](GUIDE.md): the full manual, with the reasoning behind each
  behaviour and default.
- [PROFILES.md](PROFILES.md): the multi-profile design and its decisions.
- [DESIGN.md](DESIGN.md): why the program is shaped the way it is, what
  would break it, and what would prove it wrong.
- [APP_CLI.md](APP_CLI.md): the JSON contract between the app and the CLI:
  every command the app runs and what it returns.
- [BRAND.md](BRAND.md): the Twin rings brand: the mark, palette, type, and
  how the app and CLI use them.
- [GROUND_TRUTH.md](GROUND_TRUTH.md): verified facts about Claude Code and
  the usage API that the design rests on, each with the output that proved it.
- [IMPROVEMENTS.md](IMPROVEMENTS.md): the improvements decided after
  comparing with claude-swap (including C2, per-profile Chrome profiles), with
  how each was built.

[← Project README](../README.md)
