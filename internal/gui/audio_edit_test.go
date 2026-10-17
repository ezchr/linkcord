package gui

import (
	"encoding/binary"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// testWAV is a WAV of a tone, seconds long, at rate, of channels and bits.
func testWAV(rate, channels, bits int, seconds float64) []byte {
	frames := int(float64(rate) * seconds)
	var data []byte
	for i := range frames {
		v := 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(rate))
		for range channels {
			switch bits {
			case 16:
				data = binary.LittleEndian.AppendUint16(data, uint16(int16(v*32767)))
			case 24:
				s := int32(v * 8388607)
				data = append(data, byte(s), byte(s>>8), byte(s>>16))
			}
		}
	}
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(36+len(data)))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, uint16(channels))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate*channels*bits/8))
	b = binary.LittleEndian.AppendUint16(b, uint16(channels*bits/8))
	b = binary.LittleEndian.AppendUint16(b, uint16(bits))
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

func TestDecodeWAV(t *testing.T) {
	for _, tc := range []struct{ rate, channels, bits int }{
		{48000, 2, 16}, {44100, 1, 16}, {22050, 2, 24}, {16000, 1, 24},
	} {
		pcm, err := decodeWAV(testWAV(tc.rate, tc.channels, tc.bits, 2))
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		// Two seconds of 48 kHz stereo, give or take a frame.
		if n := len(pcm); n < 2*96000-4 || n > 2*96000+4 {
			t.Errorf("%+v: %d samples", tc, n)
		}
		peak := 0
		for _, v := range pcm {
			peak = max(peak, int(v), -int(v))
		}
		if peak < 15000 || peak > 17000 {
			t.Errorf("%+v: peak %d, want about 16383", tc, peak)
		}
	}
	if _, err := decodeWAV([]byte("not a wav")); err == nil {
		t.Fatal("garbage taken as WAV")
	}
}

func TestTrimmedAudioFileSent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	os.WriteFile(path, testWAV(44100, 2, 16, 4), 0o644)
	pcm, err := decodeAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c := &chatView{u: &ui{}}
	untouched := &pendingFile{path: path, name: "song.wav", audio: newAudioTrim(pcm)}
	trimmed := &pendingFile{path: path, name: "song.wav", audio: newAudioTrim(pcm)}
	trimmed.audio.start, trimmed.audio.end = 0.25, 0.75
	out, err := c.trimmedFiles([]*pendingFile{untouched, trimmed})
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != untouched {
		t.Fatal("an audio file not trimmed was changed")
	}
	data, err := os.ReadFile(out[1].path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeWAV(data)
	if err != nil {
		t.Fatal(err)
	}
	// Half of four seconds, as 48 kHz stereo.
	if d := time.Duration(len(got)) * time.Second / samplesPerSecond; d < 1990*time.Millisecond || d > 2010*time.Millisecond {
		t.Fatalf("the part kept lasts %v", d)
	}
}

// TestAudioChipRender draws an audio file attached, trimmed, to
// LINKCORD_TRIM_RENDER/audio-chip.png.
func TestAudioChipRender(t *testing.T) {
	out := os.Getenv("LINKCORD_TRIM_RENDER")
	if out == "" {
		t.Skip("set LINKCORD_TRIM_RENDER to a folder to draw it")
	}
	const w, h = 460, 120
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	c := &chatView{u: &ui{th: newTheme(), win: new(app.Window)}}
	f := &pendingFile{name: "grabacion de la clase.wav", size: 2 << 20, audio: newAudioTrim(testSound())}
	f.audio.start, f.audio.end = 0.22, 0.8
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now(), Constraints: layout.Exact(image.Pt(w, h))}
	paint.FillShape(&ops, colorInput, clip.Rect{Max: image.Pt(w, h)}.Op())
	layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.layoutAudioChip(gtx, f)
	})
	if err := win.Frame(&ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(out, "audio-chip.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	png.Encode(file, img)
}
