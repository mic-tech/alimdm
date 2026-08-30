"""Outline text from a TTF into SVG path data. No runtime font dependency in the output."""
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.boundsPen import BoundsPen
from fontTools.misc.transform import Transform

_cache = {}


def _font(path):
    if path not in _cache:
        _cache[path] = TTFont(str(path), fontNumber=0)
    return _cache[path]


def cap_height(path, size=100.0):
    f = _font(path)
    try:
        return f["OS/2"].sCapHeight * size / f["head"].unitsPerEm
    except (KeyError, AttributeError):
        return 0.7 * size


def _glyphs(path, text, size, tracking, dx=0.0):
    """Yield (glyphset, glyphname, x_offset, scale) for each drawable glyph."""
    f = _font(path)
    upm, cmap, gs, hmtx = f["head"].unitsPerEm, f.getBestCmap(), f.getGlyphSet(), f["hmtx"]
    sc, x = size / upm, dx
    for ch in text:
        g = cmap.get(ord(ch))
        if g is None:
            continue
        if ch != " ":
            yield gs, g, x, sc
        x += hmtx[g][0] * sc + tracking * size


def advance(path, text, size, tracking):
    f = _font(path)
    upm, cmap, hmtx = f["head"].unitsPerEm, f.getBestCmap(), f["hmtx"]
    x = 0.0
    for ch in text:
        g = cmap.get(ord(ch))
        if g is not None:
            x += hmtx[g][0] * size / upm + tracking * size
    return x - tracking * size


def draw(path, text, size, tracking, dx=0.0, dy=0.0):
    """SVG path data for `text`, baseline at y=dy, first glyph origin at x=dx."""
    parts = []
    for gs, g, x, sc in _glyphs(path, text, size, tracking, dx):
        pen = SVGPathPen(gs)
        gs[g].draw(TransformPen(pen, Transform(sc, 0, 0, -sc, x, dy)))
        d = pen.getCommands()
        if d:
            parts.append(d)
    return " ".join(parts)


def bounds(path, text, size, tracking, dx=0.0, dy=0.0):
    """Ink bounds (x0, y0, x1, y1) in SVG coords: y0 is the top (negative), y1 the bottom."""
    X0 = Y0 = 1e9
    X1 = Y1 = -1e9
    for gs, g, x, sc in _glyphs(path, text, size, tracking, dx):
        bp = BoundsPen(gs)
        gs[g].draw(TransformPen(bp, Transform(sc, 0, 0, -sc, x, dy)))
        if bp.bounds:
            a, b, c, d = bp.bounds
            X0, Y0, X1, Y1 = min(X0, a), min(Y0, b), max(X1, c), max(Y1, d)
    return X0, Y0, X1, Y1


def block(path, text, cap_target, tracking, dx=0.0, dy=0.0):
    """Size the text by cap height. Returns (path data, width, ink bounds)."""
    size = cap_target * 100.0 / cap_height(path, 100.0)
    return (draw(path, text, size, tracking, dx, dy),
            advance(path, text, size, tracking),
            bounds(path, text, size, tracking, dx, dy))


def glyph_count(path, *texts):
    """How many glyphs the given strings actually draw — the verifier's lower
    bound on contour count, which must track whatever text the lockup carries."""
    f = _font(path)
    cmap = f.getBestCmap()
    return sum(1 for t in texts for ch in t if ch != " " and cmap.get(ord(ch)))
