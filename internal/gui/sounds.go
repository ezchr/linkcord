package gui

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ezchr/linkcord/internal/dcsound"
	"github.com/ezchr/linkcord/internal/voice"
)

// soundLibrary returns Discord's sounds, made on first use, when they start
// downloading so that the first plays at once.
func (u *ui) soundLibrary() *dcsound.Library {
	v := &u.voice
	if v.sounds == nil {
		v.effects = voice.NewEffects()
		v.sounds = dcsound.New(filepath.Join(filepath.Dir(logPath()), "sounds"))
		sounds := v.sounds
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := sounds.Preload(ctx); err != nil {
				slog.Warn("could not get Discord's sounds", "err", err)
			}
		}()
	}
	return v.sounds
}

// playSound plays Discord's sound named, unless the user turned sounds off,
// and reports whether it does.
func (u *ui) playSound(name string) bool {
	settings := u.voiceSettings()
	if settings.NoSounds {
		return false
	}
	sounds := u.soundLibrary()
	effects, output, volume := u.voice.effects, settings.Output, settings.outputVolume()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pcm, err := sounds.Get(ctx, name)
		if err != nil {
			slog.Warn("could not get a sound", "sound", name, "err", err)
			return
		}
		effects.Play(pcm, output, volume)
	}()
	return true
}

// trackCallers notes who is in the voice channel the user joined, for
// voiceSounds to tell who joins and leaves it.
func (c *chatView) trackCallers() {
	status := c.u.voiceStatus()
	c.callersOf, c.callers = status.ChannelID, make(map[discord.UserID]bool)
	ch, err := c.st.Cabinet.Channel(status.ChannelID)
	if err != nil {
		return
	}
	states, _ := c.st.Cabinet.VoiceStates(ch.GuildID)
	for _, vs := range states {
		if vs.ChannelID == status.ChannelID {
			c.callers[vs.UserID] = true
		}
	}
}

// voiceSounds plays the sound of someone joining or leaving the voice
// channel the user is in, as Discord does.
func (c *chatView) voiceSounds(ev *gateway.VoiceStateUpdateEvent) {
	status := c.u.voiceStatus()
	if !status.Joined {
		c.callersOf, c.callers = 0, nil
		return
	}
	if c.callersOf != status.ChannelID {
		c.trackCallers()
		return
	}
	if ev.UserID == c.me.ID {
		return
	}
	in := ev.ChannelID == status.ChannelID
	if in == c.callers[ev.UserID] {
		return
	}
	if in {
		c.callers[ev.UserID] = true
		c.u.playSound(dcsound.UserJoin)
	} else {
		delete(c.callers, ev.UserID)
		c.u.playSound(dcsound.UserLeave)
	}
}

// viewerSounds plays the sound of someone starting or stopping to watch the
// user's stream.
func (u *ui) viewerSounds() {
	v := &u.voice
	n := 0
	if v.stream != nil {
		n = len(v.stream.Viewers())
	}
	switch {
	case n > v.viewersHeard:
		u.playSound(dcsound.StreamUserJoined)
	case n < v.viewersHeard && v.stream != nil:
		u.playSound(dcsound.StreamUserLeft)
	}
	v.viewersHeard = n
}
