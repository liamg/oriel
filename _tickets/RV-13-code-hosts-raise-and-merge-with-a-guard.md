---
id: RV-13
title: "Code hosts: raise and merge on any host, Merge with a guard"
phase: P7
status: review
area: Review
module: registry, app, host, gh
depends_on: [RV-9, FG-6]
---

# RV-13 · Code hosts: raise and merge on any host, Merge with a guard

**Phase** 7 · the rest · **After** RV-9, FG-6

## Design

- `_design/plugins.md` ("As built (code hosts)")
- `_design/scripting.md` ("As built (code hosts)")
- `_design/ui.md` ("18 · Ship", Merge)

## Mockups

![18-git](../_design/mockups/18-git.png)

## Done when

`registry.CodeHost` replaces the review source: one contribution a host for reviews, Ship's PR step and merging, with
its own words ("Raise MR"). Ship reads and raises through it, GitHub built in with no change to how it behaves.
**Merge…** on an open PR shows what stands in the way first, unresolved reviewer comments and Oriel threads counted
apart, **Continue** or **Cancel**, Continue off where the host would refuse; it merges at the head shown with
`ship.merge_method`, else the repo's default, else squash. A self-hosted server is found by `code.hosts`, gh's and
glab's configs, the URL's shape or a probe, else asked, and set up once a hostname (`code.setup`: API URL, its own
token). Against the fake GitHub.

As built: `registry/codehost.go`, `app/codehost.go`, `app/codehost_github.go`, `app/merge.go`, `app/codesites.go`, `app/codesetup.go`, `host/githubpr.go`,
`gh/merge.go` (plugins.md, "As built (code hosts)").
