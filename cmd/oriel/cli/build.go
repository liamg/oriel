package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/liamg/oriel/plugin/nativebuild"
)

const buildUsage = `oriel build --with PKG[@VERSION][=DIR] ... [--oriel VERSION|DIR] [-o FILE] [-tags TAGS]
    a custom oriel with native plugins linked in: each PKG's init calls plugin.Register (plugins.md).
    --oriel is oriel's version, or a checkout to build from; this build's version by default.`

// runBuild is `oriel build --with …`.
func runBuild(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("oriel build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, buildUsage) }
	var with multi
	fs.Var(&with, "with", "a native plugin: PKG[@VERSION][=DIR]; repeat for more")
	base := fs.String("oriel", "", "oriel's version, or a checkout's directory")
	out := fs.String("o", "oriel", "the binary to write")
	tags := fs.String("tags", "", "build tags, comma-separated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(with) == 0 {
		return errors.New("nothing to link in: give --with")
	}
	s := nativebuild.Spec{Output: *out}
	if *tags != "" {
		s.Tags = strings.Split(*tags, ",")
	}
	for _, w := range with {
		m, err := nativebuild.ParseWith(w)
		if err != nil {
			return err
		}
		s.With = append(s.With, m)
	}
	switch b := cmpOr(*base, version); {
	case b == "dev":
		return errors.New("this is a dev build: give --oriel a version or a checkout")
	case strings.HasPrefix(b, "v"):
		s.Base = nativebuild.Module{Path: "github.com/liamg/oriel/cmd/oriel", Version: b}
	default:
		s.Base = nativebuild.Module{Dir: nativebuild.ExpandHome(b)}
	}
	if err := nativebuild.Build(context.Background(), s, stderr); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "wrote", s.Output)
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }
