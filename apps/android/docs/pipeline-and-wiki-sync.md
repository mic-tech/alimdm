# Wiki Sync — not used in this fork

[Docs Home](README.md)

Upstream FreeKiosk publishes this `docs/` folder to a GitHub Wiki with a
`docs-to-wiki-sync.yml` Actions workflow, which is why these pages were written
with wiki-style links and a "the wiki is generated, never edit it there" warning.

**None of that runs here.** This fork has no such workflow — the only workflows
are `server-image.yml` and `delete-image-tag.yml` — and the repository's wiki is
disabled. These files are read directly in the repository, and their links point
at real filenames rather than wiki page names.

The page is kept as a marker so nobody re-adds the wiki-style links wondering why
they are gone. If you ever do want the wiki, take the workflow from upstream and
be aware it rewrites every link.
