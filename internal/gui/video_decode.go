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
}

func probeVideo(ctx context.Context, executable, path string) (videoMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-v", "error",
		"-protocol_whitelist", "file,pipe",
		"-show_entries", "stream=codec_type,width,height,avg_frame_rate:stream_side_data=rotation",
		"-of", "json", path)
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
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return videoMetadata{}, errors.New("invalid video metadata")
	}
	meta := videoMetadata{fps: 30}
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

func mediaDecodeCommand(ctx context.Context, executable, path string, output ...string) *exec.Cmd {
	args := []string{"-nostdin", "-v", "error", "-threads", "2",
		"-protocol_whitelist", "file,pipe", "-i", path}
	args = append(args, output...)
	cmd := exec.CommandContext(ctx, executable, args...)
	hideMediaProcess(cmd)
	return cmd
}

// decodeVideo publishes frames synchronously to a bounded consumer. Only the
// current frame and pipe buffers are retained, regardless of clip duration.
func decodeVideo(ctx context.Context, executable, path string, meta videoMetadata, publish func(*image.RGBA, time.Duration) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	filter := fmt.Sprintf("fps=%.9f", meta.fps)
	cmd := mediaDecodeCommand(ctx, executable, path,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter,
		"-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.New("could not start video decoder")
	}
	var decodeErr error
	for index := int64(0); ; index++ {
		frame := image.NewRGBA(image.Rect(0, 0, meta.width, meta.height))
		_, err := io.ReadFull(pipe, frame.Pix)
		if err != nil {
			if err != io.EOF {
				decodeErr = errors.New("video decoding stopped unexpectedly")
			}
			break
		}
		at := time.Duration(float64(index) * float64(time.Second) / meta.fps)
		if err := publish(frame, at); err != nil {
			decodeErr = err
			cancel()
			break
		}
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

func decodeVideoAudio(ctx context.Context, executable, path string, audio *videoAudio) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := mediaDecodeCommand(ctx, executable, path,
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
