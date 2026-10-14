//go:build windows && !no_voice

package golive

import (
	"bytes"
	"sync"
)

type pictureJob struct {
	frame      []byte
	generation uint64
}

func (w *Watch) requireKeyframe() {
	w.videoGeneration.Add(1)
	w.needIDR.Store(true)
}

func (w *Watch) submitPicture(frame []byte) {
	if w.decodeQueue == nil {
		w.decode(frame)
		return
	}
	job := pictureJob{frame: bytes.Clone(frame), generation: w.videoGeneration.Load()}
	select {
	case w.decodeQueue <- job:
	case <-w.done:
	default:
		// Never block the UDP reader behind a slow decoder.
		w.requireKeyframe()
		w.requestKeyframe()
	}
}

func (w *Watch) decodeLoop() {
	var generation uint64
	for {
		select {
		case <-w.done:
			return
		case job := <-w.decodeQueue:
			select {
			case <-w.done:
				return
			default:
			}
			if job.generation != generation {
				w.needIDR.Store(true)
				generation = job.generation
			}
			w.decode(job.frame)
		}
	}
}

// FFmpeg writes stderr on another goroutine. Retain only the final 64 KiB.
type decoderLog struct {
	mu   sync.Mutex
	data []byte
}

func (l *decoderLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if len(p) >= 64<<10 {
		l.data = append(l.data[:0], p[len(p)-(64<<10):]...)
	} else {
		l.data = append(l.data, p...)
		if len(l.data) > 64<<10 {
			l.data = bytes.Clone(l.data[len(l.data)-(64<<10):])
		}
	}
	return n, nil
}

func (l *decoderLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.data)
}
