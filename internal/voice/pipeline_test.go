//go:build !no_voice

package voice

import (
	"encoding/binary"
	"slices"
	"testing"
)

// With nothing played for a while the canceller is not run and what is
// captured is sent as it is; once something is played it runs again.
func TestEchoSkippedWhileNothingPlayed(t *testing.T) {
	near := madeUpVoice(2, 4)
	e := newEcho()
	defer e.reset()
	silent := make([]byte, frameBytes)
	pcm := make([]int16, frameSamples)
	frame := func(at int) {
		for i := range 960 {
			pcm[2*i], pcm[2*i+1] = near[at+i]/2, near[at+i]/2
		}
	}
	at := 0
	for range 2 * 50 {
		frame(at)
		e.played(silent)
		e.capture(pcm)
		at += 960
	}
	if e.quiet.Load() <= echoQuiet {
		t.Fatalf("canceller still run after 2 s of silence played")
	}
	for range 20 {
		frame(at)
		want := slices.Clone(pcm)
		e.played(silent)
		if !e.capture(pcm) {
			t.Fatal("the user taken for echo with nothing played")
		}
		if !slices.Equal(pcm, want) {
			t.Fatal("what was captured changed with nothing played")
		}
		at += 960
	}

	loud := make([]byte, frameBytes)
	for i := range 960 {
		binary.LittleEndian.PutUint16(loud[4*i:], uint16(int16(8000*(i%2*2-1))))
		binary.LittleEndian.PutUint16(loud[4*i+2:], uint16(int16(8000*(i%2*2-1))))
	}
	e.played(loud)
	if e.quiet.Load() != 0 {
		t.Fatal("canceller not run again once something is played")
	}
}

// Deafened, packets are not decoded and nothing is left to play late.
func TestSpeakerSkip(t *testing.T) {
	s, err := newSpeaker(0)
	if err != nil {
		t.Fatal(err)
	}
	for seq := range uint16(5) {
		s.write(seq, nil)
	}
	s.skip(5)
	if len(s.buf) != 0 || s.seq != 5 {
		t.Fatalf("buffered %d bytes, seq %d after skip", len(s.buf), s.seq)
	}
	out := make([]byte, frameBytes)
	s.mix(out, 1, false)
	if slices.ContainsFunc(out, func(b byte) bool { return b != 0 }) {
		t.Fatal("played what was skipped")
	}
}
