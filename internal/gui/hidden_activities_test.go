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
