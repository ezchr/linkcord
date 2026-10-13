//go:build !no_voice

package voice

import "github.com/gen2brain/malgo"

// StartMediaAudio opens a stereo, 48 kHz, signed 16-bit PCM output.
// The callback must not block. The returned function releases the device
// and must be called outside the audio callback.
func StartMediaAudio(fill func([]byte)) (func(), error) {
	device, err := startDevice(malgo.Playback, 2, func(output, _ []byte) {
		clear(output)
		fill(output)
	})
	if err != nil {
		return nil, err
	}
	return func() { device.Uninit() }, nil
}
