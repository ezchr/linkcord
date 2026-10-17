package gui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/textproto"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconSendArrow = mustIcon(icons.ContentSend)

// voiceRecording is a voice message being recorded in the box to write,
// as Discord's phones send them, which Linkcord sends from the computer.
type voiceRecording struct {
	rec               *voice.Recorder
	mic, cancel, send widget.Clickable
	sending           bool
	// review is what was recorded, to trim, hear and send; stop ends the
	// recording for it, and play hears the part kept.
	review     *audioTrim
	stop, play widget.Clickable
}

// active reports whether a voice message takes the place of the text: it
// records, waits to be sent, or is being sent.
func (r *voiceRecording) active() bool { return r.rec != nil || r.review != nil || r.sending }

func (c *chatView) startVoiceRecording() {
	r := &c.comp.voice
	if r.rec != nil || r.sending {
		return
	}
	s := c.u.voiceSettings()
	rec, err := voice.StartRecorder(voice.RecordOptions{
		Input: s.Input, Gain: s.inputVolume(), Strength: s.noiseStrength(),
		Denoise: !s.NoNoiseSuppression, AGC: !s.NoAutoGain,
	})
	if err != nil {
		c.u.voiceFailed("Could not record", err)
		return
	}
	r.rec = rec
}

func (c *chatView) cancelVoiceRecording() {
	r := &c.comp.voice
	if r.rec != nil {
		r.rec.Stop()
		r.rec = nil
	}
	c.stopVoicePreview()
	r.review = nil
}

// reviewVoiceRecording ends the recording, to trim it and hear it before
// sending it.
func (c *chatView) reviewVoiceRecording() {
	r := &c.comp.voice
	if r.rec == nil {
		return
	}
	pcm := r.rec.Stop()
	r.rec = nil
	if len(pcm) < samplesPerSecond/5 {
		// Nothing worth sending was said.
		return
	}
	r.review = newAudioTrim(pcm)
}

// toggleVoicePreview plays the part kept of the voice message, or stops.
func (c *chatView) toggleVoicePreview() {
	r := &c.comp.voice
	if r.review == nil {
		return
	}
	if !r.review.playing.IsZero() {
		c.stopVoicePreview()
		return
	}
	u := c.u
	u.soundLibrary()
	settings := u.voiceSettings()
	u.voice.effects.Play(r.review.kept(), settings.Output, settings.outputVolume())
	r.review.playing = time.Now()
}

func (c *chatView) stopVoicePreview() {
	r := &c.comp.voice
	if r.review != nil && !r.review.playing.IsZero() {
		if e := c.u.voice.effects; e != nil {
			e.Stop()
		}
		r.review.playing = time.Time{}
	}
}

// sendVoiceRecording sends what was recorded as a voice message, with its
// waveform, as a reply if one is being written.
func (c *chatView) sendVoiceRecording() {
	r := &c.comp.voice
	if r.review == nil || c.selChannel == nil {
		return
	}
	c.stopVoicePreview()
	pcm := r.review.kept()
	r.review, r.sending = nil, true
	ch := c.selChannel.ID
	reply := c.act.replyTo
	c.act.replyTo = nil
	go func() {
		err := c.postVoiceMessage(ch, pcm, reply)
		c.u.post(func() {
			r.sending = false
			if err != nil {
				c.u.voiceFailed("Could not send the voice message", err)
			}
		})
	}()
}

// postVoiceMessage sends pcm, 48 kHz stereo, to the channel as Discord's
// voice messages are: an Ogg Opus file, with flag 8192 and its length and
// waveform.
func (c *chatView) postVoiceMessage(ch discord.ChannelID, pcm []int16, reply *discord.Message) error {
	ogg, seconds, waveform, err := voice.VoiceMessage(pcm)
	if err != nil {
		return err
	}
	if c.demo {
		return nil
	}
	payload := map[string]any{
		"flags": 1 << 13,
		"attachments": []map[string]any{{
			"id": "0", "filename": "voice-message.ogg",
			"duration_secs": seconds, "waveform": base64.StdEncoding.EncodeToString(waveform),
		}},
	}
	if reply != nil {
		payload["message_reference"] = map[string]any{"message_id": reply.ID.String(), "channel_id": ch.String()}
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	js, _ := json.Marshal(payload)
	if err := w.WriteField("payload_json", string(js)); err != nil {
		return err
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="files[0]"; filename="voice-message.ogg"`)
	h.Set("Content-Type", "audio/ogg")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(ogg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.st.Client.FastRequest("POST", api.EndpointChannels+ch.String()+"/messages",
		httputil.WithContentType(w.FormDataContentType()), httputil.WithBody(io.NopCloser(&body)))
}

// updateVoiceRecording handles the buttons of the recording.
func (c *chatView) updateVoiceRecording(gtx layout.Context) {
	r := &c.comp.voice
	if r.mic.Clicked(gtx) {
		c.startVoiceRecording()
	}
	if r.cancel.Clicked(gtx) {
		c.cancelVoiceRecording()
	}
	if r.stop.Clicked(gtx) {
		c.reviewVoiceRecording()
	}
	if r.play.Clicked(gtx) {
		c.toggleVoicePreview()
	}
	if r.send.Clicked(gtx) {
		c.sendVoiceRecording()
	}
	if r.rec != nil || r.sending {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
	}
}

// layoutVoiceRecording is the box to write while a voice message records:
// how long and how loud, and buttons to throw it away or send it.
func (c *chatView) layoutVoiceRecording(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	r := &c.comp.voice
	if r.sending {
		return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12)}.Layout(gtx, label(th, 15, colorMuted, "Sending the voice message…").Layout)
	}
	if r.review != nil {
		return c.layoutVoiceReview(gtx)
	}
	d := r.rec.Elapsed()
	level := r.rec.Level()
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &r.cancel, iconClose, colorMuted, colorRed)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dot(gtx, colorRed, 10) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(label(th, 15, colorHeader, fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
			// How loud the microphone is now.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				w, h := gtx.Constraints.Max.X, gtx.Dp(6)
				paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rect(0, 0, w, h), h/2).Op(gtx.Ops))
				lw := int(float32(w) * min(1, level*1.5))
				paint.FillShape(gtx.Ops, colorGreen, clip.UniformRRect(image.Rect(0, 0, lw, h), h/2).Op(gtx.Ops))
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// Ends the recording, to trim and hear it before sending.
				return iconButton(gtx, &r.stop, iconStop, colorAccent, colorHeader)
			}),
		)
	})
}

// layoutVoiceReview is a voice message recorded: its sound to trim between
// two handles, to hear, and to send or throw away.
func (c *chatView) layoutVoiceReview(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	r := &c.comp.voice
	if !r.review.playing.IsZero() && (c.u.voice.effects == nil || !c.u.voice.effects.Playing()) {
		r.review.playing = time.Time{}
	}
	playIc := iconPlay
	if !r.review.playing.IsZero() {
		playIc = iconStop
	}
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &r.cancel, iconClose, colorMuted, colorRed)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &r.play, playIc, colorHeader, colorAccent)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return r.review.layout(gtx, th, 32)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &r.send, iconSendArrow, colorAccent, colorHeader)
			}),
		)
	})
}
