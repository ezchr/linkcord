package gui

import (
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"github.com/ayn2op/arikawa/v3/discord"
)

// Who plays a soundboard sound gets a blue ring while it sounds, as who
// speaks gets a green one.

// colorSoundboard is the ring of who plays a sound of the soundboard.
var colorSoundboard = rgb(0x00a8fc)

// soundGuess is how long a sound is taken to last until it is known.
const soundGuess = 2 * time.Second

// noteSoundPlayed shows id playing a sound for d.
func (c *chatView) noteSoundPlayed(id discord.UserID, d time.Duration) {
	if c.sbPlaying == nil {
		c.sbPlaying = make(map[discord.UserID]time.Time)
	}
	c.sbPlaying[id] = time.Now().Add(d)
	c.u.win.Invalidate()
}

// soundPlaying reports whether a sound id played is still sounding.
func (c *chatView) soundPlaying(id discord.UserID) bool {
	until, ok := c.sbPlaying[id]
	return ok && time.Now().Before(until)
}

// voiceRingColor is the ring around id's picture: blue while a sound they
// played sounds, green while they speak, or none.
func (c *chatView) voiceRingColor(gtx layout.Context, id discord.UserID) (colorT, bool) {
	if c.soundPlaying(id) {
		// Redrawn when the sound ends, for the ring to go.
		gtx.Execute(op.InvalidateCmd{At: c.sbPlaying[id]})
		return colorSoundboard, true
	}
	if c.u.voiceSpeaking(id) {
		return colorGreen, true
	}
	return colorT{}, false
}

// voiceRing draws id's ring, if any, over the edge of their round picture
// of px pixels, with a gap of bg inside it.
func (c *chatView) voiceRing(gtx layout.Context, id discord.UserID, px int, bg colorT) {
	if col, ok := c.voiceRingColor(gtx, id); ok {
		coloredRing(gtx, px, bg, col)
	}
}

// pcmDuration is how long 48 kHz stereo samples last.
func pcmDuration(pcm []int16) time.Duration {
	return time.Duration(len(pcm)/2) * time.Second / 48000
}
