//go:build !no_voice

package voice

import (
	"encoding/binary"
	"testing"

	"github.com/gen2brain/malgo"
)

// TestStartDeviceOn starts the last speakers of the system by their ID, as
// when the user chooses them, which hands malgo the ID in C memory.
func TestStartDeviceOn(t *testing.T) {
	_, outputs, err := Devices()
	if err != nil || len(outputs) == 0 {
		t.Skip("no speakers:", err)
	}

	id := outputs[len(outputs)-1].ID
	for range 2 {
		device, err := startDeviceOn(malgo.Playback, id, channels, func(output, _ []byte) { clear(output) })
		if err != nil {
			t.Fatal(err)
		}

		device.Uninit()
	}
}

func TestSpeakerVolume(t *testing.T) {
	for _, tt := range []struct {
		volume float32
		want   int16
	}{{1, 1000}, {0.5, 500}, {0, 0}, {2, 2000}} {
		s := &speaker{playing: true}
		for range frameSamples {
			s.buf = binary.LittleEndian.AppendUint16(s.buf, uint16(1000))
		}

		output := make([]byte, frameBytes)
		s.mix(output, tt.volume, false)
		if got := int16(binary.LittleEndian.Uint16(output)); got != tt.want {
			t.Errorf("volume %v: got %d, want %d", tt.volume, got, tt.want)
		}
	}
}
