package gui

import (
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVideoDecoderIntegration(t *testing.T) {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		if os.Getenv("LINKCORD_TEST_VIDEO") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	ffprobe, err := mediaExecutable("ffprobe")
	if err != nil {
		if os.Getenv("LINKCORD_TEST_VIDEO") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "color=c=red:s=64x48:r=10:d=0.5",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=0.5",
		"-c:v", "mpeg4", "-c:a", "aac", "-shortest", path)
	hideMediaProcess(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v: %s", err, output)
	}
	meta, err := probeVideo(ctx, ffprobe, path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.width != 64 || meta.height != 48 || !meta.audio || meta.fps != 10 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	count := 0
	err = decodeVideo(ctx, ffmpeg, path, meta, func(frame *image.RGBA, at time.Duration) error {
		if frame.Bounds() != image.Rect(0, 0, 64, 48) {
			t.Fatal("incorrect frame dimensions")
		}
		r, g, b, _ := frame.At(32, 24).RGBA()
		if r < 50000 || g > 5000 || b > 5000 {
			t.Fatal("decoded frame is not red")
		}
		if at != time.Duration(count)*100*time.Millisecond {
			t.Fatalf("incorrect frame timestamp: %v", at)
		}
		count++
		return nil
	})
	if err != nil || count != 5 {
		t.Fatalf("decoded %d frames: %v", count, err)
	}
	audio := &videoAudio{queue: make(chan []byte, 16)}
	audioResult := make(chan error, 1)
	go func() { audioResult <- decodeVideoAudio(ctx, ffmpeg, path, audio) }()
	bytes := 0
	for chunk := range audio.queue {
		bytes += len(chunk)
	}
	if err := <-audioResult; err != nil {
		t.Fatal(err)
	}
	if bytes < videoAudioBytesPerSecond/2 || bytes > videoAudioBytesPerSecond {
		t.Fatalf("unexpected decoded audio size: %d", bytes)
	}
	rotatedPath := filepath.Join(t.TempDir(), "rotated.mp4")
	rotate := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error",
		"-display_rotation:v:0", "90", "-i", path, "-c", "copy", rotatedPath)
	hideMediaProcess(rotate)
	if output, err := rotate.CombinedOutput(); err != nil {
		t.Fatalf("create rotated fixture: %v: %s", err, output)
	}
	rotatedMeta, err := probeVideo(ctx, ffprobe, rotatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedMeta.width != 48 || rotatedMeta.height != 64 {
		t.Fatalf("rotation dimensions incorrect: %+v", rotatedMeta)
	}
	rotatedCount := 0
	if err := decodeVideo(ctx, ffmpeg, rotatedPath, rotatedMeta, func(frame *image.RGBA, at time.Duration) error {
		if frame.Bounds() != image.Rect(0, 0, 48, 64) {
			t.Fatal("rotated frame dimensions incorrect")
		}
		rotatedCount++
		return nil
	}); err != nil || rotatedCount != 5 {
		t.Fatalf("rotated decoding: frames=%d, error=%v", rotatedCount, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	animated, err := gifVideoData(data, 32)
	if err != nil {
		t.Fatal(err)
	}
	frames, delays, err := decodeGIF(animated, 32)
	if err != nil || len(frames) < 2 || len(delays) != len(frames) {
		t.Fatalf("GIF video did not produce a looping animation: frames=%d, error=%v", len(frames), err)
	}
	if frames[0].Bounds().Dx() > 32 || frames[0].Bounds().Dy() > 32 {
		t.Fatal("GIF video preview exceeds requested dimensions")
	}
	var duration time.Duration
	for _, delay := range delays {
		duration += delay
	}
	if duration < 400*time.Millisecond || duration > 600*time.Millisecond {
		t.Fatalf("GIF video timing changed: %v", duration)
	}
	cancelCtx, stop := context.WithCancel(ctx)
	start := time.Now()
	err = decodeVideo(cancelCtx, ffmpeg, path, meta, func(*image.RGBA, time.Duration) error {
		stop()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("decoder did not cancel promptly: %v", err)
	}
}
