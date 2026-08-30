"""Post-build checks on generated SVGs.

These exist because of a real bug: the wordmark's path data was once written
loose inside its <g> instead of inside a <path d="...">. The file parsed, had
the right size and viewBox, and rendered the text as nothing. Structure checks
alone missed it three times, so `no_stray_text` below is the important one.
"""
import re
import xml.etree.ElementTree as ET

SVG = "{http://www.w3.org/2000/svg}"


class VerifyError(AssertionError):
    pass


def _walk(el):
    yield el
    for child in el:
        yield from _walk(child)


def _check(cond, msg, path):
    if not cond:
        raise VerifyError(f"{path.name}: {msg}")


def no_stray_text(root, path):
    """Character data inside a drawing element means markup that renders as nothing."""
    for el in _walk(root):
        for slot, val in (("text", el.text), ("tail", el.tail)):
            if val and val.strip():
                tag = el.tag.replace(SVG, "")
                raise VerifyError(
                    f"{path.name}: stray character data in <{tag}> ({slot}): "
                    f"{val.strip()[:60]!r} -- path data must live in a <path d=...> attribute")


def no_live_text(root, path):
    for el in _walk(root):
        tag = el.tag.replace(SVG, "")
        _check(tag not in ("text", "tspan"), f"<{tag}> found; text must be outlined", path)
        _check("font-family" not in el.attrib, "font-family found; text must be outlined", path)
        style = el.attrib.get("style", "")
        _check("font" not in style, f"font reference in style: {style!r}", path)


def check(path, kind, mark_paths=18, glyph_count=6):
    """kind: 'mark' or 'lockup'."""
    try:
        root = ET.parse(path).getroot()
    except ET.ParseError as e:
        raise VerifyError(f"{path.name}: not well-formed XML: {e}") from None

    _check(root.tag == SVG + "svg", "root is not <svg>", path)
    vb = root.attrib.get("viewBox", "").split()
    _check(len(vb) == 4 and float(vb[2]) > 0 and float(vb[3]) > 0,
           f"bad viewBox: {root.attrib.get('viewBox')!r}", path)
    no_live_text(root, path)
    no_stray_text(root, path)

    paths = [el for el in _walk(root) if el.tag == SVG + "path"]
    _check(all(el.attrib.get("d", "").strip() for el in paths), "a <path> has empty d", path)

    if kind == "mark":
        # a bare mark: flat file, evenodd carried on the root's style attribute
        _check(len(paths) == mark_paths, f"expected {mark_paths} paths, found {len(paths)}", path)
        _check("fill-rule:evenodd" in root.attrib.get("style", "").replace(" ", ""),
               "root style lost fill-rule:evenodd", path)
        return len(paths)

    groups = [el for el in _walk(root) if el.tag == SVG + "g"]
    art = [g for g in groups if g.attrib.get("fill-rule") == "evenodd"]
    _check(len(art) == 1, f"expected 1 evenodd (mark) group, found {len(art)}", path)
    _check(len(art[0].findall(SVG + "path")) == mark_paths,
           f"mark group should have {mark_paths} paths", path)
    _check("fill-rule" not in root.attrib,
           "fill-rule on the root would inherit into the wordmark and punch holes in A and D", path)

    word = [g for g in groups if g.attrib.get("fill-rule") == "nonzero"]
    _check(len(word) == 1, f"expected 1 nonzero (wordmark) group, found {len(word)}", path)
    wp = word[0].findall(SVG + "path")
    _check(len(wp) == 1, f"wordmark group should hold exactly 1 <path>, found {len(wp)}", path)
    d = wp[0].attrib["d"]
    contours = len(re.findall(r"M", d))
    _check(contours >= glyph_count,
           f"wordmark has {contours} contours, expected at least {glyph_count} "
           f"(one per glyph) -- outlines look truncated", path)
    _check(len(paths) == mark_paths + 1, f"expected {mark_paths + 1} paths, found {len(paths)}", path)
    return len(paths)
