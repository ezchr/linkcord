package gui

import (
	"bytes"
	"testing"
)

func TestVideoAudioPauseMuteAndUnderrun(t *testing.T) {
	a := &videoAudio{queue: make(chan []byte, 2)}
	a.queue <- []byte{1, 2, 3, 4, 5, 6, 7, 8}
	out := bytes.Repeat([]byte{255}, 4)
	a.paused.Store(true)
	a.fill(out)
	if !bytes.Equal(out, make([]byte, 4)) || a.played.Load() != 0 {
		t.Fatal("pause consumed audio or left stale output")
	}
	a.paused.Store(false)
	a.fill(out)
	if !bytes.Equal(out, []byte{1, 2, 3, 4}) {
		t.Fatal("playback did not resume at the same sample")
	}
	a.muted.Store(true)
	a.fill(out)
	if !bytes.Equal(out, make([]byte, 4)) || a.played.Load() != 8 {
		t.Fatal("mute did not preserve playback progress")
	}
	a.fill(out)
	if a.played.Load() != 8 {
		t.Fatal("an underrun advanced the media clock")
	}
	close(a.queue)
	a.fill(out)
	if !a.eof.Load() {
		t.Fatal("audio end was not detected")
	}
}
