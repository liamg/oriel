package apptest

// Desktop is a clipboard in memory, and a browser that opens nothing but notes what it was asked to.
type Desktop struct {
	Text   string
	Files  []string // copied in the file manager
	Image  []byte   // a copied picture, as PNG
	Opened []string
	Shown  []string // revealed in the file manager
}

func (d *Desktop) SetClipboard(s string) { d.Text, d.Files, d.Image = s, nil, nil }

func (d *Desktop) SetClipboardImage(png []byte) error {
	d.Text, d.Files, d.Image = "", nil, png
	return nil
}

func (d *Desktop) ReadClipboard() (string, bool) { return d.Text, d.Text != "" }

func (d *Desktop) ReadClipboardFiles() []string { return d.Files }

func (d *Desktop) ReadClipboardImage() ([]byte, bool) { return d.Image, len(d.Image) > 0 }

func (d *Desktop) Reveal(p string) error {
	d.Shown = append(d.Shown, p)
	return nil
}

func (d *Desktop) OpenURL(u string) error {
	d.Opened = append(d.Opened, u)
	return nil
}
