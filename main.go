// Command cohort runs the cohort daemon, or attaches to it from a
// terminal.
//
// Driving is done by the cohort manager agent via ~/.cohort/config.json
// and tmux itself. The only subcommands are maintenance: upgrade and
// version.
package main

import (
	"fmt"
	"log"
	"os"

	"cohort/internal/cohort"
)

// version is set at release build time with -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `usage: cohort            attach (terminal) or run the daemon (systemd/launchd)
       cohort upgrade    install the latest GitHub release
       cohort version    print the version`

func main() {
	var err error
	switch {
	case len(os.Args) == 1:
		err = cohort.Run()
	case len(os.Args) == 2 && os.Args[1] == "upgrade":
		err = cohort.Upgrade(version)
	case len(os.Args) == 2 && os.Args[1] == "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}
