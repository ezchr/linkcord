package gui

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"

	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type apngTestFrame struct {
	rect           image.Rectangle
	fill           color.NRGBA
	dispose, blend byte
}

// Build real compressed PNG subframes, including a separate default poster.
func makeTestAPNG(t *testing.T, poster bool, frames []apngTestFrame) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString(pngSignature)
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[:4], 4)
	binary.BigEndian.PutUint32(header[4:8], 4)
	header[8], header[9] = 8, 6
	appendPNGChunk(&buf, "IHDR", header)
	control := make([]byte, 8)
	binary.BigEndian.PutUint32(control[:4], uint32(len(frames)))
	appendPNGChunk(&buf, "acTL", control)
	imageData := func(w, h int, fill color.NRGBA) [][]byte {
		// All frames use the canvas header's RGBA format. png.Encode would
		// optimize opaque subframes to RGB, incompatible with that header.
		var encoded bytes.Buffer
		z := zlib.NewWriter(&encoded)
		row := make([]byte, 1+w*4)
		for x := range w {
			copy(row[1+x*4:], []byte{fill.R, fill.G, fill.B, fill.A})
		}
		for range h {
			if _, err := z.Write(row); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return [][]byte{encoded.Bytes()}
	}
	if poster {
		for _, part := range imageData(4, 4, color.NRGBA{G: 255, A: 255}) {
			appendPNGChunk(&buf, "IDAT", part)
		}
	}
	var seq uint32
	for i, f := range frames {
		fc := make([]byte, 26)
		binary.BigEndian.PutUint32(fc[:4], seq)
		seq++
		binary.BigEndian.PutUint32(fc[4:8], uint32(f.rect.Dx()))
		binary.BigEndian.PutUint32(fc[8:12], uint32(f.rect.Dy()))
		binary.BigEndian.PutUint32(fc[12:16], uint32(f.rect.Min.X))
		binary.BigEndian.PutUint32(fc[16:20], uint32(f.rect.Min.Y))
		binary.BigEndian.PutUint16(fc[20:22], 5)
		binary.BigEndian.PutUint16(fc[22:24], 100)
		fc[24], fc[25] = f.dispose, f.blend
		appendPNGChunk(&buf, "fcTL", fc)
		for _, part := range imageData(f.rect.Dx(), f.rect.Dy(), f.fill) {
			if i == 0 && !poster {
				appendPNGChunk(&buf, "IDAT", part)
			} else {
				payload := make([]byte, 4, len(part)+4)
				binary.BigEndian.PutUint32(payload, seq)
				seq++
				payload = append(payload, part...)
				appendPNGChunk(&buf, "fdAT", payload)
			}
		}
	}
	appendPNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func compositionAPNG(t *testing.T, poster bool) []byte {
	return makeTestAPNG(t, poster, []apngTestFrame{
		{rect: image.Rect(0, 0, 4, 4), fill: color.NRGBA{R: 255, A: 255}},
		{rect: image.Rect(1, 0, 2, 1), fill: color.NRGBA{B: 255, A: 255}, dispose: 2, blend: 1},
		{rect: image.Rect(2, 0, 3, 1), fill: color.NRGBA{G: 255, A: 255}, dispose: 1, blend: 1},
		{rect: image.Rect(0, 0, 1, 1), fill: color.NRGBA{}, blend: 0},
	})
}

func TestDecodeAPNG(t *testing.T) {
	for _, poster := range []bool{false, true} {
		data := compositionAPNG(t, poster)
		if !isAPNG(data) {
			t.Fatal("APNG not detected")
		}
		frames, delays, err := decodeAnimation(data, 64)
		if err != nil || len(frames) != 4 || len(delays) != 4 {
			t.Fatalf("frames=%d, delays=%d, err=%v", len(frames), len(delays), err)
		}
		if delays[1] != 50*time.Millisecond {
			t.Fatal(delays)
		}
		if r, _, _, _ := frames[0].At(3, 3).RGBA(); r != 65535 {
			t.Fatal("first animation frame was replaced by poster")
		}
		if _, _, b, _ := frames[1].At(1, 0).RGBA(); b != 65535 {
			t.Fatal("offset subframe not drawn")
		}
		if r, _, b, _ := frames[2].At(1, 0).RGBA(); r != 65535 || b != 0 {
			t.Fatal("dispose previous did not restore canvas")
		}
		if _, _, _, a := frames[3].At(2, 0).RGBA(); a != 0 {
			t.Fatal("dispose background did not clear subframe")
		}
		if _, _, _, a := frames[3].At(0, 0).RGBA(); a != 0 {
			t.Fatal("source blend did not replace opaque pixel with transparency")
		}
		if r, _, _, _ := frames[0].At(0, 0).RGBA(); r != 65535 {
			t.Fatal("later frame mutated earlier frame")
		}
		scaled, _, err := decodeAPNG(data, 2)
		if err != nil || scaled[0].Bounds().Dx() != 2 {
			t.Fatalf("scaling failed: %v", err)
		}
	}
}

func TestAPNGInvalid(t *testing.T) {
	data := compositionAPNG(t, false)
	for _, bad := range [][]byte{nil, data[:20], data[:len(data)-12]} {
		if _, _, err := decodeAPNG(bad, 64); err == nil {
			t.Fatal("truncated APNG accepted")
		}
	}
	bad := bytes.Clone(data)
	bad[20] ^= 1
	if _, _, err := decodeAPNG(bad, 64); err == nil {
		t.Fatal("bad checksum accepted")
	}
	bad = makeTestAPNG(t, true, []apngTestFrame{{rect: image.Rect(3, 3, 5, 5), fill: color.NRGBA{A: 255}}})
	if _, _, err := decodeAPNG(bad, 64); err == nil {
		t.Fatal("out-of-canvas frame accepted")
	}
	var still bytes.Buffer
	png.Encode(&still, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if isAPNG(still.Bytes()) {
		t.Fatal("still PNG detected as APNG")
	}
}

func TestAPNGControlOrder(t *testing.T) {
	data := compositionAPNG(t, false)
	// Swap adjacent acTL and fcTL chunks, keeping their contents and CRCs.
	const controlStart = 8 + 25
	controlEnd := controlStart + 20
	frameEnd := controlEnd + 38
	reordered := bytes.Clone(data[:controlStart])
	reordered = append(reordered, data[controlEnd:frameEnd]...)
	reordered = append(reordered, data[controlStart:controlEnd]...)
	reordered = append(reordered, data[frameEnd:]...)
	frames, _, err := decodeAPNG(reordered, 64)
	if err != nil || len(frames) != 4 {
		t.Fatalf("fcTL before acTL: frames=%d err=%v", len(frames), err)
	}
}

func TestAPNGSingleFramePoster(t *testing.T) {
	data := makeTestAPNG(t, true, []apngTestFrame{
		{rect: image.Rect(0, 0, 4, 4), fill: color.NRGBA{R: 255, A: 255}},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()
	im := &images{client: server.Client(), sem: make(chan struct{}, 1), dir: t.TempDir()}
	source := server.URL + "/single.png"
	frames, _, still, err := im.download("single", source, 64)
	if err != nil || len(frames) != 0 || still == nil {
		t.Fatalf("single frame: frames=%d still=%v err=%v", len(frames), still != nil, err)
	}
	if r, g, _, _ := still.At(3, 3).RGBA(); r != 65535 || g != 0 {
		t.Fatal("single frame replaced by green default poster")
	}
	cached, err := im.fromDisk(stillKey("single", source))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, _, _ := cached.At(3, 3).RGBA(); r != 65535 || g != 0 {
		t.Fatal("single frame lost in still cache")
	}
}

func TestAPNGDownloadAndCache(t *testing.T) {
	data := compositionAPNG(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	im := &images{client: server.Client(), sem: make(chan struct{}, 1), dir: t.TempDir()}
	frames, delays, still, err := im.download("test-apng", server.URL+"/sticker.png", 64)
	server.Close()
	if err != nil || len(frames) != 4 || len(delays) != 4 || still != nil {
		t.Fatalf("download lost animation: frames=%d err=%v", len(frames), err)
	}
	cached, cachedDelays, err := im.animFromDisk("test-apng", 64)
	if err != nil || len(cached) != 4 || cachedDelays[0] != delays[0] {
		t.Fatalf("disk cache lost animation: frames=%d err=%v", len(cached), err)
	}
}
