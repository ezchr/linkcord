package gui

import (
	"testing"

	"github.com/ezchr/linkcord/internal/golive"
)

// TestMainScreen checks that Go Live with nothing chosen shares the main
// screen, or the first screen without one marked main.
func TestMainScreen(t *testing.T) {
	win, scr, main := golive.Source{Kind: golive.Window}, golive.Source{Kind: golive.Screen}, golive.Source{Kind: golive.Screen, Primary: true}
	cases := []struct {
		sources []golive.Source
		want    int
	}{
		{[]golive.Source{win, scr, main}, 2},
		{[]golive.Source{win, scr, scr}, 1},
		{[]golive.Source{win}, 0},
	}
	for _, c := range cases {
		if got := mainScreen(c.sources); got != c.want {
			t.Errorf("mainScreen = %d, want %d", got, c.want)
		}
	}
}
