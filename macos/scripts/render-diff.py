#!/usr/bin/env python3
"""Compare fresh renders with the app images the docs publish.

    macos/scripts/render.sh <out>
    python3 macos/scripts/render-diff.py <out> [--write]

For every app image in docs/images it finds the render it comes from (SOURCE,
or the same name in any set when it is not listed) and prints the mean pixel
difference, 0 to 1. Two runs of the same code render alike (0.0000); a
changed line of small text scores about 0.006, a new layout 0.02 or more, and
a change of size always counts. --write copies every render that changed past
NOISE over its docs image. Needs Pillow.
"""
import os
import shutil
import sys

from PIL import Image, ImageChops, ImageStat

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DOCS = os.path.join(ROOT, "docs", "images")

# docs image -> (render set, render file), found 2026-10-28 by matching every
# docs image against every render. A new image not listed is looked for by
# name in every set, closest first; list it once it is published.
SOURCE = {
    "add-account-current.png": ("base", "add-account-current.png"),
    "add-account.png": ("tutorial", "add-account.png"),
    "glyph-states.png": ("base", "glyph-states.png"),
    "menu-more-usage.png": ("base", "menu-more-usage.png"),
    "menu-more.png": ("base", "menu-more.png"),
    "menubar-label.png": ("tutorial", "label-default.png"),
    "popover-bars-dark.png": ("base", "popover-bars-dark.png"),
    "popover-bars.png": ("base", "popover-bars.png"),
    "popover-chrome-signin.png": ("tutorial", "popover-chrome-signin.png"),
    "popover-dark.png": ("hero", "popover-dark.png"),
    "popover-dials-dark.png": ("tutorial", "popover-dark.png"),
    "popover-dials.png": ("tutorial", "popover.png"),
    "popover-update-dark.png": ("base", "popover-update-dark.png"),
    "popover-update.png": ("base", "popover-update.png"),
    "popover-warnings-dark.png": ("base", "popover-expanded-dark.png"),
    "popover-warnings.png": ("base", "popover-expanded.png"),
    "popover.png": ("hero", "popover.png"),
    "settings-accounts-pools.png": ("pools", "settings-accounts.png"),
    "settings-overview.png": ("hero", "settings-overview.png"),
    # The set whose checks fail, so the page's Fix buttons are pictured.
    "settings-health.png": ("many", "settings-health.png"),
    "settings-profiles-pools.png": ("pools", "settings-profiles.png"),
    "sheet-change-pools.png": ("pools", "sheet-change-pools-default.png"),
    "sheet-delete-pool.png": ("pools", "sheet-delete-pool.png"),
    "sheet-new-profile.png": ("tutorial", "sheet-new-profile.png"),
    "update-available-dark.png": ("base", "update-available-dark.png"),
    "update-available.png": ("base", "update-available.png"),
    "update-downloading-dark.png": ("base", "update-downloading-dark.png"),
    "update-downloading.png": ("base", "update-downloading.png"),
    "welcome-dark.png": ("base", "welcome-dark.png"),
    "welcome-profile.png": ("base", "welcome-profile.png"),
    "welcome.png": ("base", "welcome.png"),
}
# Settings panes not listed above come from the tutorial set.
SETTINGS_SET = "tutorial"
# Renders are deterministic now (unchanged frames score 0.0000); a changed
# line of small text scores as little as 0.006, so the cut sits well below.
NOISE = 0.001


def diff(a, b):
    """Mean absolute difference over RGB, 0 to 1, or None when sizes differ."""
    ia, ib = Image.open(a).convert("RGB"), Image.open(b).convert("RGB")
    if ia.size != ib.size:
        return None
    return sum(ImageStat.Stat(ImageChops.difference(ia, ib)).mean) / 3 / 255


def source(name, out):
    if name in SOURCE:
        s, f = SOURCE[name]
        p = os.path.join(out, s, f)
        return [p] if os.path.exists(p) else []
    if name.startswith("settings-"):
        p = os.path.join(out, SETTINGS_SET, name)
        return [p] if os.path.exists(p) else []
    return [os.path.join(out, s, name) for s in sorted(os.listdir(out))
            if os.path.exists(os.path.join(out, s, name))]


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    if len(args) != 1:
        sys.exit(__doc__)
    out, write = args[0], "--write" in sys.argv
    changed = 0
    for name in sorted(os.listdir(DOCS)):
        if not name.endswith(".png"):
            continue
        doc = os.path.join(DOCS, name)
        cands = source(name, out)
        if not cands:
            print(f"  ?        {name}: no render of that name")
            continue
        scored = sorted(((diff(c, doc), c) for c in cands), key=lambda t: (t[0] is None, t[0] or 0))
        d, best = scored[0]
        where = os.path.relpath(best, out)
        if d is not None and d < NOISE:
            print(f"  same     {d:.4f}  {name}  <- {where}")
            continue
        changed += 1
        what = "size" if d is None else f"{d:.4f}"
        print(f"  CHANGED  {what:>6}  {name}  <- {where}")
        if write:
            shutil.copyfile(best, doc)
    print(f"{changed} changed" + (", written to docs/images" if write and changed else ""))


if __name__ == "__main__":
    main()
