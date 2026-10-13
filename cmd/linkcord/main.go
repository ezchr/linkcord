// Command linkcord is a lightweight graphical Discord client that runs on
// Windows 7 and later.
package main

import (
	"log"
	"os"

	"github.com/ezchr/linkcord/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatal(err)
	}
}
