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

// StartLoopback captures what the speakers play, in stereo, 48 kHz, signed
// 16-bit PCM, giving it to data, which must not block. It is what Windows 7
// can capture of the sound of a screen shared. The returned function stops
// it, outside the callback.
func StartLoopback(data func(pcm []byte)) (func(), error) {
	device, err := startDeviceOn(malgo.Loopback, "", 2, func(_, input []byte) {
		data(input)
	})
	if err != nil {
		return nil, err
	}
	return func() { device.Uninit() }, nil
}
