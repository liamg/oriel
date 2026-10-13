// apigen generates the oriel.* Lua API's glue, LuaLS types and reference from script/api (tools/apigen).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/tools/apigen"
)

func main() {
	root := flag.String("root", "../..", "the repository's root, from the API's directory")
	version := flag.String("version", "0.4.0", "oriel.api.version")
	flag.Parse()
	files, err := apigen.Generate(*root, ".", *version)
	if err == nil {
		err = apigen.Write(files)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "apigen:", err)
		os.Exit(1)
	}
	for p := range files {
		fmt.Println("wrote", filepath.Clean(p))
	}
}
