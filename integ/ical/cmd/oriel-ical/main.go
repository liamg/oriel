// Command oriel-ical is the calendar plugin's process: it reads iCal feeds for the plugin's Lua, over JSON-RPC on
// stdin and stdout, in the sandbox its net grant opens.
package main

import (
	"os"
	_ "time/tzdata" // a feed's TZIDs, whatever the sandbox lets it read

	"github.com/liamg/oriel/integ/ical"
)

func main() {
	ical.Serve(os.Stdin, os.Stdout, &ical.Server{})
}
