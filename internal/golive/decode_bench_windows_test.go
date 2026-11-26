//go:build windows && !no_voice

package golive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The decoders compared, CPU time and memory, on a 10-second 1080x1080
// stream at 60 FPS, as a stream is watched: one picture every 16.7 ms, at
// 480x480 and at full size. Run with LINKCORD_DECODE_BENCH=1 and FFmpeg on
// PATH (or LINKCORD_FFMPEG set to it):
//
//	go test ./internal/golive -run TestDecodeCost -v
//
// Each case runs in a process of its own, so that its peak memory is its own.

type decodeCost struct {
	Case     string
	Frames   int
	CPU      time.Duration // the test process's
	ChildCPU time.Duration // FFmpeg's
	Peak     uint64        // the test process's peak working set
	ChildPk  uint64
}

func benchFFmpeg(t *testing.T) string {
	if p := os.Getenv("LINKCORD_FFMPEG"); p != "" {
		return p
	}
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is needed")
	}
	return p
}

// benchStream makes, once, the stream decoded: H264 in Annex B with an
// access unit delimiter before each picture, without B pictures, as live.
func benchStream(t *testing.T, ffmpeg string) string {
	out := filepath.Join(os.TempDir(), "linkcord-bench-1080x1080-60.h264")
	if _, err := os.Stat(out); err == nil {
		return out
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1080x1080:rate=60", "-t", "10",
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-g", "120", "-b:v", "8M",
		"-pix_fmt", "yuv420p", "-bsf:v", "h264_metadata=aud=insert", "-f", "h264", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("making the stream: %v: %s", err, b)
	}
	return out
}

// accessUnits splits an Annex B stream at its access unit delimiters.
func accessUnits(b []byte) [][]byte {
	var aus [][]byte
	start := -1
	for i := 0; i+4 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 && b[i+3]&0x1F == 9 {
			s := i
			if s > 0 && b[s-1] == 0 {
				s--
			}
			if start >= 0 {
				aus = append(aus, b[start:s])
			}
			start = s
		}
	}
	if start >= 0 {
		aus = append(aus, b[start:])
	}
	return aus
}

type memCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var procMemInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

func peakWorkingSet(h windows.Handle) uint64 {
	var m memCounters
	m.cb = uint32(unsafe.Sizeof(m))
	procMemInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.cb))
	return uint64(m.peakWorkingSetSize)
}

func handleCPU(h windows.Handle) time.Duration {
	var c, e, k, u windows.Filetime
	windows.GetProcessTimes(h, &c, &e, &k, &u)
	ft := func(f windows.Filetime) time.Duration {
		return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100
	}
	return ft(k) + ft(u)
}

func TestDecodeCost(t *testing.T) {
	if os.Getenv("LINKCORD_DECODE_BENCH") == "" {
		t.Skip("set LINKCORD_DECODE_BENCH=1 to measure the decoders")
	}
	ffmpeg := benchFFmpeg(t)
	stream := benchStream(t, ffmpeg)
	for _, c := range []string{"inproc-480", "ffmpeg-480", "inproc-full", "ffmpeg-full"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDecodeCostCase$", "-test.v")
		cmd.Env = append(os.Environ(), "LINKCORD_DECODE_CASE="+c, "LINKCORD_DECODE_STREAM="+stream, "LINKCORD_FFMPEG="+ffmpeg)
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
		t.Logf("%-12s frames %3d  CPU %6.2fs (Linkcord %5.2fs + FFmpeg %5.2fs) = %4.1f%% of one core  peak working set %4d MB (Linkcord %4d MB + FFmpeg %4d MB)",
			r.Case, r.Frames, (r.CPU + r.ChildCPU).Seconds(), r.CPU.Seconds(), r.ChildCPU.Seconds(),
			100*(r.CPU+r.ChildCPU).Seconds()/10, (r.Peak+r.ChildPk)>>20, r.Peak>>20, r.ChildPk>>20)
	}
}

// TestDecodeCostCase is one case of TestDecodeCost, in its own process.
func TestDecodeCostCase(t *testing.T) {
	c := os.Getenv("LINKCORD_DECODE_CASE")
	if c == "" {
		t.Skip("run by TestDecodeCost")
	}
	b, err := os.ReadFile(os.Getenv("LINKCORD_DECODE_STREAM"))
	if err != nil {
		t.Fatal(err)
	}
	aus := accessUnits(b)
	ow, oh := 1080, 1080
	if c == "inproc-480" || c == "ffmpeg-480" {
		ow, oh = 480, 480
	}
	if v, _ := strconv.Atoi(os.Getenv("LINKCORD_DECODE_SIZE")); v > 0 {
		ow, oh = v, v
	}
	self := windows.CurrentProcess()
	r := decodeCost{Case: c}
	start := handleCPU(self)
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	switch c {
	case "inproc-480", "inproc-full":
		d, err := openHWDecoder(1080, 1080)
		if err != nil {
			t.Fatal(err)
		}
		var ring [4]*image.RGBA
		for i := range ring {
			ring[i] = image.NewRGBA(image.Rect(0, 0, ow, oh))
		}
		for i, au := range aus {
			<-tick.C
			got, err := d.decode(au, ring[i%len(ring)])
			if err != nil {
				t.Fatalf("picture %d: %v", i, err)
			}
			if got {
				r.Frames++
				if png := os.Getenv("LINKCORD_DECODE_PNG"); png != "" && i == 300 {
					savePNG(t, png, ring[i%len(ring)])
				}
			}
		}
		d.close()
	default:
		args := []string{"-hide_banner", "-loglevel", "error", "-fflags", "nobuffer", "-flags", "low_delay",
			"-probesize", "32", "-analyzeduration", "0", "-hwaccel", "d3d11va", "-f", "h264", "-i", "pipe:0"}
		if ow != 1080 {
			args = append(args, "-vf", fmt.Sprintf("scale=%d:%d:flags=area", ow, oh))
		}
		args = append(args, "-fps_mode", "passthrough", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
		cmd := exec.Command(os.Getenv("LINKCORD_FFMPEG"), args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		in, _ := cmd.StdinPipe()
		out, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		go func() {
			for _, au := range aus {
				<-tick.C
				in.Write(au)
			}
			in.Close()
		}()
		var ring [4]*image.RGBA
		for i := range ring {
			ring[i] = image.NewRGBA(image.Rect(0, 0, ow, oh))
		}
		for i := 0; ; i++ {
			if _, err := io.ReadFull(out, ring[i%len(ring)].Pix); err != nil {
				break
			}
			r.Frames++
		}
		cmd.Wait()
		r.ChildPk = peakWorkingSet(h)
		r.ChildCPU = handleCPU(h)
	}
	r.CPU = handleCPU(self) - start
	r.Peak = peakWorkingSet(self)
	j, _ := json.Marshal(r)
	fmt.Println("RESULT " + string(j))
}

func savePNG(t *testing.T, path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
