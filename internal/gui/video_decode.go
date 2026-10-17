package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type videoMetadata struct {
	width, height int
	fps           float64
	audio         bool
	duration      time.Duration // zero when unknown
}

func probeVideo(ctx context.Context, executable, path string) (videoMetadata, error) {
	// A streamed video whose index sits at its end is probed once it has all
	// downloaded, which the download limit bounds.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	args := append([]string{"-v", "error"}, mediaInput(path)...)
	args = append(args, "-show_entries", "stream=codec_type,width,height,avg_frame_rate:stream_side_data=rotation:format=duration",
		"-of", "json")
	cmd := exec.CommandContext(ctx, executable, args...)
	hideMediaProcess(cmd)
	data, err := cmd.Output()
	if err != nil {
		return videoMetadata{}, errors.New("could not inspect this video")
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Rate      string `json:"avg_frame_rate"`
			SideData  []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return videoMetadata{}, errors.New("invalid video metadata")
	}
	meta := videoMetadata{fps: 30}
	if d, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && d > 0 && d < 24*3600 {
		meta.duration = time.Duration(d * float64(time.Second))
	}
	for _, stream := range result.Streams {
		if stream.CodecType == "audio" {
			meta.audio = true
		}
		if stream.CodecType != "video" || meta.width != 0 {
			continue
		}
		meta.width, meta.height = stream.Width, stream.Height
		for _, side := range stream.SideData {
			rotation := math.Mod(math.Abs(side.Rotation), 180)
			if math.Abs(rotation-90) < 0.01 {
				meta.width, meta.height = meta.height, meta.width
				break
			}
			if rotation > 0.01 && math.Abs(rotation-180) > 0.01 {
				return videoMetadata{}, errors.New("video uses an unsupported rotation angle")
			}
		}
		parts := strings.Split(stream.Rate, "/")
		if len(parts) == 2 {
			n, _ := strconv.ParseFloat(parts[0], 64)
			d, _ := strconv.ParseFloat(parts[1], 64)
			if d > 0 && n > 0 && !math.IsInf(n/d, 0) && !math.IsNaN(n/d) {
				meta.fps = n / d
			}
		}
	}
	if meta.width <= 0 || meta.height <= 0 || meta.width > 8192 || meta.height > 8192 ||
		int64(meta.width)*int64(meta.height) > 3840*2160 {
		return videoMetadata{}, errors.New("video dimensions are unsupported (maximum 4K pixel count)")
	}
	if meta.fps > 120 {
		return videoMetadata{}, errors.New("video frame rate exceeds 120 fps")
	}
	return meta, nil
}

// streamFormats are the containers a streamed video or audio file may use;
// formats such as playlists, which point the decoder at other addresses, are
// not allowed.
const streamFormats = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,avi,mpegts,ogg,flv,wav,w64,mp3,flac,aac,asf,aiff"

// mediaInput gives the decoder its input: a local file, or a video streamed
// from this program on the loopback address while it downloads.
func mediaInput(path string) []string {
	if strings.HasPrefix(path, "http://127.0.0.1:") {
		return []string{"-protocol_whitelist", "http,tcp", "-format_whitelist", streamFormats, "-i", path}
	}
	return []string{"-protocol_whitelist", "file,pipe", "-i", path}
}

// videoBox is the largest frame decoded for the viewer, which never shows
// videos much bigger; full 1080p frames are 8 MB each to copy and upload.
const videoBoxLong, videoBoxShort = 1280, 720

// scaled returns the frame size the video is decoded at: its own, or that
// fitted in the box, kept even as the decoder wants.
func (m videoMetadata) scaled() (int, int) {
	boxW, boxH := videoBoxLong, videoBoxShort
	if m.height > m.width {
		boxW, boxH = boxH, boxW
	}
	if m.width <= boxW && m.height <= boxH {
		return m.width, m.height
	}
	k := math.Min(float64(boxW)/float64(m.width), float64(boxH)/float64(m.height))
	w := max(2, int(float64(m.width)*k)&^1)
	h := max(2, int(float64(m.height)*k)&^1)
	return w, h
}

func mediaDecodeCommand(ctx context.Context, executable, path string, output ...string) *exec.Cmd {
	return mediaDecodeCommandAt(ctx, executable, path, 0, output...)
}

// mediaDecodeCommandAt decodes from start onwards.
func mediaDecodeCommandAt(ctx context.Context, executable, path string, start time.Duration, output ...string) *exec.Cmd {
	args := []string{"-nostdin", "-v", "error", "-threads", "0"}
	if start > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", start.Seconds()))
	}
	args = append(args, mediaInput(path)...)
	args = append(args, output...)
	cmd := exec.CommandContext(ctx, executable, args...)
	hideMediaProcess(cmd)
	return cmd
}

// decodeVideo publishes frames synchronously to a bounded consumer. Only the
// current frame and pipe buffers are retained, regardless of clip duration.
func decodeVideo(ctx context.Context, executable, path string, meta videoMetadata, start time.Duration, publish func(*image.RGBA, time.Duration) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	width, height := meta.scaled()
	filter := fmt.Sprintf("fps=%.9f", meta.fps)
	if width != meta.width || height != meta.height {
		filter += fmt.Sprintf(",scale=%d:%d:flags=bilinear", width, height)
	}
	cmd := mediaDecodeCommandAt(ctx, executable, path, start,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter,
		"-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.New("could not start video decoder")
	}
	// A few frames are read ahead, so that one slow to decode does not hold
	// up the picture.
	frames := make(chan *image.RGBA, 4)
	var readErr error
	go func() {
		defer close(frames)
		for {
			frame := image.NewRGBA(image.Rect(0, 0, width, height))
			if _, err := io.ReadFull(pipe, frame.Pix); err != nil {
				if err != io.EOF {
					readErr = errors.New("video decoding stopped unexpectedly")
				}
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	var decodeErr error
	index := int64(0)
	for frame := range frames {
		at := start + time.Duration(float64(index)*float64(time.Second)/meta.fps)
		index++
		if err := publish(frame, at); err != nil {
			decodeErr = err
			cancel()
			break
		}
	}
	for range frames {
	}
	if decodeErr == nil {
		decodeErr = readErr
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if decodeErr != nil {
		return decodeErr
	}
	if waitErr != nil {
		return errors.New("video codec could not be decoded")
	}
	return nil
}

func decodeVideoAudio(ctx context.Context, executable, path string, start time.Duration, audio *videoAudio) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := mediaDecodeCommandAt(ctx, executable, path, start,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "2", "-ar", "48000",
		"-acodec", "pcm_s16le", "-f", "s16le", "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		close(audio.queue)
		return err
	}
	if err := cmd.Start(); err != nil {
		close(audio.queue)
		return errors.New("could not start audio decoder")
	}
	readErr := audio.read(ctx, pipe)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return errors.New("video audio could not be decoded")
	}
	return nil
}

func removeVideoFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
