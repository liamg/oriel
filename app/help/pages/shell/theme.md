---
id: shell/theme
covers: [app.overlay.theme, app.overlay.themepick]
related: [shell/help]
---

# Theme editor

`:Theme` opens the editor: every token on the left, a preview in the middle, and the one you picked on the right.
Edits are overrides: each writes the smallest change into your theme file, and the window repaints. To switch
themes, `:Themes` (`␣T`) previews each as you move through them.

## Tokens {#tokens}

The palette, then semantic tokens, statuses, component tokens and syntax groups. A dot marks the ones your file
overrides. Type to filter.

## Preview {#preview}

The fixture workspace in your theme, or split against the theme yours extends. Click anything in it to pick
what painted it.

## Pick mode {#pick}

`p`, or `:Theme pick`, hides the editor so you can click anything in the real window. Hover shows the token
under the pointer; a click picks it and brings the editor back. `esc` leaves pick mode.

## The chain {#chain}

How the picked token got its value: the token, what it refers to or derives from, down to a palette colour.
Choose the link to change: the top one changes only this, a lower one everything that shares it.

## Value {#value}

Derive writes an expression (`lighten(bg.base, 6%)`, `mix(ink, ember, 3%)`); fixed sets a colour with OKLCH
lightness, chroma and hue, or a hex code.

## Contrast and lint {#contrast}

The WCAG contrast ratio of each pair the token is drawn in, against the ratio it needs: 4.5:1 for text and 3:1
for large text and marks. The lint lists every pair under its ratio and statuses too close to tell apart, with
a fix you can apply.

## The file {#toml}

The strip at the bottom is your theme file as it's written. A built-in theme isn't changed: your edits go into
a local theme in your themes directory that extends it, which becomes your theme.

- `r` resets the picked token to what it inherits.
- `u` or `⌘z` undoes the last edit.

{{keys "theme.*"}}
