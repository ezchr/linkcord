//go:build windows && !no_voice

package golive

/*
#include "native.h"
*/
import "C"

import (
	"bytes"
	"image"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// VideoParams is what receiving one sender's H264 video needs. The video
// comes on a connection of its owner's, which decrypts the packets and
// gives them to Push.
type VideoParams struct {
	Log *slog.Logger
	// Decrypt removes the end-to-end encryption of a picture into out,
	// which MaxDecrypted(len(au)) bytes fit; nil when there is none.
	Decrypt      func(au, out []byte) (int, error)
	MaxDecrypted func(size int) int
	// SendRTCP sends an RTCP packet, sealed by the connection; Ours is the
	// SSRC of this end on it, which the packets are from.
	SendRTCP func(pkt []byte) error
	Ours     func() uint32
	// OnFrame is called, from another goroutine, when a picture is ready.
	OnFrame func()
	// NoDecoder, for tests, has pictures handed to OnPicture rather than
	// decoded.
	NoDecoder bool
	OnPicture func(pic []byte)
}

// VideoReceiver receives one sender's H264 video, as a camera in a voice
// channel: it puts the packets back in order, asks again for those lost
// (NACK, and RTX resends), rebuilds each picture, removes its end-to-end
// encryption, and decodes it on the graphics card at the size it is shown
// at, into a shared texture or a picture read back. It asks for keyframes
// (PLI) when a picture is lost and reports how the video arrives.
type VideoReceiver struct {
	p VideoParams

	mu        sync.Mutex
	video     uint32
	rtx       uint32
	order     packetOrder
	asm       h264Assembler
	reception watchReception
	// videoNow is video, read without mu by requestKeyframe, which runs
	// with it held.
	videoNow atomic.Uint32

	queue chan []byte
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup

	needIDR atomic.Bool
	lastPLI atomic.Int64
	// view is the size the video is shown at, width<<32|height, set by
	// SetView; 0 is unknown. viewAt is when it was last set.
	view   atomic.Uint64
	viewAt atomic.Int64
	frames atomic.Int64
	// unavailable is set once the graphics card cannot decode the video.
	unavailable atomic.Bool
	packets     atomic.Int64
	lost        atomic.Int64
	daveFailed  atomic.Int64

	// The decoder, used by the decoding goroutine only.
	dec      *hwDecoder
	srcW     int
	srcH     int
	ring     [4]*image.RGBA
	ri       int
	tex      *SharedFrame
	noShared bool

	// fmu guards what the GUI takes: frame, the last picture read back,
	// taken, the one it holds, and current, the shared texture with the
	// last picture.
	fmu     sync.Mutex
	frame   *image.RGBA
	taken   *image.RGBA
	current *SharedFrame
	size    image.Point
}

// NewVideoReceiver starts receiving a video; SetSSRCs tells which.
func NewVideoReceiver(p VideoParams) *VideoReceiver {
	if p.Log == nil {
		p.Log = slog.Default()
	}
	r := &VideoReceiver{p: p, queue: make(chan []byte, 8), done: make(chan struct{})}
	r.needIDR.Store(true)
	r.wg.Add(2)
	go r.decodeLoop()
	go r.tick()
	return r
}

// SetSSRCs sets the SSRCs of the video and of its resends. Another video
// starts again from a keyframe.
func (r *VideoReceiver) SetSSRCs(video, rtx uint32) {
	r.mu.Lock()
	changed := video != r.video
	r.video, r.rtx = video, rtx
	r.videoNow.Store(video)
	if changed {
		r.order = packetOrder{}
		r.asm.reset()
	}
	r.mu.Unlock()
	if changed {
		r.needIDR.Store(true)
		r.lastPLI.Store(0)
		r.requestKeyframe()
	}
}

// SSRCs are the SSRCs of the video and of its resends.
func (r *VideoReceiver) SSRCs() (video, rtx uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.video, r.rtx
}

// Push gives a packet of the video, or of its resends, by its SSRC. The
// payload is copied. It does not block.
func (r *VideoReceiver) Push(ssrc uint32, seq uint16, ts uint32, marker bool, payload []byte) {
	if r.Closed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.video == 0 {
		return
	}
	if ssrc == r.rtx && r.rtx != 0 && ssrc != r.video {
		// A resend carries the original sequence number first.
		if len(payload) < 3 {
			return
		}
		seq, payload = uint16(payload[0])<<8|uint16(payload[1]), payload[2:]
	} else if ssrc != r.video {
		return
	}
	if len(payload) == 0 {
		return
	}
	now := time.Now()
	if !r.order.push(receivedPacket{seq: seq, ts: ts, marker: marker, payload: bytes.Clone(payload), at: now}) {
		return
	}
	r.packets.Add(1)
	r.reception.record(r.video, seq, ts, now)
	r.drain(now)
}

// drain consumes the packets in order and asks for those missing. Called
// with mu held.
func (r *VideoReceiver) drain(now time.Time) {
	for {
		p, ok := r.order.pop(now)
		if !ok {
			break
		}
		r.asm.push(p, r.submit, r.lostPicture)
	}
	if missing := r.order.missing(now); len(missing) > 0 && r.p.SendRTCP != nil {
		r.p.SendRTCP(nackPacket(r.ours(), r.video, missing))
	}
}

func (r *VideoReceiver) ours() uint32 {
	if r.p.Ours == nil {
		return 0
	}
	return r.p.Ours()
}

func (r *VideoReceiver) lostPicture() {
	r.lost.Add(1)
	r.needIDR.Store(true)
	r.requestKeyframe()
}

// submit queues a picture for decoding, never blocking the reader.
func (r *VideoReceiver) submit(au []byte) {
	select {
	case r.queue <- bytes.Clone(au):
	default:
		r.lostPicture()
	}
}

// requestKeyframe asks the sender for a whole picture, at most twice a
// second.
func (r *VideoReceiver) requestKeyframe() {
	now := time.Now().UnixNano()
	last := r.lastPLI.Load()
	if now-last < int64(500*time.Millisecond) || r.p.SendRTCP == nil {
		return
	}
	video := r.videoNow.Load()
	if video == 0 {
		return
	}
	if r.p.SendRTCP(pliPacket(r.ours(), video)) == nil {
		r.lastPLI.CompareAndSwap(last, now)
	}
}

// tick lets packets held for reordering go once waited for, asks for
// keyframes until a picture shows and when the video shows again, and
// reports reception every second.
func (r *VideoReceiver) tick() {
	defer r.wg.Done()
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	n := 0
	shown := true
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
		}
		n++
		r.mu.Lock()
		r.drain(time.Now())
		r.mu.Unlock()
		if n%12 == 0 && r.frames.Load() == 0 {
			r.requestKeyframe()
		}
		if s := r.Shown(); s != shown {
			shown = s
			if s {
				// Back on screen: the video starts again from a keyframe.
				r.needIDR.Store(true)
				r.lastPLI.Store(0)
				r.requestKeyframe()
			}
		}
		if n%50 == 0 {
			r.report()
		}
	}
}

// cameraBitrate is what this end says it can take of a camera, in bits a
// second, as Discord's maximum for one.
const cameraBitrate = 2_500_000

// report sends a receiver report with a REMB.
func (r *VideoReceiver) report() {
	if r.p.SendRTCP == nil {
		return
	}
	r.mu.Lock()
	video := r.video
	var rr []byte
	if video != 0 {
		rr = r.reception.report(r.ours(), video, time.Now())
	}
	r.mu.Unlock()
	if video == 0 {
		return
	}
	r.p.SendRTCP(rr)
	r.p.SendRTCP(rembPacket(r.ours(), video, cameraBitrate))
}

// decodeLoop decrypts and decodes the pictures queued, until closed.
func (r *VideoReceiver) decodeLoop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.done:
			return
		case au := <-r.queue:
			r.decode(au)
		}
	}
}

// decode decrypts a picture and decodes it, the decoder opened at the size
// its SPS gives.
func (r *VideoReceiver) decode(au []byte) {
	pic := au
	if r.p.Decrypt != nil {
		size := len(au)
		if r.p.MaxDecrypted != nil {
			size = r.p.MaxDecrypted(len(au))
		}
		out := make([]byte, max(size, len(au)))
		n, err := r.p.Decrypt(au, out)
		if err != nil || n < 0 || n > len(out) {
			if c := r.daveFailed.Add(1); c <= 3 || c%100 == 0 {
				r.p.Log.Info("could not decrypt a camera picture", "err", err, "failures", c)
			}
			r.needIDR.Store(true)
			r.requestKeyframe()
			return
		}
		pic = out[:n]
	}
	idr := hasIDR(pic)
	if r.needIDR.Load() {
		if !idr {
			r.requestKeyframe()
			return
		}
		r.needIDR.Store(false)
	}
	if r.p.OnPicture != nil {
		r.p.OnPicture(pic)
	}
	if r.p.NoDecoder || r.unavailable.Load() {
		return
	}
	for _, nal := range splitNALs(pic) {
		if len(nal) > 1 && nal[0]&0x1F == 7 {
			if sw, sh, err := spsSize(nal[1:]); err == nil && sw > 0 && sh > 0 && (sw != r.srcW || sh != r.srcH || r.dec == nil) {
				r.openDecoder(sw, sh)
			}
		}
	}
	if r.dec == nil {
		r.needIDR.Store(true)
		r.requestKeyframe()
		return
	}
	view := r.view.Load()
	dw, dh := decodeSizeFor(r.srcW, r.srcH, view)
	ow, oh := viewSizeFor(dw, dh, view)
	ow, oh = max(ow, 2), max(oh, 2)
	got, err := r.decodeInto(pic, ow, oh)
	if err != nil {
		r.p.Log.Warn("the camera's decoder failed", "err", err)
		r.closeDecoder()
		r.needIDR.Store(true)
		r.requestKeyframe()
		return
	}
	if got {
		r.frames.Add(1)
		if r.p.OnFrame != nil {
			r.p.OnFrame()
		}
	}
}

// openDecoder opens the graphics card's decoder for a sw×sh video in place
// of any other.
func (r *VideoReceiver) openDecoder(sw, sh int) {
	r.closeDecoder()
	if !InProcessDecoding() {
		r.unavailable.Store(true)
		r.p.Log.Info("the graphics card cannot decode cameras")
		return
	}
	d, err := openHWDecoder(sw, sh)
	if err != nil {
		r.unavailable.Store(true)
		r.p.Log.Info("could not open a decoder for a camera", "err", err)
		return
	}
	r.dec, r.srcW, r.srcH = d, sw, sh
	r.p.Log.Info("camera size", "width", sw, "height", sh)
}

// closeDecoder closes the decoder and its textures; the GUI keeps no
// handle to them (it sees 0 once freed).
func (r *VideoReceiver) closeDecoder() {
	r.fmu.Lock()
	cur := r.current
	r.current = nil
	r.fmu.Unlock()
	if cur != nil && cur != r.tex {
		cur.free()
	}
	if r.tex != nil {
		r.tex.free()
		r.tex = nil
	}
	if r.dec != nil {
		r.dec.close()
		r.dec = nil
	}
}

// decodeInto decodes pic at ow×oh, into the shared texture where the GUI
// can draw it, else into a picture read back.
func (r *VideoReceiver) decodeInto(pic []byte, ow, oh int) (bool, error) {
	if !r.noShared && sharedFrames() {
		if r.tex == nil || r.tex.size != image.Pt(ow, oh) {
			f := r.dec.newSharedFrame(ow, oh)
			if f == nil {
				r.noShared = true
				return r.decodeReadback(pic, ow, oh)
			}
			r.fmu.Lock()
			keep := r.tex == r.current
			r.fmu.Unlock()
			// The last one stays until this one has a picture.
			if r.tex != nil && !keep {
				r.tex.free()
			}
			log := r.p.Log
			f.log = func(err error) {
				log.Warn("the GUI cannot draw videos from the graphics card; reading pictures back", "err", err)
			}
			r.tex = f
		}
		got, err := r.dec.decodeShared(pic, r.tex)
		if err != nil {
			return false, err
		}
		if C.lc_hwdec_no_shared(r.dec.c) != 0 {
			r.noShared = true
			r.fmu.Lock()
			cur := r.current
			r.current = nil
			r.fmu.Unlock()
			if cur != nil && cur != r.tex {
				cur.free()
			}
			r.tex.free()
			r.tex = nil
			return false, nil
		}
		if !got {
			return false, nil
		}
		r.fmu.Lock()
		old := r.current
		r.current, r.frame, r.size = r.tex, nil, image.Pt(ow, oh)
		r.fmu.Unlock()
		if old != nil && old != r.tex {
			old.free()
		}
		return true, nil
	}
	if r.tex != nil {
		r.fmu.Lock()
		cur := r.current
		r.current = nil
		r.fmu.Unlock()
		if cur != nil && cur != r.tex {
			cur.free()
		}
		r.tex.free()
		r.tex = nil
	}
	return r.decodeReadback(pic, ow, oh)
}

// decodeReadback decodes pic into a picture in memory, one of a few used in
// turn, never the one shown nor the one the GUI holds.
func (r *VideoReceiver) decodeReadback(pic []byte, ow, oh int) (bool, error) {
	r.fmu.Lock()
	i := r.ri
	for k := 0; k < len(r.ring) && r.ring[i] != nil && (r.ring[i] == r.frame || r.ring[i] == r.taken); k++ {
		i = (i + 1) % len(r.ring)
	}
	r.fmu.Unlock()
	if r.ring[i] == nil || r.ring[i].Rect.Dx() != ow || r.ring[i].Rect.Dy() != oh {
		r.ring[i] = image.NewRGBA(image.Rect(0, 0, ow, oh))
	}
	img := r.ring[i]
	got, err := r.dec.decode(pic, img)
	if err != nil || !got {
		return false, err
	}
	r.ri = (i + 1) % len(r.ring)
	r.fmu.Lock()
	r.frame, r.size = img, image.Pt(ow, oh)
	r.fmu.Unlock()
	return true, nil
}

// SetView tells the size, in pixels, the video is shown at; the GUI sets it
// each frame it draws the video.
func (r *VideoReceiver) SetView(width, height int) {
	r.view.Store(uint64(max(width, 0))<<32 | uint64(max(height, 0)))
	r.viewAt.Store(time.Now().UnixNano())
}

// Shown reports whether the GUI drew the video lately.
func (r *VideoReceiver) Shown() bool {
	at := r.viewAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < viewHidden
}

// Pixels is how many pixels the video is shown in, 0 if not shown.
func (r *VideoReceiver) Pixels() int {
	if !r.Shown() {
		return 0
	}
	v := r.view.Load()
	return int(v>>32) * int(v&0xFFFFFFFF)
}

// Frame is the latest picture read back, or nil when there is none or it is
// a SharedFrame.
func (r *VideoReceiver) Frame() *image.RGBA {
	r.fmu.Lock()
	defer r.fmu.Unlock()
	r.taken = r.frame
	return r.frame
}

// SharedFrame is the latest picture as a texture on the graphics card, or
// nil when pictures come through Frame.
func (r *VideoReceiver) SharedFrame() *SharedFrame {
	r.fmu.Lock()
	defer r.fmu.Unlock()
	return r.current
}

// Info is the video's size, 0 before it is known, and how many pictures
// were decoded.
func (r *VideoReceiver) Info() (width, height int, frames int64) {
	r.fmu.Lock()
	defer r.fmu.Unlock()
	return r.size.X, r.size.Y, r.frames.Load()
}

// Unavailable reports whether the video cannot be decoded here.
func (r *VideoReceiver) Unavailable() bool { return r.unavailable.Load() }

// Stats are the packets received, pictures lost and pictures that could
// not be decrypted.
func (r *VideoReceiver) Stats() (packets, lost, daveFailed int64) {
	return r.packets.Load(), r.lost.Load(), r.daveFailed.Load()
}

// Closed reports whether Close was called.
func (r *VideoReceiver) Closed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// Close stops receiving and frees the decoder.
func (r *VideoReceiver) Close() {
	r.once.Do(func() {
		close(r.done)
		r.wg.Wait()
		r.closeDecoder()
		r.fmu.Lock()
		r.frame, r.taken = nil, nil
		r.fmu.Unlock()
	})
}
