package gui

import (
	"os"
	"testing"
	"time"
)

func TestGameMatch(t *testing.T) {
	list := indexGames([]knownGame{
		{ID: "1", Name: "Grand Theft Auto V", Paths: []string{"gta5.exe", "grand theft auto v/gta5.exe"}},
		{ID: "2", Name: "Other GTA", Paths: []string{"gta5.exe"}},
		{ID: "3", Name: "Rakion", Paths: []string{"bin/rakion.bin"}},
	})
	for exe, want := range map[string]string{
		`C:\Games\Grand Theft Auto V\GTA5.exe`: "1",
		`D:\gta5.exe`:                          "1",
		`C:\Rakion\bin\rakion.bin`:             "3",
		`C:\Rakion\rakion.bin`:                 "",
		`C:\Windows\notepad.exe`:               "",
		`xgta5.exe`:                            "",
	} {
		got := ""
		if g := list.match(exe); g != nil {
			got = g.ID
		}
		// Either game named gta5.exe will do for a bare gta5.exe; the folder
		// picks the first.
		if exe == `D:\gta5.exe` && got != "" {
			continue
		}
		if got != want {
			t.Errorf("%s: got game %q, want %q", exe, got, want)
		}
	}
}

func TestGameOpenedAndClosed(t *testing.T) {
	list := indexGames([]knownGame{{ID: "1", Name: "Counter-Strike 2", Paths: []string{"cs2.exe"}}})
	since := make(map[string]time.Time)
	start := time.Unix(1000, 0)
	if g := scanGames([]string{`C:\Windows\explorer.exe`}, since, start, list); len(g) != 0 {
		t.Fatalf("nothing running, got %d games", len(g))
	}
	games := scanGames([]string{`D:\Steam\cs2.exe`}, since, start, list)
	if len(games) != 1 || games[0].game.Name != "Counter-Strike 2" {
		t.Fatalf("opened: got %v", games)
	}
	// Still running later: still played since it opened.
	games = scanGames([]string{`D:\Steam\cs2.exe`}, since, start.Add(time.Minute), list)
	if len(games) != 1 || !games[0].since.Equal(start) {
		t.Fatalf("still running: got %v", games)
	}
	// Closed: no longer played, and opened again it starts anew.
	if games = scanGames(nil, since, start.Add(2*time.Minute), list); len(games) != 0 {
		t.Fatalf("closed: still %d games", len(games))
	}
	games = scanGames([]string{`D:\Steam\cs2.exe`}, since, start.Add(3*time.Minute), list)
	if len(games) != 1 || !games[0].since.Equal(start.Add(3*time.Minute)) {
		t.Fatalf("opened again: got %v", games)
	}
}

// TestDetectRunningGames asks Discord for its list and says which programs
// running are games. Set LINKCORD_GAMES_TEST=1 to run it.
func TestDetectRunningGames(t *testing.T) {
	if os.Getenv("LINKCORD_GAMES_TEST") == "" {
		t.Skip("set LINKCORD_GAMES_TEST=1 to ask Discord for its list of games")
	}
	games, err := fetchGames()
	if err != nil {
		t.Fatal(err)
	}
	list := indexGames(games)
	exes := runningExecutables()
	t.Logf("%d games known on Windows, %d programs running", len(games), len(exes))
	for _, exe := range exes {
		if g := list.match(exe); g != nil {
			t.Logf("playing %s (%s): %s", g.Name, g.ID, exe)
		}
	}
}
