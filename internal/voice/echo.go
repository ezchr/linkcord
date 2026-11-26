//go:build !no_voice

package voice

import (
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"

	"github.com/ezchr/linkcord/third_party/aec3"
)

const (
	// echoBlock is how many samples of a channel the canceller takes at a
	// time: 10 ms.
	echoBlock = aec3.FrameSize
	// echoIdle is how many blocks are played without the microphone
	// capturing, as while muted, before what is played is no longer given
	// to the canceller, which has no use for it then.
	echoIdle = 50
)

// echo removes from what the microphone captures the echo of what Linkcord
// plays, which the microphone picks up from the speakers: the others would
// hear themselves. It has two layers. A canceller, WebRTC's AEC3, takes
// what is played out of what was captured, following the delay between the
// two and the room. A gate then tells whether what is left is the user or
// what the canceller missed, by how loud it is beside what is played.
// played is called by the device that plays and capture by whoever sends,
// each from one goroutine at a time.
type echo struct {
	on atomic.Bool
	// idle is how many blocks were played since the last captured.
	idle atomic.Int32

	// renderMu is held while what is played is given to the canceller, and
	// captureMu while it works on what was captured. The canceller is made
	// and freed with both held, captureMu first.
	renderMu, captureMu sync.Mutex
	aec                 *aec3.Canceller
	// pending is what was played short of a whole block, mono.
	pending []int16
	// stereo is whether the two channels captured ever differed. Most
	// microphones are mono, and the canceller then works on one channel,
	// which costs less.
	stereo bool
	mono   []int16

	gate echoGate
}

// newEcho returns an echo that is on.
func newEcho() *echo {
	e := &echo{}
	e.on.Store(true)
	return e
}

// played takes what the speakers are about to play, 48 kHz stereo, with
// what the other devices of Linkcord play added.
func (e *echo) played(output []byte) {
	if !e.on.Load() || e.idle.Load() > echoIdle {
		return
	}
	e.renderMu.Lock()
	defer e.renderMu.Unlock()

	start := len(e.pending)
	for i := 0; i+3 < len(output); i += 4 {
		l := int32(int16(binary.LittleEndian.Uint16(output[i:])))
		r := int32(int16(binary.LittleEndian.Uint16(output[i+2:])))
		e.pending = append(e.pending, int16((l+r)/2))
	}
	addTaps(e.pending[start:])

	n := len(e.pending) / echoBlock * echoBlock
	for at := 0; at < n; at += echoBlock {
		block := e.pending[at : at+echoBlock]
		e.idle.Add(1)
		e.gate.played(meanSquare(block))
		if e.aec != nil {
			e.aec.Render(block)
		}
	}
	e.pending = append(e.pending[:0], e.pending[n:]...)
}

// capture removes the echo from pcm, whole blocks of what the microphone
// captured, 48 kHz stereo, in place, and reports whether what is left may
// be the user speaking, rather than echo.
func (e *echo) capture(pcm []int16) bool {
	e.captureMu.Lock()
	defer e.captureMu.Unlock()

	if !e.on.Load() {
		e.closeLocked()
		return true
	}
	e.idle.Store(0)
	captured := meanSquare(pcm)

	if !e.stereo {
		for i := 0; i+1 < len(pcm); i += 2 {
			if pcm[i] != pcm[i+1] {
				// A canceller for both channels takes the place of the
				// one for one.
				e.stereo = true
				e.freeLocked()
				break
			}
		}
	}
	if e.aec == nil {
		used := 1
		if e.stereo {
			used = channels
		}
		aec := aec3.New(used)
		e.renderMu.Lock()
		e.aec = aec
		e.renderMu.Unlock()
	}

	if e.stereo {
		for at := 0; at+echoBlock*channels <= len(pcm); at += echoBlock * channels {
			e.aec.Capture(pcm[at : at+echoBlock*channels])
		}
	} else {
		e.mono = e.mono[:0]
		for i := 0; i < len(pcm); i += channels {
			e.mono = append(e.mono, pcm[i])
		}
		for at := 0; at+echoBlock <= len(e.mono); at += echoBlock {
			e.aec.Capture(e.mono[at : at+echoBlock])
		}
		for i, v := range e.mono {
			pcm[i*channels], pcm[i*channels+1] = v, v
		}
	}

	return e.gate.open(captured, meanSquare(pcm))
}

// reset forgets the room and the devices, as when a voice channel is left.
func (e *echo) reset() {
	e.captureMu.Lock()
	defer e.captureMu.Unlock()
	e.closeLocked()
}

// closeLocked frees the canceller and forgets what was learned, with
// captureMu held.
func (e *echo) closeLocked() {
	e.freeLocked()
	e.stereo = false
	e.gate.reset()
}

// freeLocked frees the canceller, with captureMu held.
func (e *echo) freeLocked() {
	e.renderMu.Lock()
	aec := e.aec
	e.aec = nil
	e.pending = e.pending[:0]
	e.renderMu.Unlock()
	if aec != nil {
		aec.Close()
	}
}

// meanSquare is how loud pcm is: the mean of its samples squared.
func meanSquare(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, v := range pcm {
		sum += float64(v) * float64(v)
	}
	return sum / float64(len(pcm))
}

const (
	// gateHold is how many frames of 20 ms what was played keeps the gate
	// raised: the echo comes late by the delay of the devices, and goes on
	// in the room for a while after.
	gateHold = 20
	// gateFloor is how loud, in mean square, what is played must be for
	// its echo to be told from the user: -60 dB.
	gateFloor = 32.767 * 32.767
	// gateMargin is how many times as loud as the echo expected, in mean
	// square, what is captured must be to be the user: 6 dB.
	gateMargin = 4
	// gateWindow is how many frames of echo alone the reach of the echo is
	// measured over, and gateWindows how many such measures it takes the
	// largest of.
	gateWindow  = 20
	gateWindows = 3
	// gateApart is how many frames before and after one taken for the user
	// the reach is not measured in: the quiet starts and ends of words.
	gateApart = 10
	// gateTrust is how many times quieter than the echo captured what the
	// canceller leaves is expected to be at the least, in mean square,
	// however little it left of late: 20 dB. When it loses the room for a
	// moment, as when something moves or the clocks of the devices drift
	// apart, what it lets through is not taken for the user.
	gateTrust = 100
)

// echoGate tells the user from the echo of what is played by how loud each
// is. It learns the reach of the echo, how loud what is played arrives at
// the microphone, from the frames in which the user does not speak, and
// while something is played takes for the user only what is clearly louder
// than the echo expected. With the canceller before it, what it learns is
// what the canceller leaves. With nothing played it lets everything
// through. played may be called while open runs.
type echoGate struct {
	// loudest is the loudest block played since the last frame captured, in
	// mean square, as the bits of a float64.
	loudest atomic.Uint64

	// recent are the loudest blocks played during each of the last frames.
	recent [gateHold]float64
	at     int
	// reach is the mean square of what is left of the echo over that of
	// what is played. It is 1 until learned, which errs on the side of
	// taking the user for echo.
	reach   float64
	learned bool
	// waiting are the last frames not taken for the user, to be measured
	// once none after them is, and apart how many of the next are not.
	waiting []gateFrame
	apart   int
	// sum is how loud what was captured, what the canceller left of it and
	// what was played were over the window being measured, of frames
	// frames, and windows the last measures: the reach of the echo as
	// captured, and of what was left of it.
	sum     gateFrame
	frames  int
	windows [][2]float64
}

// gateFrame is how loud a frame was as captured, how loud what the
// canceller left of it, and how loud what was played then.
type gateFrame struct{ mic, left, far float64 }

// played takes how loud a block played is, in mean square.
func (g *echoGate) played(level float64) {
	for {
		old := g.loudest.Load()
		if level <= math.Float64frombits(old) || g.loudest.CompareAndSwap(old, math.Float64bits(level)) {
			return
		}
	}
}

// reset forgets the reach of the echo.
func (g *echoGate) reset() {
	g.loudest.Store(0)
	g.recent = [gateHold]float64{}
	g.reach, g.learned = 0, false
	g.waiting, g.apart = g.waiting[:0], 0
	g.sum, g.frames = gateFrame{}, 0
	g.windows = g.windows[:0]
}

// open takes how loud a frame is as captured and how loud what the
// canceller left of it, in mean square, the same without a canceller, and
// reports whether it may be the user speaking.
func (g *echoGate) open(mic, level float64) bool {
	if !g.learned {
		g.reach = 1
	}
	now := math.Float64frombits(g.loudest.Swap(0))
	g.recent[g.at] = now
	g.at = (g.at + 1) % gateHold
	// held is the loudest played of late, which the echo is no louder
	// than, and spread what was played evened out over as long, which
	// the echo, late by no one knows how much, is measured against.
	var held, spread float64
	for _, v := range g.recent {
		held = max(held, v)
		spread += v / gateHold
	}
	if held < gateFloor {
		return true
	}

	if level > gateMargin*g.reach*held {
		// The user, or an echo that became much louder than learned, as
		// when the speakers are turned up: the reach creeps up for it, to
		// be learned again once it is no longer taken for the user.
		if g.learned {
			g.reach = min(g.reach*1.005, 1)
		}
		g.waiting, g.apart = g.waiting[:0], gateApart
		return true
	}
	if g.apart > 0 {
		g.apart--
		return false
	}
	g.waiting = append(g.waiting, gateFrame{mic, level, spread})
	if len(g.waiting) <= gateApart {
		return false
	}
	g.sum.mic += g.waiting[0].mic
	g.sum.left += g.waiting[0].left
	g.sum.far += g.waiting[0].far
	g.waiting = append(g.waiting[:0], g.waiting[1:]...)
	if g.frames++; g.frames < gateWindow {
		return false
	}

	// A window with next to nothing played measures nothing.
	if g.sum.far >= gateWindow/4*gateFloor {
		g.windows = append(g.windows, [2]float64{g.sum.mic / g.sum.far, g.sum.left / g.sum.far})
		if len(g.windows) > gateWindows {
			g.windows = g.windows[1:]
		}
		// The largest of the last measures: one may miss an echo that
		// had not arrived yet, and taking echo for the user is worse than
		// the user having to speak up.
		var captured, left float64
		for _, w := range g.windows {
			captured, left = max(captured, w[0]), max(left, w[1])
		}
		g.reach, g.learned = max(left, captured/gateTrust), true
	}
	g.sum, g.frames = gateFrame{}, 0
	return false
}

// tap is what one of the other devices of Linkcord plays, as a stream
// watched or a sound, kept for the echo to take it out too.
type tap struct {
	mu  sync.Mutex
	buf []int16
}

// tapMax is how much a tap keeps, mono: 100 ms.
const tapMax = sampleRate / 10

var (
	tapsMu sync.Mutex
	taps   []*tap
)

// newTap returns a tap that addTaps reads until it is closed.
func newTap() *tap {
	t := &tap{}
	tapsMu.Lock()
	taps = append(taps, t)
	tapsMu.Unlock()
	return t
}

func (t *tap) close() {
	tapsMu.Lock()
	defer tapsMu.Unlock()
	for i, other := range taps {
		if other == t {
			taps = append(taps[:i], taps[i+1:]...)
			return
		}
	}
}

// played takes what the device plays, 48 kHz stereo.
func (t *tap) played(output []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i+3 < len(output); i += 4 {
		l := int32(int16(binary.LittleEndian.Uint16(output[i:])))
		r := int32(int16(binary.LittleEndian.Uint16(output[i+2:])))
		t.buf = append(t.buf, int16((l+r)/2))
	}
	if len(t.buf) > tapMax {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tapMax:]...)
	}
}

// addTaps adds to mono what the taps played since it was last called, the
// latest of it if they played more, for it not to fall behind.
func addTaps(mono []int16) {
	tapsMu.Lock()
	defer tapsMu.Unlock()
	for _, t := range taps {
		t.mu.Lock()
		if over := len(t.buf) - len(mono) - sampleRate/50; over > 0 {
			t.buf = append(t.buf[:0], t.buf[over:]...)
		}
		n := min(len(mono), len(t.buf))
		for i, v := range t.buf[:n] {
			mono[i] = int16(max(math.MinInt16, min(math.MaxInt16, int32(mono[i])+int32(v))))
		}
		t.buf = append(t.buf[:0], t.buf[n:]...)
		t.mu.Unlock()
	}
}
