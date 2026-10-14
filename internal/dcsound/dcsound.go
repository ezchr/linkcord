// Package dcsound gets Discord's own sounds, such as of muting or of someone
// joining the voice channel, from discord.com as Discord's web app does,
// keeps them on the disk, and decodes them to play.
package dcsound

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/hajimehoshi/go-mp3"
	"github.com/tphakala/go-opus/oggopus"
)

// The sounds that Linkcord plays, by their name in Discord's web app.
const (
	Mute             = "mute"
	Unmute           = "unmute"
	Deafen           = "deafen"
	Undeafen         = "undeafen"
	Disconnect       = "disconnect"
	UserJoin         = "user_join"
	UserLeave        = "user_leave"
	UserMoved        = "user_moved"
	PTTStart         = "ptt_start"
	PTTStop          = "ptt_stop"
	StreamStarted    = "stream_started"
	StreamEnded      = "stream_ended"
	StreamUserJoined = "stream_user_joined"
	StreamUserLeft   = "stream_user_left"
	Message          = "message1"
)

// assets are the files of the sounds on discord.com/assets, as Discord's web
// app named them in October 2026. A sound whose file is gone is looked for
// in the web app again.
var assets = map[string]string{
	Mute:             "2d3b4ba32c34c862.mp3",
	Unmute:           "e74c4a06134a20e4.mp3",
	Deafen:           "529ff198eac567af.mp3",
	Undeafen:         "b150f03c89944403.mp3",
	Disconnect:       "dd0911025fe00b2e.mp3",
	UserJoin:         "b135ff6c8e091b43.mp3",
	UserLeave:        "7b9a183742515fc2.mp3",
	UserMoved:        "4003230e4086b680.mp3",
	PTTStart:         "f5c8ec31889c5317.mp3",
	PTTStop:          "34b5969be3a063ba.mp3",
	StreamStarted:    "abe52a3c92953edb.mp3",
	StreamEnded:      "cbff07b5a83e0321.mp3",
	StreamUserJoined: "3d2af2764581bb2e.mp3",
	StreamUserLeft:   "24ae9dc0184bd570.mp3",
	Message:          "3ed22d14f3c30bc4.mp3",
}

const (
	origin = "https://discord.com"
	// rate is the sample rate sounds are decoded to, that of voice.
	rate = 48000
	// maxFile is more than a sound weighs, against a server sending
	// endlessly; maxScript is the same for the web app.
	maxFile   = 2 << 20
	maxScript = 64 << 20
)

// Library is Discord's sounds, kept in a folder.
type Library struct {
	dir    string
	client *http.Client

	mu  sync.Mutex
	pcm map[string][]int16
	// files are the files of the sounds, assets updated by what the web app
	// said, kept in sounds.json; looked reports whether the web app was
	// looked in already in this run.
	files  map[string]string
	looked bool
}

// New returns the sounds kept in dir.
func New(dir string) *Library {
	l := &Library{dir: dir, client: http.DefaultClient, pcm: make(map[string][]int16), files: make(map[string]string)}
	for name, file := range assets {
		l.files[name] = file
	}
	if data, err := os.ReadFile(filepath.Join(dir, "sounds.json")); err == nil {
		var found map[string]string
		if json.Unmarshal(data, &found) == nil {
			for name, file := range found {
				l.files[name] = file
			}
		}
	}
	return l
}

// Get returns the sound named, as 48 kHz stereo samples, downloading it the
// first time.
func (l *Library) Get(ctx context.Context, name string) ([]int16, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if pcm, ok := l.pcm[name]; ok {
		return pcm, nil
	}
	data, err := l.fileLocked(ctx, name)
	if errors.Is(err, errGone) && !l.looked {
		l.looked = true
		if err = l.lookLocked(ctx); err == nil {
			data, err = l.fileLocked(ctx, name)
		}
	}
	if err != nil {
		return nil, err
	}
	pcm, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("dcsound: %s: %w", name, err)
	}
	l.pcm[name] = pcm
	return pcm, nil
}

// Preload gets every sound that Linkcord plays, so that the first plays at
// once.
func (l *Library) Preload(ctx context.Context) error {
	var errs []error
	for name := range assets {
		if _, err := l.Get(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var errGone = errors.New("dcsound: the sound is no longer on discord.com")

// fileLocked returns the MP3 of the sound named, from the disk or else from
// discord.com.
func (l *Library) fileLocked(ctx context.Context, name string) ([]byte, error) {
	file, ok := l.files[name]
	if !ok {
		return nil, errGone
	}
	path := filepath.Join(l.dir, name+"-"+file)
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	data, err := l.fetch(ctx, origin+"/assets/"+file, maxFile)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(l.dir, 0o700); err == nil {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			slog.Warn("could not keep a sound", "sound", name, "err", err)
		}
	}
	return data, nil
}

func (l *Library) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errGone
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("dcsound: %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("dcsound: %s is too big", url)
	}
	return data, nil
}

var (
	webScript  = regexp.MustCompile(`src="(/assets/web\.[0-9a-f]+\.js)"`)
	soundNames = regexp.MustCompile(`"\./([a-zA-Z0-9_]+)\.mp3":"(\d+)"`)
	soundFiles = regexp.MustCompile(`(\d+)\(e,t,n\)\{"use strict";e\.exports=n\.p\+"([0-9a-f]+\.mp3)"\}`)
)

// lookLocked finds the files of the sounds in Discord's web app again, as
// when they changed, and keeps them in sounds.json.
func (l *Library) lookLocked(ctx context.Context) error {
	page, err := l.fetch(ctx, origin+"/app", maxFile)
	if err != nil {
		return err
	}
	m := webScript.FindSubmatch(page)
	if m == nil {
		return errors.New("dcsound: the web app's script is not on its page")
	}
	script, err := l.fetch(ctx, origin+string(m[1]), maxScript)
	if err != nil {
		return err
	}
	modules := make(map[string]string)
	for _, m := range soundFiles.FindAllSubmatch(script, -1) {
		modules[string(m[1])] = string(m[2])
	}
	found := make(map[string]string)
	for _, m := range soundNames.FindAllSubmatch(script, -1) {
		if file, ok := modules[string(m[2])]; ok {
			found[string(m[1])] = file
		}
	}
	if len(found) == 0 {
		return errors.New("dcsound: no sounds in the web app")
	}
	for name, file := range found {
		l.files[name] = file
	}
	if data, err := json.Marshal(found); err == nil {
		os.MkdirAll(l.dir, 0o700)
		os.WriteFile(filepath.Join(l.dir, "sounds.json"), data, 0o600)
	}
	slog.Info("found Discord's sounds in its web app", "sounds", len(found))
	return nil
}

// DecodeSound decodes a sound, MP3 or Ogg Opus as Discord's soundboard
// keeps them, to 48 kHz stereo samples.
func DecodeSound(data []byte) ([]int16, error) {
	if !bytes.HasPrefix(data, []byte("OggS")) {
		return decode(data)
	}
	d, err := oggopus.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(d)
	if err != nil && len(raw) == 0 {
		return nil, err
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	if d.Info().Channels == 1 {
		stereo := make([]int16, 2*len(pcm))
		for i, v := range pcm {
			stereo[2*i], stereo[2*i+1] = v, v
		}
		pcm = stereo
	}
	return pcm, nil
}

// decode decodes an MP3 to 48 kHz stereo samples.
func decode(data []byte) ([]int16, error) {
	d, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(d)
	if err != nil {
		return nil, err
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return resample(pcm, d.SampleRate()), nil
}

// resample makes stereo samples at from a second as many at rate.
func resample(pcm []int16, from int) []int16 {
	if from == rate || from <= 0 {
		return pcm
	}
	frames := len(pcm) / 2
	out := make([]int16, int(int64(frames)*rate/int64(from))*2)
	for i := range len(out) / 2 {
		pos := float64(i) * float64(from) / rate
		j := int(pos)
		f := pos - float64(j)
		for c := range 2 {
			a := float64(pcm[min(j, frames-1)*2+c])
			b := float64(pcm[min(j+1, frames-1)*2+c])
			out[i*2+c] = int16(a + (b-a)*f)
		}
	}
	return out
}
