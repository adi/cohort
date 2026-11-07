// Command cohort runs the cohort daemon.
//
// It takes no CLI options by design: all driving is done by the cohort
// manager agent via ~/.cohort/config.json and tmux itself.
package main

import (
	"log"

	"cohort/internal/cohort"
)

func main() {
	if err := cohort.Run(); err != nil {
		log.Fatal(err)
	}
}
