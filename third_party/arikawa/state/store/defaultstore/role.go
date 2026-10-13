package defaultstore

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type Role struct {
	guilds guildMap[discord.RoleID, discord.Role]
}

var _ store.RoleStore = (*Role)(nil)

func NewRole() *Role {
	return &Role{guilds: newGuildMap[discord.RoleID, discord.Role]()}
}

func (s *Role) Reset() error {
	return s.guilds.reset()
}

func (s *Role) Role(guildID discord.GuildID, roleID discord.RoleID) (*discord.Role, error) {
	return s.guilds.get(guildID, roleID)
}

func (s *Role) Roles(guildID discord.GuildID) ([]discord.Role, error) {
	return s.guilds.all(guildID)
}

func (s *Role) RoleSet(guildID discord.GuildID, role *discord.Role, update bool) error {
	return s.guilds.set(guildID, role.ID, role, update)
}

func (s *Role) RoleRemove(guildID discord.GuildID, roleID discord.RoleID) error {
	return s.guilds.remove(guildID, roleID)
}
