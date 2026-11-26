//go:build windows && !no_voice

package golive

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/tphakala/go-opus/opus"
)

// streamClip keeps the stream watched as it came, its pictures still
// compressed and its sound as Opus, from a keyframe on, for clips of the
// last minutes to be made without decoding anything until then.
type streamClip struct {
	mu    sync.Mutex
	keep  time.Duration
	video []clipPicture
	audio []clipSound
}

type clipPicture struct {
	at  time.Time
	h   []byte
	key bool
}

type clipSound struct {
	at   time.Time
	opus []byte
}

// SetClipLength keeps the stream for clips of up to d, or nothing if d is
// zero.
func (w *Watch) SetClipLength(d time.Duration) {
	c := &w.clip
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keep = d
	if d <= 0 {
		c.video, c.audio = nil, nil
	}
}

func (c *streamClip) picture(h []byte, key bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keep <= 0 {
		return
	}
	now := time.Now()
	c.video = append(c.video, clipPicture{at: now, h: slices.Clone(h), key: key})
	// Kept from the last keyframe before what clips can reach, which the
	// rest is built on.
	cut := now.Add(-c.keep)
	from := 0
	for i, p := range c.video {
		if p.at.After(cut) {
			break
		}
		if p.key {
			from = i
		}
	}
	if from > 0 {
		c.video = slices.Delete(c.video, 0, from)
	}
	for len(c.audio) > 0 && c.audio[0].at.Before(c.video[0].at) {
		c.audio = c.audio[1:]
	}
}

func (c *streamClip) sound(packet []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keep <= 0 || len(c.video) == 0 {
		return
	}
	c.audio = append(c.audio, clipSound{at: time.Now(), opus: slices.Clone(packet)})
}

// SaveClip writes the last d of the stream to out, as MP4: its pictures as
// they came, and its sound, by ffmpeg.
func (w *Watch) SaveClip(ctx context.Context, ffmpeg string, d time.Duration, out string) error {
	c := &w.clip
	c.mu.Lock()
	start := time.Now().Add(-d)
	from := -1
	for i, p := range c.video {
		if p.key && (from < 0 || !p.at.After(start)) {
			from = i
		}
	}
	if from < 0 {
		c.mu.Unlock()
		return errors.New("nothing of the stream is kept yet")
	}
	video := slices.Clone(c.video[from:])
	var audio []clipSound
	for _, s := range c.audio {
		if !s.at.Before(video[0].at) {
			audio = append(audio, s)
		}
	}
	c.mu.Unlock()

	dir, err := os.MkdirTemp("", "linkcord-clip-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var h264 []byte
	for _, p := range video {
		h264 = append(h264, p.h...)
	}
	vpath := filepath.Join(dir, "video.h264")
	if err := os.WriteFile(vpath, h264, 0o600); err != nil {
		return err
	}
	// Raw pictures have no times: they play at the rate they came at.
	fps := 30.0
	if span := video[len(video)-1].at.Sub(video[0].at).Seconds(); len(video) > 1 && span > 0 {
		fps = float64(len(video)-1) / span
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-fflags", "+genpts",
		"-f", "h264", "-framerate", strconv.FormatFloat(fps, 'f', 3, 64), "-i", vpath}
	if len(audio) > 0 {
		apath := filepath.Join(dir, "audio.wav")
		pcm, err := clipPCM(audio, video[0].at, video[len(video)-1].at)
		if err != nil {
			return err
		}
		if err := os.WriteFile(apath, wav(pcm), 0o600); err != nil {
			return err
		}
		args = append(args, "-i", apath, "-c:a", "aac", "-b:a", "160k")
	}
	args = append(args, "-c:v", "copy", "-movflags", "+faststart", out)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, msg)
	}
	return nil
}

// clipPCM is the sound from start to end, 48 kHz stereo, each packet where
// it arrived.
func clipPCM(audio []clipSound, start, end time.Time) ([]int16, error) {
	decoder, err := opus.NewDecoder(48000, 2)
	if err != nil {
		return nil, err
	}
	const frame = 20 * time.Millisecond
	frames := int(end.Sub(start)/frame) + 1
	pcm := make([]int16, frames*1920)
	tmp := make([]int16, 6*1920)
	prev := -1
	for _, s := range audio {
		slot := int(s.at.Sub(start) / frame)
		if slot <= prev {
			slot = prev + 1
		}
		if slot >= frames {
			break
		}
		n, err := decoder.Decode(s.opus, tmp)
		if err != nil {
			continue
		}
		copy(pcm[slot*1920:], tmp[:n*2])
		prev = slot + max(n*2/1920, 1) - 1
	}
	return pcm, nil
}

// wav is pcm, 48 kHz stereo, as a WAV file.
func wav(pcm []int16) []byte {
	data := len(pcm) * 2
	b := make([]byte, 0, 44+data)
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(36+data))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint32(b, 48000)
	b = binary.LittleEndian.AppendUint32(b, 48000*4)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(data))
	for _, v := range pcm {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}
