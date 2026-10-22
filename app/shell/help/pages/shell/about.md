---
id: shell/about
covers: [about.toggle, about.close, about.website, hud.indicator.about]
related: [shell/update, errors]
---

# About Oriel

The logo at the top left is the title bar's brand. Its cursor blinks while the pointer is on it. Click it for the
About card:

- the version you're running, and how updates stand, with **Check for updates**, **What's new** when a newer
  release is out, or **Restart** once one is installed;
- **Copy report**, the same report the log copies, for an issue;
- **Help**, and **Website**, which opens Oriel's home page in the browser.

Escape, a click outside it, or its `✕` puts the card away.

The brand is the `titlebar.brand` indicator slot: a plugin can turn off the built-in `about` indicator and put its
own there.
