//go:build !no_voice

package voice

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/tphakala/go-opus/opus"
)

func TestClip(t *testing.T) {
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: sampleRate, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	// A second of a tone, as it would have come in.
	tone := make([]int16, frameSamples)
	for i := range tone {
		tone[i] = int16(8000 * ((i/40)%2*2 - 1))
	}
	packet := make([]byte, 4000)
	n, err := encoder.Encode(tone, packet)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	s.SetClipLength(time.Minute)
	b := &s.clips
	start := time.Now().Add(-2 * time.Second)
	for i := range 50 {
		b.users[1] = append(b.users[1], clipPacket{at: start.Add(time.Duration(i) * frame), opus: packet[:n]})
	}

	clips, err := s.Clip(nil, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pcm := clips[1]
	if len(pcm) != 150*frameSamples {
		t.Fatalf("got %d samples, want 3 seconds' worth", len(pcm))
	}
	loud := 0
	for _, v := range pcm {
		if v > 1000 || v < -1000 {
			loud++
		}
	}
	if loud == 0 {
		t.Error("the clip is silent")
	}
	if got := Mix(map[discord.UserID][]int16{1: pcm, 2: pcm}); len(got) != len(pcm) {
		t.Errorf("mixed %d samples, want %d", len(got), len(pcm))
	}
	// Who did not talk has no clip.
	if clips, _ := s.Clip([]discord.UserID{2}, time.Second); len(clips) != 0 {
		t.Errorf("got clips of who did not talk: %d", len(clips))
	}
}

func TestLevelGuard(t *testing.T) {
	var g levelGuard
	if gain := g.apply(30000, 1); gain >= 0.5 {
		t.Errorf("a saturated voice is played at %v", gain)
	}
	for range 1000 {
		g.apply(3000, 1)
	}
	if g.gain != 1 {
		t.Errorf("the voice did not come back up: %v", g.gain)
	}
	var quiet levelGuard
	if gain := quiet.apply(10000, 1); gain != 1 {
		t.Errorf("a voice that is not saturated is lowered: %v", gain)
	}
}
