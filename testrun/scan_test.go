package testrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestScanFindsEveryTestFile(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("cp", "-r", fixtureRoot(t, "go/watch")+"/.", root).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, d := range []string{"vendor/v", ".git/g", "testdata/td", "a/node_modules/n"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
		os.WriteFile(filepath.Join(root, d, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestHidden(t *testing.T) {}\n"), 0o644)
	}
	ws := Workspace{Root: root}
	var got []string
	for _, it := range Builtin().Scan(context.Background(), ws) {
		got = append(got, it.Package+" "+it.Name)
	}
	slices.Sort(got)
	if s := strings.Join(got, ", "); s != "example.com/watch/a TestDouble, example.com/watch/a TestSign, example.com/watch/b TestQuadruple, example.com/watch/c TestSixteen, example.com/watch/d TestHello" {
		t.Errorf("scanned %s", s)
	}
	gone := filepath.Join(root, "d", "d_test.go")
	os.Remove(gone)
	if items := Builtin().ScanFile(ws, gone); len(items) != 0 {
		t.Errorf("a deleted file has tests: %+v", items)
	}
}

func TestGoPackageDir(t *testing.T) {
	ws, root := goBasic(t)
	for pkg, want := range map[string]string{"example.com/basic": root, "example.com/basic/calc": filepath.Join(root, "calc")} {
		if d, ok := (&Go{}).PackageDir(ws, pkg); !ok || d != want {
			t.Errorf("%s: %q %v, want %q", pkg, d, ok, want)
		}
	}
	if _, ok := (&Go{}).PackageDir(ws, "example.com/other"); ok {
		t.Error("a package outside the module has a directory")
	}
}

// A failure's location, printed relative to its package, resolves when the run named no test of that package.
func TestExecResolvesLocWithoutItems(t *testing.T) {
	ws, root := goBasic(t)
	x, err := Start(context.Background(), Request{Adapter: &Go{}, Workspace: ws, Items: []Item{{Package: "example.com/basic/calc", Kind: KindPackage}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := x.Wait()
	r, ok := run.Result(Key("example.com/basic/calc", "TestGotWant"))
	if !ok || r.Failure == nil {
		t.Fatalf("result = %+v; %s %s %s", r, run.Args, run.Raw, run.Stderr)
	}
	if want := filepath.Join(root, "calc", "calc_test.go"); r.Failure.Loc == nil || r.Failure.Loc.File != want {
		t.Errorf("loc = %+v, want %s", r.Failure.Loc, want)
	}
}
