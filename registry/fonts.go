package registry

// FontsService is the window's typefaces (Fonts).
const FontsService = "fonts"

// Fonts are the families a window can draw with: those installed on the machine, and which draws each of its
// roles. A role is typeset's: "Inter" (the UI's), "JetBrains Mono" (code's), "Space Grotesk" (headings').
type Fonts interface {
	// Families are the installed families, sorted; none until they've been read, which starts off the loop the
	// first time they're asked for, then calls ready.
	Families(ready func()) []string
	// Use draws role with an installed family, off the loop, then calls done with why it couldn't; "" is the
	// bundled face again.
	Use(role, family string, done func(error))
}

func FontsOf(r Contributions) (Fonts, bool) { return ServiceOf[Fonts](r, FontsService) }
