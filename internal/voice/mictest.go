//go:build !no_voice

package voice

import (
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

// SilentLevel is the level of silence, in dB, the quietest a level is.
const SilentLevel = -100

// maxEcho is how much of what the microphone captured a MicTest keeps to play back, beyond which the oldest is dropped rather than fall behind.
const maxEcho = 10 * frameBytes

// MicTest captures the microphone to tell how loud it is, and plays it back if asked, as Discord's microphone test does.
type MicTest struct {
	capture *malgo.Device

	// proc readies what is captured as it would be sent, volume is how many times as loud it is played back, as the bits of a float32, and level how loud what was captured last was, in dB, as the bits of a float64.
	proc   *processor
	volume atomic.Uint32
	level  atomic.Uint64
	// pending is what was captured short of a whole block for proc.
	pending []int16

	mu       sync.Mutex
	playback *malgo.Device
	echo     []byte
}

// StartMicTest starts capturing the microphone with the ID input, "" for the default one.
func StartMicTest(input string) (*MicTest, error) {
	t := &MicTest{proc: newProcessor()}
	t.SetOutputVolume(1)
	t.level.Store(math.Float64bits(SilentLevel))

	device, err := startDeviceOn(malgo.Capture, input, channels, t.captured)
	if err != nil {
		return nil, err
	}

	t.capture = device

	return t, nil
}

// SetInputVolume measures and plays back what is captured gain times as loud, as it would be sent.
func (t *MicTest) SetInputVolume(gain float32) {
	t.proc.gain.Store(math.Float32bits(gain))
}

// SetNoiseSuppression removes the noise of what is captured, as it would be sent, if on.
func (t *MicTest) SetNoiseSuppression(on bool) {
	t.proc.denoise.Store(on)
}

// SetAutoGain evens out how loud what is captured is, as it would be sent, if on.
func (t *MicTest) SetAutoGain(on bool) {
	t.proc.autoGain.Store(on)
}

// SetOutputVolume plays back volume times as loud.
func (t *MicTest) SetOutputVolume(volume float32) {
	t.volume.Store(math.Float32bits(volume))
}

// Level is how loud what the microphone captured last was, in dB, from SilentLevel to 0.
func (t *MicTest) Level() float64 {
	return math.Float64frombits(t.level.Load())
}

// Echo plays back what is captured on the speakers with the ID output, "" for the default ones, if on, and stops otherwise.
func (t *MicTest) Echo(output string, on bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.echo = t.echo[:0]

	return setDevice(&t.playback, on, malgo.Playback, output, t.play)
}

// Echoing reports whether what is captured is played back.
func (t *MicTest) Echoing() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.playback != nil
}

// Stop stops capturing and playing back.
func (t *MicTest) Stop() {
	t.capture.Uninit()

	t.mu.Lock()
	defer t.mu.Unlock()

	setDevice(&t.playback, false, malgo.Playback, "", nil)
	t.proc.close()
}

// captured measures what the microphone captured and keeps it to be played back. It is what the capture device calls.
func (t *MicTest) captured(_, input []byte) {
	for i := 0; i+1 < len(input); i += 2 {
		t.pending = append(t.pending, int16(binary.LittleEndian.Uint16(input[i:])))
	}
	n := len(t.pending) / blockSamples * blockSamples
	if n == 0 {
		return
	}
	samples := t.pending[:n]
	t.proc.process(samples)
	peak := 0
	pcm := make([]byte, 2*n)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(sample))
		peak = max(peak, int(sample), -int(sample))
	}
	t.pending = append(t.pending[:0], t.pending[n:]...)

	t.level.Store(math.Float64bits(levelOf(peak)))

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.playback == nil {
		return
	}

	t.echo = append(t.echo, pcm...)
	if len(t.echo) > maxEcho {
		t.echo = t.echo[len(t.echo)-cushion:]
	}
}

// play fills output with what was captured. It is what the playback device calls.
func (t *MicTest) play(output, _ []byte) {
	volume := math.Float32frombits(t.volume.Load())

	t.mu.Lock()
	defer t.mu.Unlock()

	n := min(len(output), len(t.echo))
	for i := 0; i+1 < n; i += 2 {
		sample := amplify(int16(binary.LittleEndian.Uint16(t.echo[i:])), volume)
		binary.LittleEndian.PutUint16(output[i:], uint16(sample))
	}

	t.echo = t.echo[n:]
}

// amplify returns sample gain times as loud, as loud as a sample can be at most.
func amplify(sample int16, gain float32) int16 {
	if gain == 1 {
		return sample
	}

	return int16(max(math.MinInt16, min(math.MaxInt16, float32(sample)*gain)))
}

// levelOf is how loud a sample as loud as peak is, in dB.
func levelOf(peak int) float64 {
	if peak == 0 {
		return SilentLevel
	}

	return max(SilentLevel, 20*math.Log10(float64(peak)/math.MaxInt16))
}
