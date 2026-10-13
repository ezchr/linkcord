package defaultstore

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type VoiceState struct {
	guilds guildMap[discord.UserID, discord.VoiceState]
}

var _ store.VoiceStateStore = (*VoiceState)(nil)

func NewVoiceState() *VoiceState {
	return &VoiceState{guilds: newGuildMap[discord.UserID, discord.VoiceState]()}
}

func (s *VoiceState) Reset() error {
	return s.guilds.reset()
}

func (s *VoiceState) VoiceState(guildID discord.GuildID, userID discord.UserID) (*discord.VoiceState, error) {
	return s.guilds.get(guildID, userID)
}

func (s *VoiceState) VoiceStates(guildID discord.GuildID) ([]discord.VoiceState, error) {
	return s.guilds.all(guildID)
}

func (s *VoiceState) VoiceStateSet(guildID discord.GuildID, voiceState *discord.VoiceState, update bool) error {
	return s.guilds.set(guildID, voiceState.UserID, voiceState, update)
}

func (s *VoiceState) VoiceStateRemove(guildID discord.GuildID, userID discord.UserID) error {
	return s.guilds.remove(guildID, userID)
}
