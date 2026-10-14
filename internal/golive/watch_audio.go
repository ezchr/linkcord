//go:build windows && !no_voice

package golive

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/disgoorg/godave"
	"github.com/ezchr/linkcord/internal/voice"
	"github.com/tphakala/go-opus/opus"
)

const (
	streamAudioFrame   = 960 * 2 * 2
	streamAudioCushion = 3 * streamAudioFrame
	streamAudioLimit   = 10 * streamAudioFrame
)

type audioPacket struct {
	ssrc uint32
	receivedPacket
}

type streamAudio struct {
	w           *Watch
	input       chan audioPacket
	done        chan struct{}
	once        sync.Once
	closeDevice func()
	mu          sync.Mutex
	pcm         []byte
	playing     bool
}

// SetAudioVolume plays the stream's sound volume times as loud, 0 to 2.
func (w *Watch) SetAudioVolume(volume float32) {
	w.audioVolume.Store(uint32(min(max(volume, 0), 2)*1000) + 1)
}

// AudioVolume is how loud the stream's sound plays, 1 as it comes.
func (w *Watch) AudioVolume() float32 {
	v := w.audioVolume.Load()
	if v == 0 {
		return 1
	}
	return float32(v-1) / 1000
}

// SetAudioMuted mutes the stream's sound, or not.
func (w *Watch) SetAudioMuted(muted bool) { w.audioMuted.Store(muted) }

// AudioMuted reports whether the stream's sound is muted.
func (w *Watch) AudioMuted() bool { return w.audioMuted.Load() }

func newStreamAudio(w *Watch) (*streamAudio, error) {
	a := &streamAudio{w: w, input: make(chan audioPacket, 32), done: make(chan struct{})}
	stop, err := voice.StartMediaAudio(a.fill)
	if err != nil {
		return nil, err
	}
	a.closeDevice = stop
	go a.run()
	return a, nil
}

func (a *streamAudio) stop() {
	a.once.Do(func() {
		close(a.done)
		if a.closeDevice != nil {
			a.closeDevice()
		}
	})
}

func (a *streamAudio) fill(output []byte) {
	// What is not filled is silence: the device's buffer otherwise keeps
	// what it played last, which repeats as noise while audio is waited for
	// or runs out.
	clear(output)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.playing && len(a.pcm) < streamAudioCushion {
		return
	}
	a.playing = true
	n := copy(output, a.pcm)
	a.pcm = a.pcm[n:]
	// As loud as the user chose, or muted.
	switch vol := a.w.AudioVolume(); {
	case a.w.AudioMuted() || vol == 0:
		clear(output[:n])
	case vol != 1:
		for i := 0; i+1 < n; i += 2 {
			v := float32(int16(binary.LittleEndian.Uint16(output[i:]))) * vol
			binary.LittleEndian.PutUint16(output[i:], uint16(int16(max(-32768, min(32767, v)))))
		}
	}
	if len(a.pcm) == 0 {
		a.playing = false
	}
}

func (a *streamAudio) appendPCM(pcm []int16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, sample := range pcm {
		a.pcm = binary.LittleEndian.AppendUint16(a.pcm, uint16(sample))
	}
	if len(a.pcm) > streamAudioLimit {
		// Favor live audio over accumulated delay.
		a.pcm = append(a.pcm[:0], a.pcm[len(a.pcm)-streamAudioCushion:]...)
	}
}

func (a *streamAudio) run() {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	var (
		order    packetOrder
		source   uint32
		decoder  *opus.Decoder
		last     uint16
		haveLast bool
	)
	samples := make([]int16, 48000*2*120/1000)
	drain := func() {
		for {
			p, ok := order.pop(time.Now())
			if !ok || decoder == nil {
				return
			}
			user := godave.UserID(a.w.p.StreamerID.String())
			plain := make([]byte, a.w.conn.dave.MaxDecryptedFrameSize(user, len(p.payload)))
			n, err := a.w.conn.dave.Decrypt(user, p.payload, plain)
			if err != nil {
				continue
			}
			if haveLast {
				gap := p.seq - last
				if gap > 1 && gap <= 4 {
					for i := uint16(1); i < gap; i++ {
						if count, err := decoder.Decode(nil, samples[:960*2]); err == nil {
							a.appendPCM(samples[:count*2])
						}
					}
				}
			}
			last, haveLast = p.seq, true
			count, err := decoder.Decode(plain[:n], samples)
			if err == nil {
				a.appendPCM(samples[:count*2])
			}
		}
	}
	for {
		select {
		case <-a.done:
			return
		case <-a.w.done:
			return
		case <-tick.C:
			drain()
		case p := <-a.input:
			if p.ssrc != source {
				var err error
				decoder, err = opus.NewDecoder(48000, 2)
				if err != nil {
					a.w.log.Warn("could not decode stream audio", "err", err)
					return
				}
				source, order, haveLast = p.ssrc, packetOrder{}, false
				a.mu.Lock()
				a.pcm, a.playing = nil, false
				a.mu.Unlock()
			}
			order.push(p.receivedPacket)
			drain()
		}
	}
}

func (w *Watch) receiveAudio(ssrc uint32, seq uint16, payload []byte) {
	w.mu.Lock()
	audio, expected := w.audio, w.audioSSRC
	w.mu.Unlock()
	// Wait for the streamer's announced audio SSRC instead of guessing from
	// another user's media.
	if audio == nil || expected == 0 || ssrc != expected {
		return
	}
	p := audioPacket{ssrc: ssrc, receivedPacket: receivedPacket{
		seq: seq, payload: payload, at: time.Now(),
	}}
	select {
	case audio.input <- p:
	case <-audio.done:
	default:
	}
}
