# Add account

The sheet that adds an account, or signs in to one again: through a browser, or by saving the login Claude Code has now.

See also: [cs accounts → login, add](../cli/accounts.md) · [GUIDE → Adding an account](../GUIDE.md#adding-an-account) · [Settings → Accounts](settings-accounts.md)

It opens from **+ Add account** in the popover (for the followed profile),
**Add account** in [Settings → Accounts](settings-accounts.md) (no profile
chosen), and **Sign in again...** in an account's ⋯ menu (that account, in
its profile).

## Sign in with browser

<p align="center"><img src="../images/add-account.png" width="520" alt="Sign in with browser: Name, Pool, Organization, Browser, then Open the sign-in page and Paste the code it shows"></p>

| field | what it is | default |
|---|---|---|
| **Name** | the account's name in claudeswitch. Any name works; it is pinned to whichever seat signs in | |
| **Pool** | where a new account goes (D37): every pool, named with the profiles that use it, and No pool (default uses it last). It starts on a pool only the profile it was opened from uses, so nothing is shared unless you choose it. An account that already exists stays in its pool. With an older claudeswitch this row is **Profile** | that profile's unshared pool |
| **Organization** | the organization to sign in to: one an account here is already in, another pasted by id, or whichever the browser is in. Choose one to add a person in a second organization. An account signing in again asks for its own | The one your browser is in |
| **Browser** | the browser the sign-in page opens in. A browser signs in as the account it already holds, so pick one signed in to this account | Default browser |

**Open sign-in page** runs `cs login <name> --direct --no-open`, with
`--pool <pool>` or `--no-pool` for the pool chosen (`--profile <profile>`
with an older claudeswitch), and `--browser` and `--org` when set, then
opens the page. An account signing in again sends neither a pool nor an
organization: it stays in its pool and asks for its own organization. Sign
in, then paste the code the page shows; the app finishes with
`cs login <name> --code <code>`. The credential is checked, stored and added
to the pool. Your current Claude Code session is not touched.

The CLI refuses a mismatch: another organization than the one chosen, a name
already pinned to another seat, or a seat already stored under another name.
The sheet shows its message and opens again, so you can choose another
organization or browser and sign in again.

## Save current login

<p align="center"><img src="../images/add-account-current.png" width="520" alt="Save current login: Save login from a profile, Name, and Add it to pool"></p>

| field | what it is | default |
|---|---|---|
| **Save login from** | the profile whose Claude Code login is saved | the followed profile |
| **Name** | the name to save it under, suggested from the account | suggested |
| **Add it to pool** | the pool it goes into, as **Pool** above. When the source profile uses that pool too, it keeps the login until its own rotation moves on; when it does not, it moves off it. The sheet says which. For an account that already exists this row is **Used by** and is not chosen; with an older claudeswitch it is **Add it to profile** | that profile's unshared pool |

**Save** runs `cs add <name> --from <profile> --pool <pool>` (or `--no-pool`;
`--profile <profile>` for an account that already exists, or with an older
claudeswitch). Use it after `/login` in Claude Code as the account you want
to add. The name in the screenshot is a placeholder.

[← Documentation index](../README.md)
