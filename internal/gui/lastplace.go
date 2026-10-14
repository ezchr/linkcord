package gui

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ayn2op/arikawa/v3/discord"
)

// lastPlace is where the user was: the server open and the channel open in
// each server, kept in lastplace.json beside the log, to start there again.
type lastPlace struct {
	Guild    discord.GuildID                       `json:"guild,omitempty"`
	Channels map[discord.GuildID]discord.ChannelID `json:"channels,omitempty"`
}

func lastPlacePath() string {
	return filepath.Join(filepath.Dir(logPath()), "lastplace.json")
}

// loadLastPlace takes back where the user was, into lastChannel, and gives
// the server to open.
func (c *chatView) loadLastPlace() discord.GuildID {
	if c.demo {
		return discord.NullGuildID
	}
	data, err := os.ReadFile(lastPlacePath())
	if err != nil {
		return discord.NullGuildID
	}
	var p lastPlace
	if json.Unmarshal(data, &p) != nil {
		return discord.NullGuildID
	}
	for g, ch := range p.Channels {
		if _, ok := c.lastChannel[g]; !ok {
			c.lastChannel[g] = ch
		}
	}
	return p.Guild
}

// saveLastPlace keeps where the user is, unless in the demo.
func (c *chatView) saveLastPlace() {
	if c.demo {
		return
	}
	p := lastPlace{Guild: c.selGuild, Channels: make(map[discord.GuildID]discord.ChannelID, len(c.lastChannel))}
	for g, ch := range c.lastChannel {
		p.Channels[g] = ch
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	go func() {
		c.placeMu.Lock()
		defer c.placeMu.Unlock()
		if err := os.WriteFile(lastPlacePath(), data, 0o600); err != nil {
			slog.Warn("could not keep the last channel", "err", err)
		}
	}()
}
