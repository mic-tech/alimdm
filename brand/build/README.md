# Ali MDM logo assets

Every SVG in the project root is generated. Don't hand-edit them — change
`build/generate.py` and re-run it, or your edit disappears on the next build.

## Regenerate

Run from `brand/` (the venv is gitignored; the build itself is
location-independent, so it works from wherever this directory lives):

```
python3 -m venv .venv
.venv/bin/pip install -r build/requirements.txt      # .venv/Scripts/ on Windows
.venv/bin/python build/generate.py
```

Output is deterministic: a clean run reproduces the current files byte for byte.

## Sources of truth

| Input | Role |
|---|---|
| `ali-mdm-logo.af` | the upstream Affinity design file for the mark. Re-export to `ali-mdm-logo.svg.bak` if the geometry ever changes. **Not in this repo** — it lives wherever the design was authored. Everything here is reproducible without it, but changing the mark's geometry is not. |
| `ali-mdm-logo.svg.bak` | that export, in its original red. Every generated SVG is derived from it. |
| `build/fonts/Outfit[wght].ttf` | variable font. Instanced to `wght=600` on first run if `Outfit-SemiBold.ttf` is absent. |

## Output

| File | Use |
|---|---|
| `ali-mdm-logo.svg`, `ali-mdm-blue1.svg` | the mark, solid blue — **light theme** |
| `ali-mdm-white-glass.svg` | the mark in translucent white — **dark theme** |
| `ali-mdm-blue-glass.svg` | the mark in translucent light blue — dark theme, softer alternative |
| `ali-mdm-lockup-h.svg` | mark + wordmark, horizontal — **primary lockup** |
| `ali-mdm-lockup-v.svg` | mark + wordmark, stacked |
| `ali-mdm-lockup-h-dark.svg`, `ali-mdm-lockup-v-dark.svg` | the same two for dark backgrounds |

## Palette

Eight tones, darkest to lightest. The ordering matters: the glass variants map
opacity onto it, so shading survives the translation.

```
13,52,120  24,86,180  30,98,190  33,102,196  35,105,198  52,130,214  74,155,232  76,157,234
```

The glass variants make the lightest face opaque and each darker face thinner,
so they only read correctly on a background darker than the ink. On a light
background they vanish — that is what the solid version is for.

## Lockup geometry

All relative to a mark height of 100, so it scales without re-tuning.

- **Horizontal** — cap height 34, gap 22. The cap box is centred on the mark
  rather than baseline-aligned; the mark has no baseline, and aligning to one
  drops the text visibly low.
- **Stacked** — the wordmark is fitted to 1.18x the mark width and the cap
  height falls out of that, rather than being picked by eye. Gap 17 from the
  mark's bottom to the cap top, centred on the mark's optical centre.
- **Tracking** 1.5%.

## Two-line wordmark

`TEXT` is the brand line; `SUBTEXT` is an optional product line below it. Set
`SUBTEXT = None` for the original one-line lockup — that path is unchanged and
still reproduces the old files byte for byte.

`SUBTEXT` exists because "Ali MDM Console" set on one line runs about 4.95:1.
At the console's 150px sidebar logo that leaves a 10px cap height, smaller than
the 13px nav labels beside it. Stacking the product word holds the lockup at
3.02:1 — the same as the one-line version — so the brand line keeps its size
and the horizontal lockup remains a drop-in at any width it was used before.

- `SUB_RATIO` 0.62 — product cap height relative to the brand line.
- `LINE_GAP` 0.30 — brand baseline to product cap top, relative to the brand cap.
- Horizontal: both lines left-aligned, the two-line block centred on the mark.
- Stacked: each line independently centred on the mark's optical centre.

Both lines are emitted into a **single** `<path>`, because the verifier requires
exactly one path in the wordmark group. That also means they share one `fill`,
so hierarchy between the lines has to come from size, not colour.

## Verification

`generate.py` runs `verify.py` over every file it writes and exits non-zero on
failure, so a broken asset cannot be written silently. The checks:

- parses as well-formed XML, has a positive-area viewBox
- no `<text>`, `<tspan>`, `font-family`, or font reference in a style attribute
- **no stray character data inside any element.** This is the one that matters:
  path data written outside a `d=` attribute produces a file that parses, has
  the right dimensions, and draws nothing. Structural checks miss it entirely.
- marks: 18 paths, `fill-rule:evenodd` still on the root style
- lockups: 19 paths, exactly one evenodd (mark) group and one nonzero
  (wordmark) group, the wordmark group holds exactly one non-empty `<path>`
  with at least one contour per glyph, and no `fill-rule` on the root.
  `generate.py` passes the real glyph count for `TEXT` + `SUBTEXT`, so the
  contour floor tracks the text instead of sitting at a stale constant — with
  the old hardcoded 6, a lockup that silently dropped its entire second line
  still passed.

Each check corresponds to a bug that actually shipped during this build. All
three are covered by reintroducing them into a throwaway copy and confirming
the build fails.

## fill-rule

Set per group, never on the root `<svg>`. The mark needs `evenodd`; glyph
outlines need `nonzero`. Letting one rule inherit to both punches holes in any
glyph whose contours overlap — `A` and `D` in "Ali MDM" both do.

## Font licence

Outfit is licensed under the SIL Open Font License 1.1 (`build/fonts/OFL.txt`),
which permits embedding its outlines in a logo, commercial use included. The
generated SVGs contain outlines only and have no runtime font dependency.

## Where these end up

The console does not read this directory at build time — Vite only bundles
files under `console/`. Three assets are copied in, and a change here means
re-copying:

| From | To | Used for |
|---|---|---|
| `ali-mdm-lockup-h.svg` | `apps/console/src/assets/` | sidebar, expanded |
| `ali-mdm-logo.svg` | `apps/console/src/assets/` | sidebar collapsed, and the login card |
| `ali-mdm-logo.svg` | `apps/console/public/favicon.svg` | browser tab icon |
| `ali-mdm-icon-foreground.xml` | `apps/android/.../res/drawable/ic_launcher_foreground.xml` | Android adaptive icon |
| `ali-mdm-icon-monochrome.xml` | `apps/android/.../res/drawable/ic_launcher_monochrome.xml` | Android themed icon |

```
cp brand/ali-mdm-lockup-h.svg brand/ali-mdm-logo.svg apps/console/src/assets/
cp brand/ali-mdm-logo.svg apps/console/public/favicon.svg
RES=apps/android/android/app/src/main/res
cp brand/ali-mdm-icon-foreground.xml $RES/drawable/ic_launcher_foreground.xml
cp brand/ali-mdm-icon-monochrome.xml $RES/drawable/ic_launcher_monochrome.xml
```

The Android icons are VectorDrawables generated from the same mark — SVG path
data is already valid `android:pathData`, so the geometry is carried over rather
than re-traced. They cover API 26+ (adaptive icons). The legacy
`mipmap-*/ic_launcher.png` rasters for API 24-25 are **not** generated here and
still carry the old artwork; regenerating them needs a rasteriser.

The dark and glass variants are unused so far: the console is light-themed
throughout, and those only read correctly on a background darker than the ink.
