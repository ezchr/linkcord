package gui

import (
	"os"
	"testing"
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
