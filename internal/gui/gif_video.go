package gui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"
)

// This fragment is an internal cache hint; HTTP never sends URL fragments.
const gifVideoFragment = "linkcord-gifv-v1"

func isGIFVideoURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Fragment == gifVideoFragment
}

type limitedGIFBuffer struct {
	bytes.Buffer
}

func (b *limitedGIFBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 16<<20 {
		return 0, errors.New("GIF preview exceeds the animation download limit")
	}
	return b.Buffer.Write(data)
}

// GIFV embeds are silent looping animations. Convert their video rendition once
// in an image-cache worker, then reuse the normal GIF playback and disk cache.
func gifVideoData(data []byte, size int) ([]byte, error) {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "linkcord-gifv-*.mp4")
	if err != nil {
		return nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	side := max(min(size, 640), 1)
	filter := fmt.Sprintf("fps=20,scale=%d:%d:force_original_aspect_ratio=decrease", side, side)
	cmd := mediaDecodeCommand(ctx, ffmpeg, name,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter, "-loop", "0", "-f", "gif", "pipe:1")
	var output limitedGIFBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("could not decode GIF video preview: %w", err)
	}
	return output.Bytes(), nil
}
