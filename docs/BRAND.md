# claudeswitch brand: Twin rings

A short guide for anyone drawing, writing or coding something that carries the
claudeswitch name: the app, the `cs` CLI, the docs and the screenshots.

## The mark

Two concentric dotted dials, each sweeping 270° with the gap at the bottom.

| Part | What it is | Detail |
|---|---|---|
| Outer ring | the weekly window | 30 dots |
| Inner ring | the 5-hour session | 19 dots |
| Satellite | the week's current reading | one larger dot on the outer ring |

The mark is a gauge, not a decoration. Wherever it is live (the menu-bar glyph,
the popover dials) the lit dots and the satellite show real readings.

In text, the mark is **◎**: `◎ claudeswitch`.

**Clear space.** Keep clear space around the mark equal to the gap between
the two rings. Leave the 90° gap at the bottom empty: it is reserved for the
threshold dot. Never put a badge, a label or the wordmark inside it.

**Minimum size.** 16 px. Below 14 px the glyph drops the satellite and shows
the rings alone, because a satellite that small reads as noise.

## Colour

| Name | Value | Used for |
|---|---|---|
| Graphite | `#17181b` | dark backgrounds, terminal frames |
| Frost | `#f4f7fa` | light backgrounds; primary text on dark |
| Slate | `#5f6670` | secondary text and unlit dots on light; quiet chrome on dark |
| Glacier | `#8fd3ff` | the accent on dark: the mark, the live account, healthy bars |
| Glacier ink | `#2a9fe0` | the accent on light, where Glacier lacks contrast |
| Meltwater | `#b6f0dc` | the second accent: healthy figures and good outcomes |
| Amber | `#f0c75e` | warning: usage is climbing |
| Red | `#ff7a7a` | warning: refused, over the limit, act now |

Light and dark pairs: on light use Glacier ink for the accent, Slate for
secondary text and Graphite for primary text. On dark use Glacier, a light grey
for secondary text and Frost for primary text. Amber and red stay the same in
both, because they carry meaning.

In the app, the brand accent covers what the app draws: buttons, switches,
the followed card, dials, bars and rings. The Settings sidebar selection and
the selected segment of a segmented control keep the user's macOS accent.
Overriding those needs an asset-catalog `AccentColor`, which needs full Xcode
to build; the app builds with the Command Line Tools alone (decided
2026-10-08).

The warnings are semantic. Amber and red appear only when something needs
attention, and nothing else is ever drawn in them.

## Type

- **Wordmark:** Geist, weight 600, tracking −5.5%.
- **Figures and the CLI:** Geist Mono (percentages, countdowns, tables,
  screenshots).
- **App UI:** SF Pro, the system font. The app does not ship Geist for its
  interface.

## The app

- **Dials and bars.** The popover shows the session and the week as two dot
  dials. A switch in the popover changes them to dot bars, and the same choice
  is a setting. Both forms use the same dots, colours and threshold dot.
- **Appearance.** System, Light or Dark. It is set in Advanced → This app and
  in the popover's ⋯ menu.
- **The live glyph.** The menu-bar glyph is the live rings, drawn as a template
  image so it follows the menu bar's own colour. It has four states:

| State | What the glyph shows |
|---|---|
| Healthy | the rings with the current readings |
| Climbing | the rings with a reading near its threshold |
| Switching | the rings during a rotation to another account |
| Needs you | the rings with a warning mark: the live account needs attention (a sign-in, a refusal) |

Because the glyph is a template image, its states are told apart by shape (the
fill and the warning mark), never by colour alone.

## The CLI

### Palette at each colour depth

`cs` chooses the depth from the environment:

- **Truecolour** when `COLORTERM` is `truecolor` or `24bit`.
- **256 colours** otherwise. This is the default.
- **16 colours** when `TERM` names a terminal that stops there (`linux`,
  `vt100`, `ansi`, `xterm-color` and similar).

| Role | Truecolour | 256 | 16 |
|---|---|---|---|
| Glacier: accent | `#8fd3ff` | 117 | cyan (36) |
| Meltwater: healthy figures | `#b6f0dc` | 158 | green (32) |
| Amber: climbing | `#f0c75e` | 221 | yellow (33) |
| Close to the trigger | `#ffa066` | 215 | bold yellow (1;33) |
| Red: act now | `#ff7a7a` | 203 | red (31) |
| Grey: secondary text | 245 | 245 | bright black (90) |

"Close" is the existing band between climbing and act now: a reading at or
above 85% of the trigger. It is a warning tone, not an accent. Dim (SGR 2) and
bold are used as they always were.

The Claude Code status line uses the same palette. Its layout is unchanged.

### The header

On a terminal, `status`, `why` and `doctor` start with one line and then a
blank line:

```
  ◎ claudeswitch v0.5.2 · daemon live · polled 1m ago
```

"◎ claudeswitch" is in Glacier and the rest is dim. The daemon part is the
real state: `daemon live`, `daemon dry-run` or `daemon not running`. "polled"
is the age of the newest reading of any account, or `not polled yet`.

### Dot bars

Each window in the `status` table has a dot bar:

- **Lit dots** `●`, up to the reading. They are Glacier while healthy, amber
  when climbing, orange when close and red when refused or over the trigger.
  An account whose figures can no longer be refreshed is grey.
- **Unlit dots** `·`, dim.
- **The threshold dot**, where the profile rotates away. It is a bold `●` in
  the terminal's default foreground. Without colour it is `◉`, because bold
  alone cannot tell it apart from a lit dot.

A bar has 12 dots. Two bars of 12, plus the plan, the clears column and a
typical account name, keep the table within 100 columns. Narrower terminals
get 6 dots, and the bars are dropped before the table would wrap. `--detail`
keeps its ASCII `[####....]` bars.

### NO_COLOR and pipes

- `NO_COLOR` turns off every colour, but the header is still printed on a
  terminal.
- Piped output and `--json` get no colour and no header. The output is the
  same as before the rebrand, apart from the dot bars.
- `CLICOLOR_FORCE` forces colour, for example when piping into `less -R`.

## Don'ts

- **No new accent colours.** Glacier and Meltwater are the only accents.
- **Warnings never in Glacier.** Anything that needs attention is amber or red.
  A refusal drawn in the accent reads as healthy.
- **No gradients in the CLI.** Each dot is one flat colour. Terminals render
  gradients unevenly, and a gradient bar cannot be read in 16 colours.
- Do not recolour the mark, stretch it, or put anything in its bottom gap.
- Do not use red or amber for decoration, emphasis or branding.
