package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestLatestActivities(t *testing.T) {
	song := func(details string) discord.Activity {
		return discord.Activity{Type: discord.ListeningActivity, Name: "YouTube Music", AppID: 1, Details: details}
	}
	custom := discord.Activity{Type: discord.CustomActivity, Name: "Custom Status", State: "Sticker"}
	got := latestActivities([]discord.Activity{custom, song("Fragile Hands"), song("Motorcycle"), song("Motorcycle"), song("Motorcycle")})
	if len(got) != 2 {
		t.Fatalf("got %d activities, want the custom status and one song: %+v", len(got), got)
	}
	if got[0].Type != discord.CustomActivity || got[1].Details != "Motorcycle" {
		t.Errorf("got %+v, want the custom status then the newest song", got)
	}
}
