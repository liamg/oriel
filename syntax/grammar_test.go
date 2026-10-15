package syntax

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/tools/perf"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// The embedded grammars are what build.sh makes: each decompresses to its SHA256SUMS line, which CI checks a rebuild
// from the pinned sources against.
func TestGrammarChecksums(t *testing.T) {
	f, err := os.Open("grammars/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, _ := strings.Cut(sc.Text(), "  ")
		want[name] = sum
	}
	for _, l := range Builtin().Languages() {
		if l.Grammar == nil {
			continue
		}
		b, err := l.Grammar()
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(b)
		if hex.EncodeToString(got[:]) != want[l.Name+".wasm"] {
			t.Errorf("%s.wasm doesn't match SHA256SUMS", l.Name)
		}
		delete(want, l.Name+".wasm")
	}
	for name := range want {
		t.Errorf("%s is in SHA256SUMS but not built in", name)
	}
}

// ED-11: "The binary grows by under 8 MB for the core set", raised to 12 MB when the set grew (your call, P3):
// the grammars are embedded compressed.
func TestCoreSetSize(t *testing.T) {
	total := 0
	fs.WalkDir(grammarFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			info, _ := d.Info()
			total += int(info.Size())
		}
		return nil
	})
	t.Logf("the core grammars take %.1f MB", float64(total)/(1<<20))
	if total >= 12<<20 {
		t.Errorf("the core grammars take %d bytes; the budget is 12 MB", total)
	}
}

// ED-11: "Opening a file in an already-compiled language is under 20 ms from cache": a runtime that finds the
// compiled grammar on disk compiles, instantiates and parses a fixture in that.
func TestCompiledFromCacheBudget(t *testing.T) {
	perf.Timed(t)
	dir := t.TempDir()
	l, _ := Builtin().Language("go")
	wasm, _, err := l.load()
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile("testdata/highlight/fixture.go")
	open := func() time.Duration {
		rt, err := newRuntime(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close(context.Background())
		start := time.Now()
		cm, err := rt.CompileModule(context.Background(), wasm)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := rt.InstantiateModule(context.Background(), cm, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
		if err != nil {
			t.Fatal(err)
		}
		m := &module{m: mod, fns: map[string]api.Function{}}
		p := &Parser{lang: l, m: m, parser: m.call("parser_new"), queries: map[string]*query{}, symbols: map[string]uint32{}}
		p.Parse(src)
		return time.Since(start)
	}
	cold := open()
	warm := open()
	t.Logf("first open %v, from the cache %v", cold, warm)
	if warm > 20*time.Millisecond && !raceEnabled && testing.CoverMode() == "" {
		t.Errorf("opening from the cache took %v", warm)
	}
}

// The languages added after ED-11, by the files nvim-treesitter's filetypes give them: Terraform and OpenTofu are
// the HCL grammar's terraform dialect.
func TestMoreLanguagesByFile(t *testing.T) {
	for file, want := range map[string]string{
		"main.tf": "terraform", "prod.tfvars": "terraform", "main.tofu": "terraform", "versions.tofuvars": "terraform",
		"job.hcl": "hcl", ".terraformrc": "hcl",
		"App.kt": "kotlin", "build.gradle.kts": "kotlin",
		"Program.cs": "c_sharp",
		"index.php":  "php",
		"main.zig":   "zig", "build.zig.zon": "zig",
		"flake.nix":      "nix",
		"api.proto":      "proto",
		"schema.graphql": "graphql", "q.gql": "graphql",
		"pom.xml": "xml", "icon.svg": "xml",
		"setup.ini":  "ini",
		"lib/app.ex": "elixir", "mix.exs": "elixir", "Main.scala": "scala", "build.sbt": "scala",
		".gitignore": "gitignore", ".dockerignore": "gitignore", "App.vue": "vue", "Page.svelte": "svelte",
	} {
		l, ok := Builtin().ForFile(file, nil)
		if !ok || l.Name != want {
			t.Errorf("%s: %v, want %s", file, l, want)
		}
	}
	if l, ok := Builtin().ForFile("script", []byte("#!/usr/bin/env php")); !ok || l.Name != "php" {
		t.Errorf("a php #! line: %v", l)
	}
}
