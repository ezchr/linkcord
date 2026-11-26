//go:build windows && !no_voice

package golive

import (
	"bytes"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/op"
	"gioui.org/op/paint"
	"golang.org/x/sys/windows"
)

// The watched stream drawn by Gio's Direct3D 11 renderer, as the GUI does,
// two ways: read back from the graphics card and uploaded again ("readback",
// as before), or kept there in a shared texture ("shared"). Gio's headless
// window has a device of its own, as the GUI's window does. Run with
// LINKCORD_DECODE_BENCH=1 and FFmpeg on PATH (to make the stream once):
//
//	go test ./internal/golive -run 'TestSharedFrame' -v

// TestSharedFrameDraws checks that a picture drawn from the shared texture
// is the one read back.
func TestSharedFrameDraws(t *testing.T) {
	if os.Getenv("LINKCORD_DECODE_BENCH") == "" {
		t.Skip("set LINKCORD_DECODE_BENCH=1 to draw from the graphics card")
	}
	aus := accessUnits(mustRead(t, benchStream(t, benchFFmpeg(t))))[:120]
	const n = 480
	rd, err := openHWDecoder(1080, 1080)
	if err != nil {
		t.Skip(err)
	}
	defer rd.close()
	sd, err := openHWDecoder(1080, 1080)
	if err != nil {
		t.Fatal(err)
	}
	defer sd.close()
	f := sd.newSharedFrame(n, n)
	if f == nil {
		t.Skip("no shared texture")
	}
	defer f.free()
	want := image.NewRGBA(image.Rect(0, 0, n, n))
	for _, au := range aus {
		if _, err := rd.decode(au, want); err != nil {
			t.Fatal(err)
		}
		if _, err := sd.decodeShared(au, f); err != nil {
			t.Fatal(err)
		}
	}
	win, err := headless.NewWindow(n, n)
	if err != nil {
		t.Skip(err)
	}
	defer win.Release()
	var failed error
	src := &testShared{SharedFrame: f, failed: &failed}
	ops := new(op.Ops)
	paint.NewSharedImageOp(src).Add(ops)
	paint.PaintOp{}.Add(ops)
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	if failed != nil {
		t.Fatal(failed)
	}
	got := image.NewRGBA(image.Rect(0, 0, n, n))
	if err := win.Screenshot(got); err != nil {
		t.Fatal(err)
	}
	var sum, worst int
	for i := range got.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		worst = max(worst, d)
	}
	mean := float64(sum) / float64(len(got.Pix))
	t.Logf("drawn from the shared texture vs read back: mean difference %.3f, worst %d", mean, worst)
	if mean > 1 {
		t.Fatalf("the shared picture differs: mean %.3f", mean)
	}
	if bytes.Equal(got.Pix, make([]byte, len(got.Pix))) {
		t.Fatal("nothing drawn")
	}
}

// testShared reports the renderer's failure to the test.
type testShared struct {
	*SharedFrame
	failed *error
}

func (s *testShared) SharedTextureFailed(err error) { *s.failed = err }

func mustRead(t *testing.T, path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSharedFrameCost measures the CPU time of watching the 10-second
// 1080x1080 60 FPS stream, decoding and drawing each picture at 480 and at
// full size, both ways, each in a process of its own.
func TestSharedFrameCost(t *testing.T) {
	if os.Getenv("LINKCORD_DECODE_BENCH") == "" {
		t.Skip("set LINKCORD_DECODE_BENCH=1 to measure")
	}
	stream := benchStream(t, benchFFmpeg(t))
	for _, size := range []int{480, 1080} {
		for _, c := range []string{"readback", "shared"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSharedFrameCostCase$", "-test.v")
			cmd.Env = append(os.Environ(), "LINKCORD_DRAW_CASE="+c, "LINKCORD_DECODE_STREAM="+stream, "LINKCORD_DECODE_SIZE="+strconv.Itoa(size))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", c, err, out)
			}
			i := bytes.Index(out, []byte("RESULT "))
			if i < 0 {
				t.Fatalf("%s: no result\n%s", c, out)
			}
			line := out[i+7:]
			line = line[:bytes.IndexByte(line, '\n')]
			var r decodeCost
			if err := json.Unmarshal(bytes.TrimSpace(line), &r); err != nil {
				t.Fatal(err)
			}
			t.Logf("%4d %-9s frames %3d  CPU %5.2fs = %4.1f%% of one core  peak working set %4d MB",
				size, r.Case, r.Frames, r.CPU.Seconds(), 100*r.CPU.Seconds()/10, r.Peak>>20)
		}
	}
}

// TestSharedFrameCostCase is one case of TestSharedFrameCost.
func TestSharedFrameCostCase(t *testing.T) {
	c := os.Getenv("LINKCORD_DRAW_CASE")
	if c == "" {
		t.Skip("run by TestSharedFrameCost")
	}
	aus := accessUnits(mustRead(t, os.Getenv("LINKCORD_DECODE_STREAM")))
	n, _ := strconv.Atoi(os.Getenv("LINKCORD_DECODE_SIZE"))
	d, err := openHWDecoder(1080, 1080)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	// The window is the size the picture is shown at, as in the GUI.
	win, err := headless.NewWindow(n, n)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	var f *SharedFrame
	var failed error
	var src paint.SharedImage
	var ring [4]*image.RGBA
	if c == "shared" {
		if f = d.newSharedFrame(n, n); f == nil {
			t.Fatal("no shared texture")
		}
		defer f.free()
		src = &testShared{SharedFrame: f, failed: &failed}
	} else {
		for i := range ring {
			ring[i] = image.NewRGBA(image.Rect(0, 0, n, n))
		}
	}
	self := windows.CurrentProcess()
	r := decodeCost{Case: c}
	start := handleCPU(self)
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	ops := new(op.Ops)
	for i, au := range aus {
		<-tick.C
		var got bool
		var img paint.ImageOp
		if c == "shared" {
			got, err = d.decodeShared(au, f)
			img = paint.NewSharedImageOp(src)
		} else {
			got, err = d.decode(au, ring[i%len(ring)])
			img = paint.NewImageOp(ring[i%len(ring)])
		}
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if !got {
			continue
		}
		r.Frames++
		ops.Reset()
		img.Add(ops)
		paint.PaintOp{}.Add(ops)
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		if failed != nil {
			t.Fatal(failed)
		}
	}
	r.CPU = handleCPU(self) - start
	r.Peak = peakWorkingSet(self)
	b, _ := json.Marshal(r)
	t.Logf("RESULT %s\n", b)
}
