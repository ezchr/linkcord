//go:build windows && !no_voice

package golive

import "testing"

// TestDescendsReusedID checks that a process given the ID of a program's
// parent that ended is not taken for that parent.
func TestDescendsReusedID(t *testing.T) {
	procs := procTable{
		10: {parent: 0, exe: "explorer.exe", started: 1},
		20: {parent: 10, exe: "game.exe", started: 5},
		30: {parent: 20, exe: "renderer.exe", started: 6},
		// 40 started 8, after the game it is given as the parent of.
		40: {parent: 10, exe: "new.exe", started: 8},
		50: {parent: 40, exe: "child.exe", started: 9},
		60: {parent: 40, exe: "orphan.exe", started: 3},
	}
	if !procs.descends(30, 20) || !procs.descends(50, 40) {
		t.Fatal("real parents are not found")
	}
	if procs.descends(60, 40) {
		t.Fatal("a process that started after the program is taken for its parent")
	}
}
