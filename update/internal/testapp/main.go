// Command testapp is a tiny Oriel for update's tests: built at a version, healthy or not, it boots through the
// installer, re-execs onto the previous version after a rollback, and prints what it runs as JSON.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/liamg/oriel/update"
)

var (
	version = "dev"
	healthy = "true"
)

// Status is what the test reads back.
type Status struct {
	Version    string           `json:"version"`
	Pending    string           `json:"pending,omitempty"`
	RolledBack *update.Rollback `json:"rolledBack,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("oriel", version)
		return
	}
	fs := flag.NewFlagSet("boot", flag.ExitOnError)
	dir := fs.String("dir", "", "the updates directory")
	target := fs.String("target", "", "the installed binary or bundle")
	exe := fs.String("exe", "", "a bundle's executable, inside it")
	_ = fs.Parse(os.Args[2:])
	in := &update.Installer{Target: *target, Exe: *exe, Dir: *dir}
	out, err := in.Boot(context.Background(), func(context.Context) error {
		if healthy != "true" {
			return errors.New("unhealthy on purpose")
		}
		return nil
	})
	if err != nil {
		log.Fatalf("boot: %v", err)
	}
	if out.RolledBack != nil {
		log.Fatal(update.Reexec(in.Executable()))
	}
	st := Status{Version: version}
	if p, ok := in.Pending(); ok {
		st.Pending = p.Version
	}
	if rb, ok := in.Last(); ok {
		st.RolledBack = &rb
	}
	_ = json.NewEncoder(os.Stdout).Encode(st)
}
