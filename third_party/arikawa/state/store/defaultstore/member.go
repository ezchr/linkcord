package defaultstore

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type Member struct {
	guilds guildMap[discord.UserID, discord.Member]
}

var _ store.MemberStore = (*Member)(nil)

func NewMember() *Member {
	return &Member{guilds: newGuildMap[discord.UserID, discord.Member]()}
}

func (s *Member) Reset() error {
	return s.guilds.reset()
}

func (s *Member) Member(guildID discord.GuildID, userID discord.UserID) (*discord.Member, error) {
	return s.guilds.get(guildID, userID)
}

func (s *Member) Members(guildID discord.GuildID) ([]discord.Member, error) {
	return s.guilds.all(guildID)
}

func (s *Member) MemberSet(guildID discord.GuildID, member *discord.Member, update bool) error {
	return s.guilds.set(guildID, member.User.ID, member, update)
}

func (s *Member) MemberRemove(guildID discord.GuildID, userID discord.UserID) error {
	return s.guilds.remove(guildID, userID)
}
