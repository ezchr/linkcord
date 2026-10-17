package gui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/dcsound"
)

// Audio files sent in a chat play in it, as voice messages do, with bars of
// their sound, which Linkcord works out from the file, not too big, as
// Discord says them only for voice messages.

// maxAudioPreview is the largest audio file whose sound is read for its
// bars; bigger ones play without them.
const maxAudioPreview = 25 << 20

// audioPreview is what is known of an audio file sent: its bars, as
// Discord's waveform, and how long it lasts.
type audioPreview struct {
	loading bool
	wave    []byte
	dur     time.Duration
}

// isAudioAttachment reports whether a is an audio file, but a voice message.
func isAudioAttachment(a discord.Attachment) bool {
	return !isVoiceMessage(a) && (strings.HasPrefix(a.ContentType, "audio/") || isAudioFile(a.Filename))
}

// audioPreviewOf is what is known of a, read the first time it shows.
func (c *chatView) audioPreviewOf(a discord.Attachment) *audioPreview {
	url := string(a.URL)
	if c.audioPreviews == nil {
		c.audioPreviews = make(map[string]*audioPreview)
	}
	p, ok := c.audioPreviews[url]
	if ok {
		return p
	}
	p = new(audioPreview)
	c.audioPreviews[url] = p
	if c.demo {
		// The demo downloads nothing: made-up bars.
		p.dur = 2*time.Minute + 47*time.Second
		p.wave = make([]byte, 64)
		for i := range p.wave {
			p.wave[i] = byte(40 + (i*53+i*i*7)%200)
		}
		return p
	}
	if a.Size > maxAudioPreview {
		return p
	}
	p.loading = true
	name := a.Filename
	go func() {
		pcm, err := fetchAudio(url, name)
		c.u.post(func() {
			p.loading = false
			if err != nil || len(pcm) == 0 {
				return
			}
			p.dur = time.Duration(len(pcm)) * time.Second / samplesPerSecond
			// As many bars as Discord's waveforms have samples.
			levels := newAudioTrim(pcm).levels(64)
			p.wave = make([]byte, len(levels))
			for i, lv := range levels {
				p.wave[i] = byte(lv * 255)
			}
		})
	}()
	return p
}

// fetchAudio downloads an audio file and reads its sound.
func fetchAudio(url, name string) ([]int16, error) {
	client := http.Client{Timeout: time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioPreview+1))
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav":
		if pcm, err := decodeWAV(data); err == nil {
			return pcm, nil
		}
	case ".mp3", ".ogg", ".opus":
		if pcm, err := dcsound.DecodeSound(data); err == nil {
			return pcm, nil
		}
	}
	// The others through FFmpeg, from a file.
	tmp, err := os.CreateTemp("", "linkcord-audio-*"+filepath.Ext(name))
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	tmp.Close()
	if err != nil {
		return nil, err
	}
	return decodeWithFFmpeg(tmp.Name())
}

// layoutAudioAttachment is an audio file sent: its name and size over a
// player as voice messages have, with the bars of its sound.
func (c *chatView) layoutAudioAttachment(gtx layout.Context, a discord.Attachment) layout.Dimensions {
	th := c.u.th
	p := c.audioPreviewOf(a)
	// Played as a voice message, which it is not, with the bars worked out.
	shown := a
	shown.DurationSecs, shown.Waveform = p.dur.Seconds(), p.wave
	sub := formatSize(a.Size)
	if p.loading {
		sub += " · " + tr("reading its sound…")
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMusic, 16, colorAccent) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Flexed(1, oneLine(label(th, 14, colorLink, a.Filename)).Layout),
					layout.Rigid(label(th, 12, colorMuted, "  "+sub).Layout),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutVoiceMessage(gtx, shown) }),
		)
	})
}
