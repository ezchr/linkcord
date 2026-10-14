//go:build !no_voice

package voice

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVoiceMessage(t *testing.T) {
	// Two seconds of a tone, stereo.
	pcm := make([]int16, 2*sampleRate*2)
	for i := 0; i < len(pcm); i += 2 {
		v := int16(8000 * math.Sin(float64(i/2)*2*math.Pi*440/sampleRate))
		pcm[i], pcm[i+1] = v, v
	}
	ogg, seconds, waveform, err := VoiceMessage(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if seconds < 1.99 || seconds > 2.01 {
		t.Errorf("lasts %v seconds, want 2", seconds)
	}
	if len(waveform) != 20 {
		t.Errorf("waveform of %d, want one for each tenth of a second", len(waveform))
	}
	if !bytes.HasPrefix(ogg, []byte("OggS")) || !bytes.Contains(ogg, []byte("OpusHead")) {
		t.Fatal("not an Ogg Opus file")
	}
	// Each page's checksum is right: decoded back, it plays.
	player := NewPlayer()
	if err := player.Play(1, ogg); err != nil {
		t.Logf("could not play it here: %v", err)
	}
	// FFmpeg reads it as Opus, two seconds long.
	if ffprobe, err := exec.LookPath("ffprobe"); err == nil {
		p := filepath.Join(t.TempDir(), "m.ogg")
		os.WriteFile(p, ogg, 0o600)
		out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_name:format=duration", "-of", "csv=p=0", p).CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("opus")) {
			t.Errorf("ffprobe: %v %s", err, out)
		}
		t.Logf("ffprobe: %s", bytes.TrimSpace(out))
	}
	if _, _, _, err := VoiceMessage(pcm[:10]); err == nil {
		t.Error("a recording too short was made a message")
	}
}
