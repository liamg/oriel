package helper

import (
	"bytes"
	"debug/elf"
	"testing"
)

// What `make helper` embeds runs in any Linux image: a static ELF for this arch, with no dynamic loader.
func TestTheEmbeddedHelperIsStatic(t *testing.T) {
	if !Embedded() {
		t.Skip("built without `make helper`")
	}
	b, err := Binary()
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a Linux binary: %v", err)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Error("the helper is dynamically linked")
		}
	}
	Use([]byte("carried"))
	defer Use(nil)
	if gz, _ := Gzipped(); string(gz) != "carried" {
		t.Errorf("a carried helper wasn't used: %q", gz)
	}
}
