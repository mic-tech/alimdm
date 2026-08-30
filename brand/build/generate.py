"""Regenerate every Ali MDM logo asset from the two sources of truth:

    ali-mdm-logo.svg.bak      the original artwork (red)
    build/fonts/Outfit[wght].ttf

Usage:  python build/generate.py
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FONTS = Path(__file__).resolve().parent / "fonts"
sys.path.insert(0, str(Path(__file__).resolve().parent))

import verify    # noqa: E402
import wordmark  # noqa: E402

ORIGINAL = ROOT / "ali-mdm-logo.svg.bak"

# ---------------------------------------------------------------- palettes ---
# The mark has eight tones. Everything below is ordered darkest -> lightest;
# that ordering is what lets the glass variants map opacity onto shading.
RED = ["167,13,18", "233,39,44", "235,58,71", "236,55,66",
       "237,59,71", "244,82,83", "252,109,109", "252,110,109"]
BLUE = ["13,52,120", "24,86,180", "30,98,190", "33,102,196",
        "35,105,198", "52,130,214", "74,155,232", "76,157,234"]
# 252,109,108 is a stray near-duplicate of 252,109,109 in the original artwork.
RED_ALIAS = {"252,109,108": "252,109,109"}

# Lightest face opaque, each darker face thinner. Reads as shading on a
# background darker than the ink, so these are for dark themes only.
GLASS = [0.10, 0.20, 0.32, 0.38, 0.44, 0.62, 0.92, 1]


def read(path):
    return path.read_text(encoding="utf-8", newline="")


def write(path, text):
    path.write_text(text, encoding="utf-8", newline="")
    print(f"  {path.relative_to(ROOT)}")


def recolour(svg, mapping):
    """Replace each tone via a sentinel, so a new colour can't be re-matched."""
    for i, (src, dst) in enumerate(mapping):
        svg = svg.replace(f"fill:rgb({src})", f"fill:@{i}@")
    for i, (_, dst) in enumerate(mapping):
        svg = svg.replace(f"fill:@{i}@", f"fill:{dst}")
    return svg


def solid(svg_red):
    pairs = [(r, f"rgb({b})") for r, b in zip(RED, BLUE)]
    pairs += [(a, f"rgb({BLUE[RED.index(t)]})") for a, t in RED_ALIAS.items()]
    return recolour(svg_red, pairs)


def glass(svg_blue, ink):
    pairs = [(b, f"rgb({ink});fill-opacity:{o}") for b, o in zip(BLUE, GLASS)]
    return recolour(svg_blue, pairs)


# ----------------------------------------------------------------- lockups ---
FONT = FONTS / "Outfit-SemiBold.ttf"
TEXT = "Ali MDM"
# Second wordmark line. Set to None for a one-line lockup (the original form).
# "Ali MDM Console" on one line runs ~4.95:1, which at a 150px sidebar logo
# leaves a 10px cap — smaller than the nav labels beside it. Stacking the
# product word keeps the lockup near 3:1 and the brand line legible.
SUBTEXT = "Console"
SUB_RATIO = 0.62          # Console's cap height, relative to the brand line
LINE_GAP = 0.30           # brand baseline -> product cap top, relative to cap
TRACKING = 0.015          # Outfit sets tight; a little air suits a wordmark
MARK_H = 100.0            # all geometry below is relative to this
INK = {"light": "rgb(13,52,120)", "dark": "rgb(255,255,255)"}
MARK_SRC = {"light": "ali-mdm-blue1.svg", "dark": "ali-mdm-white-glass.svg"}
MARK_BBOX = (67.28, 26.66, 477.19, 512.23)   # ink bounds of the artwork


def mark(theme, height=MARK_H):
    paths = re.findall(r"(<path[^>]*/>)", read(ROOT / MARK_SRC[theme]))
    k = height / MARK_BBOX[3]
    inner = "\n    ".join(paths)
    g = (f'<g fill-rule="evenodd" transform="scale({k:.5f}) '
         f'translate({-MARK_BBOX[0]:.3f},{-MARK_BBOX[1]:.3f})">\n    {inner}\n  </g>')
    return g, MARK_BBOX[2] * k


def wrap(vb, w, h, inner):
    # fill-rule is set per group, never here: the mark needs evenodd, glyph
    # outlines need nonzero, and inheriting one rule for both breaks the other.
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" '
            f'width="{w:.2f}" height="{h:.2f}" '
            'stroke-linejoin="round" stroke-miterlimit="2">\n'
            f'{inner}\n</svg>\n')


def _lines(cap):
    """Baselines and cap heights for the wordmark, centred on the mark.

    Both lines are emitted into one path: the verifier requires the wordmark
    group to hold exactly one <path>, which also means they share a fill, so
    hierarchy has to come from size rather than colour.
    """
    if not SUBTEXT:
        return [(TEXT, cap, (MARK_H + cap) / 2)]
    cap2 = cap * SUB_RATIO
    lead = cap * LINE_GAP
    top = (MARK_H - (cap + lead + cap2)) / 2
    return [(TEXT, cap, top + cap), (SUBTEXT, cap2, top + cap + lead + cap2)]


def _wordmark(lines, dx=0.0, centre_on=None):
    """Render lines into one path. centre_on: width to centre each line within.

    Returns (path data, ink bounds, (sx, sy)) where the offset is what the
    caller must put on the group's transform; bounds are relative to it. A
    single line keeps its whole placement in the transform, which is what the
    one-line lockups did before SUBTEXT existed and keeps them byte-identical.
    Two lines cannot share one offset, so their placement is baked into the
    path data instead and the offset comes back as zero.
    """
    offs = []
    for text, cap, _ in lines:
        off = dx
        if centre_on is not None:
            _, _, (a0, _, a1, _) = wordmark.block(FONT, text, cap, TRACKING)
            off = centre_on / 2 - (a0 + a1) / 2
        offs.append(off)
    sx, sy = (offs[0], lines[0][2]) if len(lines) == 1 else (0.0, 0.0)

    parts, X0, Y0, X1, Y1 = [], 1e9, 1e9, -1e9, -1e9
    for (text, cap, base), off in zip(lines, offs):
        d, _, (x0, y0, x1, y1) = wordmark.block(FONT, text, cap, TRACKING, off - sx, base - sy)
        parts.append(d)
        X0, Y0, X1, Y1 = min(X0, x0), min(Y0, y0), max(X1, x1), max(Y1, y1)
    return " ".join(parts), (X0, Y0, X1, Y1), (sx, sy)


def horizontal(theme, cap=34.0, gap=22.0):
    m, mw = mark(theme)
    tx = mw + gap
    body, (x0, y0, x1, y1), (sx, sy) = _wordmark(_lines(cap))
    g = (f'  <g transform="translate({tx + sx:.3f},{sy:.3f})" '
         f'fill="{INK[theme]}" fill-rule="nonzero"><path d="{body}"/></g>')
    X1, Y0, Y1 = tx + sx + x1, min(0.0, sy + y0), max(MARK_H, sy + y1)
    return wrap(f"0.00 {Y0:.2f} {X1:.2f} {Y1 - Y0:.2f}", X1, Y1 - Y0, "  " + m + "\n" + g)


def stacked(theme, ratio=1.18, gap=17.0):
    m, mw = mark(theme)
    cap = 20.0
    cap *= (mw * ratio) / wordmark.block(FONT, TEXT, cap, TRACKING)[1]   # fit width to ratio x mark
    _, _, (_, y0, _, _) = wordmark.block(FONT, TEXT, cap, TRACKING)
    base = MARK_H + gap - y0           # ink top sits `gap` below the mark
    lines = [(TEXT, cap, base)]
    if SUBTEXT:
        cap2 = cap * SUB_RATIO
        lines.append((SUBTEXT, cap2, base + cap * LINE_GAP + cap2))
    body, (x0, _, x1, y1), (sx, sy) = _wordmark(lines, centre_on=mw)  # centre each line on the mark
    g = (f'  <g transform="translate({sx:.3f},{sy:.3f})" '
         f'fill="{INK[theme]}" fill-rule="nonzero"><path d="{body}"/></g>')
    X0, X1, Y1 = min(0.0, sx + x0), max(mw, sx + x1), sy + y1
    return wrap(f"{X0:.2f} 0.00 {X1 - X0:.2f} {Y1:.2f}", X1 - X0, Y1, "  " + m + "\n" + g)


# ---------------------------------------------------- android launcher icon ---
# Android adaptive icons are a 108x108dp canvas whose outer ring gets masked
# away; content has to sit inside the middle. The mark is fitted to 60x60
# centred, which clears the 72x72 safe zone with room for the launcher's
# parallax. SVG path data is already valid android:pathData, so the geometry
# carries over exactly rather than being re-traced.
ICON_VIEWPORT = 108.0
ICON_CONTENT = 60.0


def vector_drawable(mono=None):
    """The mark as an Android VectorDrawable. mono: a single fill, or None to
    keep the eight-tone artwork."""
    svg = read(ROOT / MARK_SRC["light"])
    paths = re.findall(r'<path\s+d="([^"]+)"\s+style="([^"]*)"\s*/>', svg)
    if len(paths) != 18:
        raise SystemExit(f"expected 18 mark paths, parsed {len(paths)}")

    x0, y0, w, h = MARK_BBOX
    k = ICON_CONTENT / max(w, h)
    tx = (ICON_VIEWPORT - w * k) / 2 - x0 * k
    ty = (ICON_VIEWPORT - h * k) / 2 - y0 * k

    body = []
    for d, style in paths:
        m = re.search(r"fill:rgb\((\d+),(\d+),(\d+)\)", style)
        fill = mono or ("#%02X%02X%02X" % tuple(int(v) for v in m.groups()))
        body.append(f'    <path android:fillColor="{fill}"\n'
                    f'          android:fillType="evenOdd"\n'
                    f'          android:pathData="{d}"/>')
    inner = "\n".join(body)
    return ('<?xml version="1.0" encoding="utf-8"?>\n'
            '<!-- Generated by brand/build/generate.py - do not edit. -->\n'
            '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n'
            f'    android:width="{ICON_VIEWPORT:.0f}dp"\n'
            f'    android:height="{ICON_VIEWPORT:.0f}dp"\n'
            f'    android:viewportWidth="{ICON_VIEWPORT:.0f}"\n'
            f'    android:viewportHeight="{ICON_VIEWPORT:.0f}">\n'
            f'  <group android:scaleX="{k:.6f}" android:scaleY="{k:.6f}"\n'
            f'         android:translateX="{tx:.4f}" android:translateY="{ty:.4f}">\n'
            f'{inner}\n'
            '  </group>\n'
            '</vector>\n')


CHECKS = []


def emit(name, text, kind):
    write(ROOT / name, text)
    CHECKS.append((ROOT / name, kind))


def main():
    if not FONT.exists():
        from fontTools.ttLib import TTFont
        from fontTools.varLib import instancer
        print("instancing Outfit[wght].ttf at wght=600")
        f = TTFont(FONTS / "Outfit[wght].ttf")
        instancer.instantiateVariableFont(f, {"wght": 600}, inplace=True)
        f.save(FONT)

    print("marks:")
    blue1 = solid(read(ORIGINAL))
    emit("ali-mdm-blue1.svg", blue1, "mark")
    emit("ali-mdm-logo.svg", blue1, "mark")
    emit("ali-mdm-white-glass.svg", glass(blue1, "255,255,255"), "mark")
    emit("ali-mdm-blue-glass.svg", glass(blue1, BLUE[-1]), "mark")

    print("lockups:")
    for theme, suffix in (("light", ""), ("dark", "-dark")):
        emit(f"ali-mdm-lockup-h{suffix}.svg", horizontal(theme), "lockup")
        emit(f"ali-mdm-lockup-v{suffix}.svg", stacked(theme), "lockup")

    print("android icon:")
    write(ROOT / "ali-mdm-icon-foreground.xml", vector_drawable())
    write(ROOT / "ali-mdm-icon-monochrome.xml", vector_drawable(mono="#FFFFFF"))

    print("verifying:")
    glyphs = wordmark.glyph_count(FONT, TEXT, SUBTEXT or "")
    for path, kind in CHECKS:
        n = verify.check(path, kind, glyph_count=glyphs)
        print(f"  {path.name:<28} ok ({n} paths)")


if __name__ == "__main__":
    main()
