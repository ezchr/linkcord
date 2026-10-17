package gui

import (
	"strings"
	"testing"
	"time"
)

func TestHiddenActivities(t *testing.T) {
	cs2 := &knownGame{ID: "1", Name: "Counter-Strike 2"}
	gta := &knownGame{ID: "2", Name: "Grand Theft Auto V"}
	c := &chatView{u: &ui{}}
	c.games = []playing{{cs2, time.Now()}, {gta, time.Now()}}
	c.u.app.HiddenActivities = map[string]string{"2": "Grand Theft Auto V", "3": "Spotify"}

	sent := c.gameActivities()
	if len(sent) != 1 || !strings.Contains(string(sent[0]), "Counter-Strike 2") {
		t.Fatalf("hidden game sent: %s", sent)
	}
	var names []string
	for _, a := range c.knownActivities() {
		names = append(names, a.name)
	}
	// Running or hidden, all are listed to be turned on or off.
	if got := strings.Join(names, ", "); got != "Counter-Strike 2, Grand Theft Auto V, Spotify" {
		t.Fatalf("listed: %s", got)
	}
}

func TestCustomGame(t *testing.T) {
	custom := customList([]customGame{{Exe: "minecraft.exe", Name: "Minecraft"}})
	discordList := indexGames([]knownGame{{ID: "1", Name: "Other", Paths: []string{"minecraft.exe"}}})
	since := make(map[string]time.Time)
	games := scanGames([]string{`C:\Games\minecraft.exe`}, since, time.Now(), custom, discordList)
	if len(games) != 1 || games[0].game.Name != "Minecraft" {
		t.Fatalf("the program added was not found first: %+v", games)
	}
	c := &chatView{u: &ui{}}
	c.games = games
	sent := c.gameActivities()
	if len(sent) != 1 || strings.Contains(string(sent[0]), "application_id") || !strings.Contains(string(sent[0]), "Minecraft") {
		t.Fatalf("sent %s", sent)
	}
}
