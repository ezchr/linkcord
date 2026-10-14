// Command linkcord-patch makes the patch that lets the updater download only
// what changed between two releases:
//
//	go run ./cmd/linkcord-patch OLD.exe NEW.exe linkcord-vOLD.patch
//
// OLD.exe is the previous release's executable as GitHub serves it, NEW.exe
// the one being released. The patch is uploaded beside NEW.exe, named for
// OLD's version (see update.PatchName); the updater checks that what it makes
// is NEW.exe exactly, and downloads the whole file otherwise.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/gabstv/go-bsdiff/pkg/bsdiff"
	"github.com/gabstv/go-bsdiff/pkg/bspatch"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: linkcord-patch OLD.exe NEW.exe OUT.patch")
		os.Exit(2)
	}
	old, err := os.ReadFile(os.Args[1])
	check(err)
	nw, err := os.ReadFile(os.Args[2])
	check(err)
	patch, err := bsdiff.Bytes(old, nw)
	check(err)
	// The patch must give back NEW.exe exactly before it is published.
	out, err := bspatch.Bytes(old, patch)
	check(err)
	if a, b := sha256.Sum256(out), sha256.Sum256(nw); !bytes.Equal(a[:], b[:]) {
		check(fmt.Errorf("the patch does not reproduce %s", os.Args[2]))
	}
	check(os.WriteFile(os.Args[3], patch, 0o644))
	fmt.Printf("%s: %d bytes, %.1f%% of %d\n", os.Args[3], len(patch), 100*float64(len(patch))/float64(len(nw)), len(nw))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkcord-patch:", err)
		os.Exit(1)
	}
}
