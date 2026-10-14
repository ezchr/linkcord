//go:build !no_voice

package voice

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

// effectsIdle is how long the speakers stay open after the last sound, for
// the next to play without waiting for them.
const effectsIdle = 3 * time.Second

// Effects plays short sounds, such as of muting, on the speakers, several
// at once if they overlap. It is safe for concurrent use.
type Effects struct {
	mu     sync.Mutex
	device *malgo.Device
	output string
	// playing is what is left to play of each sound, already as loud as it
	// is played; last is when the last one ended.
	playing [][]int16
	last    time.Time
	closing bool
}

// NewEffects returns an Effects that plays nothing yet.
func NewEffects() *Effects { return new(Effects) }

// Play plays pcm, 48 kHz stereo samples, volume times as loud, on the
// speakers with the ID output, "" for the default ones.
func (e *Effects) Play(pcm []int16, output string, volume float32) {
	sound := make([]int16, len(pcm))
	for i, v := range pcm {
		sound[i] = amplify(v, volume)
	}

	// Other speakers chosen close those open first, out of the lock, which
	// the device's mix waits for while it closes.
	e.mu.Lock()
	var old *malgo.Device
	if e.device != nil && e.output != output {
		old, e.device = e.device, nil
	}
	e.mu.Unlock()
	if old != nil {
		old.Uninit()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.device == nil {
		device, err := startDeviceOn(malgo.Playback, output, channels, e.mix)
		if err != nil {
			slog.Warn("could not play a sound", "err", err)
			return
		}
		e.device, e.output = device, output
	}
	e.playing = append(e.playing, sound)
	if !e.closing {
		e.closing = true
		go e.closeIdle()
	}
}

// Stop stops the sounds playing.
func (e *Effects) Stop() {
	e.mu.Lock()
	e.playing = nil
	e.mu.Unlock()
}

// Playing reports whether a sound is playing.
func (e *Effects) Playing() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.playing) > 0
}

// mix fills output with what is left of the sounds. It is what the playback
// device calls.
func (e *Effects) mix(output, _ []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(output)
	n := len(output) / 2
	kept := e.playing[:0]
	for _, sound := range e.playing {
		take := min(n, len(sound))
		for i, v := range sound[:take] {
			sum := int32(int16(binary.LittleEndian.Uint16(output[2*i:]))) + int32(v)
			binary.LittleEndian.PutUint16(output[2*i:], uint16(max(math.MinInt16, min(math.MaxInt16, sum))))
		}
		if sound = sound[take:]; len(sound) > 0 {
			kept = append(kept, sound)
		}
	}
	e.playing = kept
	if len(kept) > 0 {
		e.last = time.Now()
	}
}

// closeIdle closes the speakers once no sound played for effectsIdle.
func (e *Effects) closeIdle() {
	for {
		time.Sleep(time.Second)
		e.mu.Lock()
		if len(e.playing) == 0 && time.Since(e.last) > effectsIdle {
			if e.device != nil {
				// Uninit waits for mix, which takes the lock.
				device := e.device
				e.device, e.closing = nil, false
				e.mu.Unlock()
				device.Uninit()
				return
			}
			e.closing = false
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
	}
}
