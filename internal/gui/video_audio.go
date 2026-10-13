package gui

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/ezchr/linkcord/internal/voice"
)

const videoAudioBytesPerSecond = 48000 * 2 * 2

// A bounded queue decouples the decoder from the real-time audio callback.
type videoAudio struct {
	queue   chan []byte
	current []byte
	paused  atomic.Bool
	muted   atomic.Bool
	played  atomic.Int64
	eof     atomic.Bool
	stop    func()
}

func newVideoAudio() (*videoAudio, error) {
	audio := &videoAudio{queue: make(chan []byte, 16)}
	stop, err := voice.StartMediaAudio(audio.fill)
	if err != nil {
		return nil, err
	}
	audio.stop = stop
	return audio, nil
}

func (a *videoAudio) fill(output []byte) {
	clear(output)
	if a.paused.Load() {
		return
	}
	consumed := 0
	for len(output) > 0 {
		if len(a.current) == 0 {
			select {
			case chunk, ok := <-a.queue:
				if !ok {
					a.eof.Store(true)
					a.played.Add(int64(consumed))
					return
				}
				a.current = chunk
			default:
				a.played.Add(int64(consumed))
				return
			}
		}
		n := min(len(output), len(a.current))
		if !a.muted.Load() {
			copy(output[:n], a.current[:n])
		}
		output = output[n:]
		a.current = a.current[n:]
		consumed += n
	}
	a.played.Add(int64(consumed))
}

func (a *videoAudio) position() time.Duration {
	return time.Duration(a.played.Load()) * time.Second / videoAudioBytesPerSecond
}

func (a *videoAudio) read(ctx context.Context, reader io.Reader) error {
	defer close(a.queue)
	for {
		chunk := make([]byte, 19200)
		n, err := io.ReadFull(reader, chunk)
		// Complete stereo samples only.
		n -= n % 4
		if n > 0 {
			select {
			case a.queue <- chunk[:n]:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
