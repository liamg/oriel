// Command testworker is a tiny worker for update's tests: built at a version, healthy or not, it boots through the
// installer, dials the test's socket and takes updates over the wire.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/liamg/oriel/update"
	"github.com/liamg/oriel/wire"
)

var (
	version = "dev"
	healthy = "true"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("oriel", version)
		return
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socket := fs.String("socket", "", "the app's socket")
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
		log.Printf("%s rolled back: %s", out.RolledBack.Version, out.RolledBack.Reason)
		log.Fatal(update.Reexec(in.Executable()))
	}
	raw, err := net.Dial("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := wire.Client(raw)
	if err != nil {
		log.Fatal(err)
	}
	r := &update.Receiver{Installer: in, Keys: update.Keys(), Version: version, Restart: func() {
		log.Fatal(update.Reexec(in.Executable()))
	}}
	if *exe != "" {
		r.Kind = update.KindBundle
	}
	r.Serve(conn)
	<-conn.Done()
}
