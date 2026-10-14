package gui

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ezchr/linkcord/internal/dcsound"
)

// voiceEffectEvent is sent when someone in a voice channel plays a sound
// of the soundboard, or sends an emoji: each one's app plays the sound.
type voiceEffectEvent struct {
	ChannelID   discord.ChannelID `json:"channel_id"`
	GuildID     discord.GuildID   `json:"guild_id"`
	UserID      discord.UserID    `json:"user_id"`
	SoundID     json.RawMessage   `json:"sound_id"`
	SoundVolume *float64          `json:"sound_volume"`
}

func (*voiceEffectEvent) Op() ws.OpCode           { return 0 }
func (*voiceEffectEvent) EventType() ws.EventType { return "VOICE_CHANNEL_EFFECT_SEND" }

func init() {
	gateway.OpUnmarshalers.Add(func() ws.Event { return new(voiceEffectEvent) })
}

// soundboardCache keeps the sounds of the soundboard heard, decoded.
var soundboardCache sync.Map // sound ID → []int16

// watchSoundboard plays the sounds others play in the voice channel joined,
// as Discord's app does, unless the user muted their soundboard.
func (c *chatView) watchSoundboard() func() {
	return c.st.AddHandler(func(ev *voiceEffectEvent) {
		id := strings.Trim(string(ev.SoundID), `"`)
		if id == "" || id == "null" {
			return
		}
		volume := 1.0
		if ev.SoundVolume != nil {
			volume = *ev.SoundVolume
		}
		c.u.post(func() {
			u := c.u
			if ev.ChannelID != u.voiceChannel() || u.voice.deafened || slices.Contains(u.app.SoundboardMuted, ev.UserID) {
				return
			}
			go c.playSoundboardSound(id, float32(volume))
		})
	})
}

// playSoundboardSound plays the sound id at volume, downloading it once.
func (c *chatView) playSoundboardSound(id string, volume float32) {
	u := c.u
	var pcm []int16
	if v, ok := soundboardCache.Load(id); ok {
		pcm = v.([]int16)
	} else {
		data, err := u.images.fetch("https://cdn.discordapp.com/soundboard-sounds/" + id)
		if err != nil {
			slog.Warn("could not get a soundboard sound", "id", id, "err", err)
			return
		}
		if pcm, err = dcsound.DecodeSound(data); err != nil {
			slog.Warn("could not decode a soundboard sound", "id", id, "err", err)
			return
		}
		soundboardCache.Store(id, pcm)
	}
	u.post(func() {
		u.soundLibrary()
		settings := u.voiceSettings()
		u.voice.effects.Play(pcm, settings.Output, volume*settings.outputVolume())
	})
}
