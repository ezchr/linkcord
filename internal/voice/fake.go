//go:build !no_voice

package voice

import (
	"context"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// fakeState is what the user pretends to the others: muted, or deafened,
// while what they say is still sent, and what is said still played.
type fakeState struct {
	mute, deafen bool
}

// SetFake shows the user muted, or deafened, to the others in the voice
// channel joined, or no longer, whatever they truly are: Discord tells the
// others what the app tells it, and the voice still goes as before.
func (s *Session) SetFake(mute, deafen bool) error {
	s.fake.Store(&fakeState{mute, deafen})
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	status := s.Status()
	channel, err := s.state.Cabinet.Channel(status.ChannelID)
	if err != nil || !status.Joined {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.state.SendGateway(ctx, s.voiceState(channel.GuildID, channel.ID, s.capture == nil, s.playback == nil))
}

// voiceState is what the others are told of the user: muted or deafened as
// they are, or as they pretend to be. Deafened shows muted too, as Discord.
func (s *Session) voiceState(guildID discord.GuildID, channelID discord.ChannelID, mute, deafen bool) *gateway.UpdateVoiceStateCommand {
	if f := s.fake.Load(); f != nil {
		deafen = deafen || f.deafen
		mute = mute || f.mute || deafen
	}
	return &gateway.UpdateVoiceStateCommand{GuildID: guildID, ChannelID: channelID, SelfMute: mute, SelfDeaf: deafen}
}
