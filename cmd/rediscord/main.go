// Command rediscord is a lightweight graphical Discord client that runs on
// Windows 7 and later.
package main

import (
	"log"
	"os"

	"github.com/ayn2op/discordo/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatal(err)
	}
}
