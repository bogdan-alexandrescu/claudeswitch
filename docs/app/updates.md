# Updates

The app checks once a day for a newer release, shows what it found in an
update window, and updates the app and the `claudeswitch` binary together
under a progress bar.

See also: [Popover](popover.md) · [Settings → Advanced](settings-advanced.md) · [Settings → Daemon](settings-daemon.md)

<p align="center"><img src="../images/update-available.png" width="500" alt="The update window: ClaudeSwitch 0.5.6 is available, You have 0.5.5 and what installing does, What’s new with the release's notes, Downloads 10.8 MB from GitHub, Release Notes on GitHub, Later and Install Update"></p>

## The check

Once a day the app makes one unauthenticated call to GitHub:
`GET https://api.github.com/repos/<owner>/claudeswitch/releases/latest`
for the public repository, https://github.com/bogdan-alexandrescu/claudeswitch,
whichever tree the app was built from (before 0.8.9 a build from a source
tree other than the public one asked an address that does not exist, and
never offered an update). It compares the release's tag with
the app's own version and ignores drafts and pre-releases.

A failed check is logged (subsystem `xyz.claudeswitch.menubar`, category
`flows`) and tried again the next day. It is kept apart from the last good
check: Settings → Advanced says `last check failed 5m ago` (why, in its
tooltip) instead of when it last checked, and the Overview's Updates tile
says **Last check failed** instead of **Up to date**.

| control | where | what it does | default |
|---|---|---|---|
| **Check for updates** | Settings → Advanced → This app | off makes no call at all, and hides an offer already found, and one a check still under way finds after it is turned off; turned on again, it shows what the last check found. The line under it gives this version, a newer one found, and when it last checked (`This is 0.8.18 · checked 2h ago`), or `Version unknown` for a build with no version | on |
| **Check Now** | under it | checks at once and opens the update window. Greyed out while the check is off | |
| **Check for Updates…** | the menu-bar icon's menu (right-click or Control-click the icon) | the same. Greyed out while the check is off | |
| the **Updates** tile | [Settings → Overview](settings-overview.md) | opens the offer, or checks. While the check is off it opens Settings → Advanced instead, where it is turned on | |

## The update window

**Check for Updates…** and **Check Now** open it, and it says what the
check found:

- **Checking for updates…**, with a spinner beside `Asking GitHub for the
  latest release…`, and **Cancel**;
- **ClaudeSwitch is up to date** (`0.8.18 is the latest version.`, or that
  this copy is newer than the latest release), with **OK**;
- **This copy’s version is unknown**, with **OK**: a build with no version
  of its own (from `swift run`) has nothing to compare the release with,
  so the window names the latest release rather than calling it up to date;
- **Could not check for updates**, with why, **OK** and **Try Again**;
- **Checking for updates is off**, if the window is asked to check while
  Advanced → Check for updates is off (**Try Again** after turning it off,
  say): `While it is off, the app never contacts GitHub. Turn On checks
  now, then once a day.` No call is made. **Turn On** turns the check on
  and checks; **Close** leaves it off;
- the offer.

Escape closes the window where **OK** is its only button. A newer release
found by the daily check shows at the popover's foot as **0.5.6 available ·
Update…**, which opens the same offer.

The offer names the version and the one you have, says what installing
does, shows **What’s new** (the release's notes, a bullet's wrapped lines
under its text; GitHub's "Full Changelog" line is left out), how much it
downloads (`Downloads 10.8 MB from GitHub`: tenths of a megabyte under
100 MB), and **Release Notes on GitHub**. **Later** closes it;
**Install Update** starts the install of the version the window shows,
also when the check was turned off while the window was open.

<p align="center"><img src="../images/update-downloading.png" width="500" alt="The update window while installing: Updating to 0.5.6, Downloading… 4.2 MB of 10.8 MB, a progress bar 38% full with 38% beside it, and Cancel"></p>

## Install Update

The progress bar, in the accent colour, follows each step, and the
popover's foot shows it too (click it to bring the window back). While it
downloads, the bar and the figure beside it are the download's own share,
so a bar 38% full sits beside `38%` and `4.2 MB of 10.8 MB`. The quick
steps after the download start the bar again and fill it a fifth each;
they have no figure, so the bar runs the window's full width. Install:

1. reads the latest release again;
2. downloads the app zip (`ClaudeSwitch-<version>-macos.zip`), its
   `.sha256`, this Mac's binary archive
   (`claudeswitch_<tag>_darwin_arm64.tar.gz` or `…_darwin_amd64.tar.gz`)
   and `checksums.txt` (**Downloading… 4.2 MB of 10.8 MB**);
3. checks both downloads against their published SHA-256, before anything
   is changed (**Checking the downloads against the release’s
   checksums…**);
4. unpacks them in a temporary folder (`tar`, `ditto`);
5. replaces the binary the app runs (the one in Settings → Advanced →
   claudeswitch, followed through links to the file itself) with
   `install -m 0755` (**Installing claudeswitch…**);
6. moves the running app aside and puts the new bundle in its place
   (**Installing ClaudeSwitch…**);
7. restarts the daemon on the new binary (`claudeswitch daemon restart
   --json`; no daemon installed is fine) (**Restarting the daemon on the
   new claudeswitch…**);
8. relaunches the app (**Reopening ClaudeSwitch…**), which opens the window
   once more to say **ClaudeSwitch is now 0.5.6**. Sheets and dialogs still
   open (an editor in Settings, an alert) are closed first, dropping what
   was typed in them, since an open one refuses the quit; a helper waits
   for the old app to go, ends it if it is still there after five seconds,
   then opens the new one.

**Cancel** works while it downloads and checks: nothing has been replaced
yet, and the offer comes back. From step 5 on it is greyed out, since half
an update is worse than a whole one, and the line under the bar says **Too
late to cancel: the new files are going in.** (then **…the new files are
in.**). Closing the window does not stop an install.

**Any failure leaves both as they were** and says why in the window (**The
update did not install**), ending `The app and the claudeswitch command were
not changed.`, with **Release Page on GitHub** (the release's page, where
it can be downloaded by hand), **Close** and **Try Again**. When the
release itself is at fault (a file missing from it, a checksum it does not
list, a download that does not match its checksum), **Try Again** fails the
same way, so the message goes on `Try again later, or download it from the
release page.` A download that fails or does not match its
checksum stops before anything is touched; a binary or app that cannot be
replaced (a folder you cannot write, say) is put back; a daemon that will
not restart on the new binary puts both back and restarts it on the old one
("…so the app and the claudeswitch command were put back as they were").
The popover's foot shows the failure as **Could not update**, until its ×.

Before anything is replaced, both downloads must be signed with the
ClaudeSwitch Developer ID: `codesign --verify` against a requirement naming
its team (`UpdateSource.team`). The checksums are published beside the files
they check, so they catch a broken download but not a swapped one; the
signature catches that. A file that fails stops the update with nothing
touched: `The ClaudeSwitch app in 0.9.0 is not signed by the ClaudeSwitch
developer, so it was not installed. The app and the claudeswitch command were
not changed. Do not install this release by hand.` Downloaded by the app
itself, the new app is not quarantined either way.

[← Documentation index](../README.md)
