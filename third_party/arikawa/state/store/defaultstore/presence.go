package defaultstore

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type Presence struct {
	guilds guildMap[discord.UserID, discord.Presence]
}

var _ store.PresenceStore = (*Presence)(nil)

func NewPresence() *Presence {
	return &Presence{guilds: newGuildMap[discord.UserID, discord.Presence]()}
}

func (s *Presence) Reset() error {
	return s.guilds.reset()
}

func (s *Presence) Presence(guildID discord.GuildID, userID discord.UserID) (*discord.Presence, error) {
	return s.guilds.get(guildID, userID)
}

func (s *Presence) Presences(guildID discord.GuildID) ([]discord.Presence, error) {
	return s.guilds.all(guildID)
}

func (s *Presence) PresenceSet(guildID discord.GuildID, presence *discord.Presence, update bool) error {
	return s.guilds.set(guildID, presence.User.ID, presence, update)
}

func (s *Presence) PresenceRemove(guildID discord.GuildID, userID discord.UserID) error {
	return s.guilds.remove(guildID, userID)
}
