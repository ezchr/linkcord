package gui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ezchr/linkcord/internal/dcsound"
)

// Audio files attached show their sound as bars, to hear them and keep only
// a part, as voice messages recorded are; a file trimmed is sent as a WAV
// of the part kept.

// audioExts are the audio files the trimmer opens: WAV by Linkcord itself,
// MP3 and Ogg Opus as the soundboard's sounds, the others with FFmpeg.
var audioExts = []string{".wav", ".mp3", ".ogg", ".opus", ".flac", ".m4a", ".aac", ".wma"}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range audioExts {
		if ext == e {
			return true
		}
	}
	return false
}

// decodeAudioFile is the sound of path as 48 kHz stereo samples.
func decodeAudioFile(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		if pcm, err := decodeWAV(data); err == nil {
			return pcm, nil
		}
	case ".mp3", ".ogg", ".opus":
		if pcm, err := dcsound.DecodeSound(data); err == nil {
			return pcm, nil
		}
	}
	return decodeWithFFmpeg(path)
}

// decodeWithFFmpeg decodes path with FFmpeg, if Linkcord has it.
func decodeWithFFmpeg(path string) ([]int16, error) {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		return nil, errors.New("this kind of audio needs FFmpeg, which Linkcord downloads the first time a stream is watched")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-f", "s16le", "-ac", "2", "-ar", "48000", "pipe:1")
	hideMediaProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	pcm := make([]int16, len(out)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(out[2*i:]))
	}
	return pcm, nil
}

// decodeWAV reads a WAV file of PCM or float samples, of any rate and
// channels, as 48 kHz stereo samples.
func decodeWAV(data []byte) ([]int16, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var format, channels, bits int
	var rate int
	var samples []byte
	for p := 12; p+8 <= len(data); {
		id, size := string(data[p:p+4]), int(binary.LittleEndian.Uint32(data[p+4:]))
		body := data[p+8 : min(p+8+size, len(data))]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.New("bad WAV format")
			}
			format = int(binary.LittleEndian.Uint16(body))
			channels = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
			// WAVE_FORMAT_EXTENSIBLE says the real format further on.
			if format == 0xfffe && len(body) >= 26 {
				format = int(binary.LittleEndian.Uint16(body[24:]))
			}
		case "data":
			samples = body
		}
		p += 8 + size + size%2
	}
	if channels < 1 || rate < 1000 || samples == nil || (format != 1 && format != 3) {
		return nil, errors.New("unsupported WAV")
	}
	width := bits / 8
	if width < 1 || width > 4 || (format == 3 && width != 4) {
		return nil, fmt.Errorf("unsupported WAV of %d bits", bits)
	}
	frames := len(samples) / (width * channels)
	sample := func(frame, ch int) float64 {
		o := (frame*channels + ch) * width
		b := samples[o : o+width]
		switch {
		case format == 3:
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		case width == 1:
			return (float64(b[0]) - 128) / 128
		case width == 2:
			return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
		case width == 3:
			return float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
		default:
			return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
		}
	}
	// To 48 kHz, each output frame from the nearest two of the input.
	out := make([]int16, 0, int(int64(frames)*48000/int64(rate))*2+2)
	for i := 0; ; i++ {
		pos := float64(i) * float64(rate) / 48000
		f := int(pos)
		if f >= frames {
			break
		}
		frac := pos - float64(f)
		next := min(f+1, frames-1)
		for ch := range 2 {
			src := min(ch, channels-1)
			v := sample(f, src)*(1-frac) + sample(next, src)*frac
			out = append(out, int16(max(-1, min(1, v))*32767))
		}
	}
	return out, nil
}

// loadAudio decodes an audio file attached, for its trimmer.
func (c *chatView) loadAudio(f *pendingFile) {
	if f.audio != nil || f.audioLoading || f.audioErr != "" {
		return
	}
	f.audioLoading = true
	path := f.path
	go func() {
		pcm, err := decodeAudioFile(path)
		c.u.post(func() {
			f.audioLoading = false
			if err != nil {
				f.audioErr = tr("Could not read the sound: ") + tr(err.Error())
				return
			}
			f.audio = newAudioTrim(pcm)
		})
	}()
}

// toggleAudioPreview plays the part kept of an audio file attached, or
// stops it.
func (c *chatView) toggleAudioPreview(f *pendingFile) {
	if f.audio == nil {
		return
	}
	playing := !f.audio.playing.IsZero()
	c.stopAudioPreviews()
	if playing {
		return
	}
	u := c.u
	u.soundLibrary()
	settings := u.voiceSettings()
	u.voice.effects.Play(f.audio.kept(), settings.Output, settings.outputVolume())
	f.audio.playing = time.Now()
}

// stopAudioPreviews stops the audio files attached that play.
func (c *chatView) stopAudioPreviews() {
	for _, f := range c.comp.files {
		if f.audio != nil && !f.audio.playing.IsZero() {
			if e := c.u.voice.effects; e != nil {
				e.Stop()
			}
			f.audio.playing = time.Time{}
		}
	}
}

// trimmedFiles are files with those audio files trimmed made WAV files of
// the part kept.
func (c *chatView) trimmedFiles(files []*pendingFile) ([]*pendingFile, error) {
	out := make([]*pendingFile, 0, len(files))
	for _, f := range files {
		if f.audio == nil || !f.audio.trimmed() {
			out = append(out, f)
			continue
		}
		cleanPastes()
		if err := os.MkdirAll(pasteDir, 0o755); err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp(pasteDir, "a")
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(f.name, filepath.Ext(f.name)) + ".wav"
		data := wavFile(f.audio.kept())
		if limit := c.uploadLimit(); int64(len(data)) > limit {
			return nil, fmt.Errorf(tr("the part kept of %s is %s as a WAV; you can send files up to %s here"), f.name, formatSize(uint64(len(data))), formatSize(uint64(limit)))
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		out = append(out, &pendingFile{path: path, name: name, size: int64(len(data))})
	}
	return out, nil
}

// layoutAudioChip is an audio file attached: its name, a button to hear
// it, and its sound to trim, with its button to remove it.
func (c *chatView) layoutAudioChip(gtx layout.Context, f *pendingFile) layout.Dimensions {
	th := c.u.th
	c.loadAudio(f)
	if f.audioPlay.Clicked(gtx) {
		c.toggleAudioPreview(f)
	}
	if f.audio != nil && !f.audio.playing.IsZero() && (c.u.voice.effects == nil || !c.u.voice.effects.Playing()) {
		f.audio.playing = time.Time{}
	}
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			playIc := iconPlay
			if f.audio != nil && !f.audio.playing.IsZero() {
				playIc = iconStop
			}
			head := func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if f.audio == nil {
							return icon(gtx, iconFile, 24, colorAccent)
						}
						return iconButton(gtx, &f.audioPlay, playIc, colorHeader, colorAccent)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						sub := formatSize(uint64(f.size))
						if f.audio != nil && f.audio.trimmed() {
							sub = tr("Trimmed: only the part kept is sent, as a WAV")
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(oneLine(label(th, 13, colorHeader, f.name)).Layout),
							layout.Rigid(oneLine(label(th, 11, colorMuted, sub)).Layout),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &f.remove, iconClose, colorMuted, colorRed)
					}),
				)
			}
			body := func(gtx layout.Context) layout.Dimensions {
				switch {
				case f.audioErr != "":
					return label(th, 12, colorRed, f.audioErr).Layout(gtx)
				case f.audio == nil:
					return label(th, 12, colorMuted, "Reading the sound…").Layout(gtx)
				}
				return f.audio.layout(gtx, th, 30)
			}
			return stacked(head, layout.Spacer{Height: unit.Dp(6)}.Layout, body)(gtx)
		})
	})
}
