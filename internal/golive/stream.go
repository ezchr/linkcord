//go:build windows && !no_voice

package golive

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/thomas-vilte/dave-go/frame"
	"image"
	"log/slog"
	"math/big"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/thomas-vilte/dave-go/session"
	"github.com/tphakala/go-opus/opus"
)

// Stream is a screen or a window being shared.
type Stream struct {
	p    Params
	key  string
	log  *slog.Logger
	conn *conn
	ops  <-chan ws.Op
	med  *media
	rm   []func()

	cancel  context.CancelFunc
	done    chan struct{}
	stopped atomic.Bool
	preview atomic.Pointer[image.RGBA]
	// previewW is how wide the preview is shown, in pixels, for it to be
	// made as wide.
	previewW atomic.Int32
	// keyframe asks the loop for a keyframe, and source to share another
	// screen or window.
	keyframe chan struct{}
	source   chan *capturer

	mu      sync.Mutex
	viewers []discord.UserID
	// connected are who connected to the stream's server: its viewers.
	connected map[discord.UserID]bool
	// soundStop stops sending the sound, if it is sent.
	soundStop func()

	// seen are the kinds of events of the stream's gateway logged.
	seen map[string]bool
}

// Start starts streaming the source of p.
func Start(ctx context.Context, p Params) (*Stream, error) {
	codec, err := codecPath(ctx, p.CodecDir)
	if err != nil {
		return nil, err
	}
	if err := loadCodec(codec); err != nil {
		return nil, err
	}

	s := &Stream{
		p:         p,
		key:       StreamKey(p.GuildID, p.ChannelID, p.UserID),
		log:       slog.Default().With("stream", StreamKey(p.GuildID, p.ChannelID, p.UserID)),
		done:      make(chan struct{}),
		keyframe:  make(chan struct{}, 1),
		source:    make(chan *capturer, 1),
		seen:      make(map[string]bool),
		connected: make(map[discord.UserID]bool),
	}
	ok := false
	defer func() {
		if !ok {
			s.teardown()
		}
	}()

	created := make(chan *StreamCreateEvent, 1)
	server := make(chan *StreamServerUpdateEvent, 1)
	s.rm = append(s.rm,
		p.Subscribe(func(ev *StreamCreateEvent) {
			if ev.StreamKey == s.key {
				select {
				case created <- ev:
				default:
				}
			}
		}),
		p.Subscribe(func(ev *StreamServerUpdateEvent) {
			if ev.StreamKey == s.key {
				select {
				case server <- ev:
				default:
				}
			}
		}),
		p.Subscribe(func(ev *StreamUpdateEvent) {
			if ev.StreamKey == s.key {
				s.mu.Lock()
				s.viewers = slices.Clone(ev.ViewerIDs)
				s.mu.Unlock()
			}
		}),
		p.Subscribe(func(ev *StreamDeleteEvent) {
			if ev.StreamKey == s.key {
				reason := ev.Reason
				if reason == "" {
					reason = "the stream was ended"
				}
				s.end(errors.New(reason))
			}
		}),
	)

	create := &streamCreateCommand{Type: "guild", ChannelID: p.ChannelID}
	if p.GuildID.IsValid() {
		create.GuildID = &p.GuildID
	} else {
		create.Type = "call"
	}
	if err := p.Main.SendGateway(ctx, create); err != nil {
		return nil, fmt.Errorf("golive: could not ask for a stream: %w", err)
	}
	if err := p.Main.SendGateway(ctx, &streamSetPausedCommand{StreamKey: s.key}); err != nil {
		return nil, err
	}

	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var ce *StreamCreateEvent
	var se *StreamServerUpdateEvent
	for ce == nil || se == nil {
		select {
		case ce = <-created:
		case se = <-server:
		case <-wait.Done():
			return nil, errors.New("golive: Discord did not give a stream server")
		}
	}

	// The end-to-end encryption of a stream is keyed to its RTC channel,
	// which older servers did not give: it was then the server's ID less one.
	daveChannel := new(big.Int)
	if _, ok := daveChannel.SetString(ce.RTCChannelID, 10); !ok {
		daveChannel.SetString(ce.RTCServerID, 10)
		daveChannel.Sub(daveChannel, big.NewInt(1))
	}
	s.log.Info("stream created", "rtc_server", ce.RTCServerID, "rtc_channel", ce.RTCChannelID, "dave_channel", daveChannel.String())

	loopCtx, loopCancel := context.WithCancel(context.Background())
	s.cancel = loopCancel
	s.conn, s.ops = dial(loopCtx, se.Endpoint, identifyCommand{
		ServerID:  ce.RTCServerID,
		ChannelID: daveChannel.String(),
		UserID:    p.UserID,
		SessionID: p.SessionID,
		Token:     se.Token,
		Video:     true,
		Streams:   []streamInfo{{Type: "screen", RID: "100", Quality: 100}},
	}, daveChannel.Uint64(), s.log)

	ready, err := s.waitFor(wait, func(ev ws.Event) bool { _, ok := ev.(*readyEvent); return ok })
	if err != nil {
		return nil, fmt.Errorf("golive: the stream server did not answer: %w", err)
	}
	r := ready.(*readyEvent)
	if len(r.Streams) == 0 {
		return nil, errors.New("golive: the stream server takes no video")
	}
	mode := slices.IndexFunc(modes, func(m string) bool { return slices.Contains(r.Modes, m) })
	if mode < 0 {
		return nil, fmt.Errorf("golive: no encryption mode in common with the stream server (%v)", r.Modes)
	}
	var ip string
	var port uint16
	s.med, ip, port, err = dialMedia(wait, fmt.Sprintf("%s:%d", r.IP, r.Port), r.SSRC)
	if s.med != nil {
		s.med.linkcordOnly = p.LinkcordOnly
	}
	if err != nil {
		return nil, err
	}
	s.med.mu.Lock()
	s.med.ssrc = r.Streams[0].SSRC
	s.med.rtxSSRC = r.Streams[0].RTXSSRC
	s.med.audioSSRC = r.SSRC
	s.med.mu.Unlock()
	// As Discord's clients do: the server's version, and the stream told
	// before it is active.
	s.conn.send(wait, &versionCommand{})
	if err := s.conn.send(wait, s.videoInactive()); err != nil {
		return nil, err
	}
	if err := s.conn.send(wait, &selectProtocolCommand{
		Protocol: "udp",
		Codecs: []codecInfo{
			{Name: "opus", Type: "audio", Priority: 1000, PayloadType: payloadOpus},
			{Name: "H264", Type: "video", Priority: 2000, PayloadType: payloadH264, RTXPayloadType: payloadH264RTX, Encode: true, Decode: true},
		},
		Data:            voicegateway.SelectProtocolData{Address: ip, Port: port, Mode: modes[mode]},
		Address:         ip,
		Port:            port,
		Mode:            modes[mode],
		RTCConnectionID: newUUID(),
		Experiments:     []string{"fixed_keyframe_interval"},
	}); err != nil {
		return nil, err
	}
	desc, err := s.waitFor(wait, func(ev ws.Event) bool { _, ok := ev.(*sessionDescriptionEvent); return ok })
	if err != nil {
		return nil, fmt.Errorf("golive: the stream server did not give a key: %w", err)
	}
	d := desc.(*sessionDescriptionEvent)
	s.log.Info("stream server ready", "mode", d.Mode, "audio_codec", d.AudioCodec, "video_codec", d.VideoCodec,
		"dave", d.DAVEProtocolVersion, "ssrc", r.SSRC, "video_ssrc", r.Streams[0].SSRC, "rtx_ssrc", r.Streams[0].RTXSSRC, "streams", len(r.Streams), "modes", r.Modes)
	if err := s.med.useSecret(d.Mode, d.SecretKey); err != nil {
		return nil, err
	}

	capt := newCapturer(p.Source, true)
	first, okGrab, gone := capt.grab()
	if gone {
		capt.close()
		return nil, ErrSourceGone
	}
	w, h := p.Source.W, p.Source.H
	if okGrab {
		w, h = first.w, first.h
	}
	ow, oh := p.Quality.size(w, h)
	s.conn.send(wait, &voicegateway.SpeakingEvent{Speaking: voicegateway.Soundshare, SSRC: r.SSRC})
	if err := s.conn.send(wait, s.video(ow, oh)); err != nil {
		capt.close()
		return nil, err
	}

	s.log.Info("stream capturing", "with", capt.method())
	ok = true
	go s.watch()
	go s.loop(capt)
	if p.Sound {
		s.SetSound(true)
	}
	return s, nil
}

// video is what the stream sends, w by h.
func (s *Stream) video(w, h int) *videoCommand {
	r := s.conn.ready
	st := r.Streams[0]
	return &videoCommand{
		AudioSSRC: r.SSRC,
		VideoSSRC: st.SSRC,
		RTXSSRC:   st.RTXSSRC,
		Streams: []videoStream{{
			Type: "video", RID: "100", SSRC: st.SSRC, Active: true, Quality: 100, RTXSSRC: st.RTXSSRC,
			MaxBitrate:    s.p.Quality.bitrate(w, h) * 3 / 2,
			MaxFramerate:  s.p.Quality.FPS,
			MaxResolution: maxResolution{Type: "fixed", Width: w, Height: h},
		}},
	}
}

// videoInactive tells the server of the stream before it sends anything.
func (s *Stream) videoInactive() *videoCommand {
	v := s.video(0, 0)
	v.Streams[0].Active = false
	v.Streams[0].MaxBitrate, v.Streams[0].MaxFramerate = 0, 0
	return v
}

// newUUID returns a random UUID, which names a connection.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// waitFor waits for the gateway to send an event that match accepts.
func (s *Stream) waitFor(ctx context.Context, match func(ws.Event) bool) (ws.Event, error) {
	for {
		select {
		case op, ok := <-s.ops:
			if !ok {
				return nil, s.conn.gw.LastError()
			}
			if ce, ok := op.Data.(*ws.CloseEvent); ok {
				return nil, ce
			}
			// Why the stream server does not answer, if it does not.
			if be, ok := op.Data.(*ws.BackgroundErrorEvent); ok {
				s.log.Warn("stream server error", "err", be.Err)
			}
			if match(op.Data) {
				return op.Data, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// watch follows the stream's gateway: new viewers and new keys need a
// keyframe to start from, and a closed gateway ends the stream.
func (s *Stream) watch() {
	for {
		select {
		case op, ok := <-s.ops:
			if !ok {
				s.end(errors.New("the connection to the stream server was lost"))
				return
			}
			if op.Data != nil {
				if t := fmt.Sprintf("%T", op.Data); !s.seen[t] {
					s.seen[t] = true
					s.log.Info("stream gateway event", "op", op.Code, "type", t)
				}
			}
			switch data := op.Data.(type) {
			case *ws.CloseEvent:
				s.log.Warn("stream gateway closed", "err", data)
			case *voicegateway.ClientsConnectEvent:
				s.mu.Lock()
				for _, id := range data.UserIDs {
					if id != s.p.UserID {
						s.connected[id] = true
					}
				}
				s.mu.Unlock()
				s.requestKeyframe()
			case *voicegateway.ClientDisconnectEvent:
				s.mu.Lock()
				delete(s.connected, data.UserID)
				s.mu.Unlock()
			case *voicegateway.DAVEExecuteTransitionEvent:
				s.requestKeyframe()
			}
		case <-s.med.keyframe:
			s.requestKeyframe()
		case <-s.done:
			return
		}
	}
}

func (s *Stream) requestKeyframe() {
	select {
	case s.keyframe <- struct{}{}:
	default:
	}
}

// keyframeEvery is the longest a stream goes without a keyframe. Viewers
// that join, lose a picture or get new keys ask for one and get it at once,
// so, as Discord's own client, this is only a safety net: keyframes are many
// times the size of other pictures, and fewer of them leave more of the
// bitrate to the picture.
const keyframeEvery = 30 * time.Second

// streamFrame is a picture handed from the capture thread to the encoder
// thread: in buf, converted by the processor, or in tex, converted by the
// graphics card; neither to send the last picture again.
type streamFrame struct {
	// buf is the picture, w by h, in NV12 if nv12 or else I420.
	buf  []byte
	w, h int
	nv12 bool
	// tex is the NV12 texture of the device dev, each with a reference the
	// frame holds until the encoder is done with it.
	tex, dev unsafe.Pointer
	elapsed  time.Duration
	// idr asks for a keyframe.
	idr bool
}

// empty reports whether the frame is a request to send the last picture again.
func (f streamFrame) empty() bool { return f.buf == nil && f.tex == nil }

// encodeStats are counted by the encoder thread and logged by the capture
// loop.
type encodeStats struct {
	frames, idrs, failed, bytes, encodeNs atomic.Int64
	name                                  atomic.Pointer[string]
}

// loop captures pictures until the stream stops and hands them to
// encodeLoop, on a thread of its own: capturing and converting the next
// picture runs while the last one is encoded and sent. From Windows 8 the
// graphics card captures, converts and encodes (gpuCapture); otherwise the
// processor converts, each picture split across the cores.
func (s *Stream) loop(capt *capturer) {
	jobs := make(chan streamFrame, 1)
	free := make(chan []byte, 4)
	var st encodeStats
	var wantNV12 atomic.Bool
	// gpuOK is cleared by the encoder thread when the graphics card cannot
	// encode its own textures: the processor converts from then on.
	var gpuOK atomic.Bool
	gpuOK.Store(true)
	var gpu *gpuCapture
	openGPU := func() {
		if gpu != nil {
			gpu.close()
			gpu = nil
		}
		if gpuOK.Load() {
			// The screen is duplicated once per program: the processor's
			// capture lets go of it, and takes it again only if needed.
			capt.release()
			gpu = newGPUCapture(capt.src, true)
		}
	}
	openGPU()
	// The preview is made in turn in a few pictures rather than a new one
	// each time: ten a second as wide as the window are 30 MB a second for
	// the collector, whose pauses break the sound. The GUI shows the
	// latest; the ones before it are free.
	var previews [3]*image.RGBA
	previewAt := 0
	nextPreview := func(w, h int) *image.RGBA {
		img := previews[previewAt]
		if img == nil || img.Rect.Dx() != w || img.Rect.Dy() != h {
			img = image.NewRGBA(image.Rect(0, 0, w, h))
			previews[previewAt] = img
		}
		previewAt = (previewAt + 1) % len(previews)
		return img
	}
	encDone := make(chan struct{})
	go func() {
		defer close(encDone)
		s.encodeLoop(jobs, free, &st, &wantNV12, &gpuOK)
	}()
	defer func() {
		close(jobs)
		<-encDone
		if gpu != nil {
			gpu.close()
		}
		capt.close()
		// One handed over as the stream ended is not shared.
		select {
		case next := <-s.source:
			next.close()
		default:
		}
	}()
	fps := max(s.p.Quality.FPS, 1)
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	report := time.NewTicker(time.Second)
	defer report.Stop()

	stats := time.NewTicker(5 * time.Second)
	defer stats.Stop()
	var empty, grabs int
	var grabTime, convertTime time.Duration
	var (
		ow, oh     int
		srcW, srcH int
		start      = time.Now()
		lastThumb  time.Time
		// lastFrame is when a picture was last handed over, haveLast whether
		// the encoder has one to send again, wantFrame whether a viewer
		// asked for one and idr whether as a keyframe.
		lastFrame                time.Time
		haveLast, wantFrame, idr bool
	)
	// buffer returns a picture buffer of n bytes, one the encoder is done
	// with if there is one: at most four are ever in use.
	buffer := func(n int) []byte {
		select {
		case b := <-free:
			if cap(b) >= n {
				return b[:n]
			}
		default:
		}
		return make([]byte, n)
	}
	// resize follows the size of what is captured.
	resize := func(w, h int) {
		if w != srcW || h != srcH {
			srcW, srcH = w, h
			ow, oh = s.p.Quality.size(srcW, srcH)
		}
	}
	method := func() string {
		if gpu != nil {
			return "desktop duplication, converted by the graphics card"
		}
		return capt.method()
	}
	for {
		select {
		case <-s.done:
			return
		case <-report.C:
			if err := s.med.sendReport(); err != nil {
				s.log.Debug("could not send a sender report", "err", err)
			}
			continue
		case <-stats.C:
			ds := s.conn.dave.Stats()
			dst := s.conn.dave.State()
			frames := st.frames.Swap(0)
			encodeNs := st.encodeNs.Swap(0)
			name := ""
			if n := st.name.Load(); n != nil {
				name = *n
			}
			s.log.Info("stream stats", "frames", frames, "idr", st.idrs.Swap(0), "kbytes", st.bytes.Swap(0)/1024, "no_picture", empty, "failed", st.failed.Swap(0),
				"encoder", name,
				"encode_avg", (time.Duration(encodeNs) / time.Duration(max(frames, 1))).Round(100*time.Microsecond),
				"grab_avg", (grabTime / time.Duration(max(grabs, 1))).Round(100*time.Microsecond),
				"convert_avg", (convertTime / time.Duration(max(grabs, 1))).Round(100*time.Microsecond),
				"with", method(),
				"size", fmt.Sprintf("%dx%d", ow, oh),
				"dave_ready", dst.Ready, "dave_epoch", dst.EpochID, "dave_version", dst.ProtocolVersion,
				"passthrough", ds.PassthroughFrames, "encrypt_failures", ds.EncryptFailures,
				"rtcp_in", s.med.rtcpSummary(), "packets", s.med.packetCount())
			empty, grabs, grabTime, convertTime = 0, 0, 0, 0
			continue
		case next := <-s.source:
			capt.close()
			capt = next
			openGPU()
			s.log.Info("stream capturing", "with", method())
			// The new picture starts afresh, with a keyframe, and at its own
			// size if that differs, which a new encoder takes care of.
			haveLast, wantFrame, idr = false, true, true
			continue
		case <-s.keyframe:
			// Sent at once, even if the screen did not change: a viewer
			// waits for it.
			wantFrame, idr = true, true
			continue
		case <-tick.C:
		}

		if gpu != nil && !gpuOK.Load() {
			s.log.Info("the graphics card cannot encode its own pictures; the processor converts them")
			gpu.close()
			gpu = nil
		}
		var job streamFrame
		// picked: this tick's capture is done, with a new picture in job or
		// with none.
		picked, fresh := false, false
		if gpu != nil {
			g0 := time.Now()
			w, h, status, changed, gone := gpu.grab()
			grabTime += time.Since(g0)
			grabs++
			if gone {
				s.end(ErrSourceGone)
				return
			}
			switch status {
			case gpuNever:
				s.log.Info("the graphics card cannot capture this; the processor does")
				gpu.close()
				gpu = nil
			case gpuNone:
				picked = true
			case gpuPicture:
				picked = true
				// The same picture as the last is not converted or encoded
				// again.
				if changed || !haveLast || w != srcW || h != srcH {
					resize(w, h)
					c0 := time.Now()
					tex, dev, err := gpu.convert(ow, oh)
					convertTime += time.Since(c0)
					if err != nil {
						s.log.Info("the graphics card cannot convert this; the processor does", "err", err)
						gpu.close()
						gpu = nil
						picked = false
						break
					}
					job = streamFrame{tex: tex, dev: dev, w: ow, h: oh, nv12: true}
					fresh = true
					if now := time.Now(); now.Sub(lastThumb) >= previewEvery {
						lastThumb = now
						tw := min(int(s.previewW.Load()), w)
						if tw <= 0 {
							tw = 320
						}
						if img := nextPreview(tw, max(tw*h/w, 1)); gpu.previewInto(img) {
							s.preview.Store(img)
						}
					}
				}
			}
			// gpuThisCPU: the processor captures this one picture.
		}
		if !picked {
			g0 := time.Now()
			pic, ok, gone := capt.grab()
			grabTime += time.Since(g0)
			grabs++
			if gone {
				s.end(ErrSourceGone)
				return
			}
			// The same picture as the last, which a still screen gives sixty
			// times a second, is neither converted nor encoded again.
			if ok && (pic.changed || !haveLast || pic.w != srcW || pic.h != srcH) {
				resize(pic.w, pic.h)
				c0 := time.Now()
				nv12 := wantNV12.Load()
				job = streamFrame{buf: buffer(ow * oh * 3 / 2), w: ow, h: oh, nv12: nv12}
				pic.toYUV(job.buf, ow, oh, nv12)
				convertTime += time.Since(c0)
				fresh = true
				if now := time.Now(); now.Sub(lastThumb) >= previewEvery {
					lastThumb = now
					tw := min(int(s.previewW.Load()), pic.w)
					if tw <= 0 {
						tw = 320
					}
					img := nextPreview(tw, max(tw*pic.h/pic.w, 1))
					pic.intoRGBA(img)
					s.preview.Store(img)
				}
			}
		}
		if fresh {
			haveLast = true
		} else {
			empty++
			// Nothing changed on the screen, so the capture has nothing new.
			// The last picture is sent again when a keyframe is asked for,
			// and every second, so that a viewer who joins a still screen
			// sees it at once.
			if !haveLast || !wantFrame && time.Since(lastFrame) < time.Second {
				continue
			}
		}
		job.elapsed, job.idr = time.Since(start), idr
		wantFrame, idr, lastFrame = false, false, time.Now()
		select {
		case jobs <- job:
		case <-s.done:
			releaseCOM(job.tex)
			releaseCOM(job.dev)
			return
		}
	}
}

// encodeLoop encodes, encrypts and sends the pictures of jobs until it is
// closed, on one OS thread, which the graphics card's encoder needs. Each
// buffer it is done with goes to free.
func (s *Stream) encodeLoop(jobs <-chan streamFrame, free chan<- []byte, st *encodeStats, wantNV12, gpuOK *atomic.Bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fps := max(s.p.Quality.FPS, 1)
	var (
		enc     *encoder
		last    streamFrame
		scratch []byte
	)
	// done lets go of what a frame holds.
	done := func(f streamFrame) {
		if f.buf != nil {
			select {
			case free <- f.buf:
			default:
			}
		}
		releaseCOM(f.tex)
		releaseCOM(f.dev)
	}
	defer func() {
		done(last)
		if enc != nil {
			enc.close()
		}
	}()
	idrEvery := fps * int(keyframeEvery/time.Second)
	for job := range jobs {
		if s.stopped.Load() {
			done(job)
			continue
		}
		if job.empty() {
			if last.empty() {
				continue
			}
			// The last picture again; last keeps what it holds.
			job.buf, job.tex, job.dev, job.w, job.h, job.nv12 = last.buf, last.tex, last.dev, last.w, last.h, last.nv12
		} else {
			done(last)
			last = job
		}
		onGPU := job.tex != nil
		// A new size gets a new encoder, which starts with a keyframe; so
		// does a texture of another device, as after a change of resolution.
		if enc == nil || enc.w != job.w || enc.h != job.h || onGPU && !enc.textures(job.dev) {
			resized := enc != nil && (enc.w != job.w || enc.h != job.h)
			if enc != nil {
				enc.close()
				enc = nil
			}
			bitrate := s.p.Quality.bitrate(job.w, job.h)
			if onGPU {
				if enc = newEncoderOn(addRefCOM(job.dev), job.w, job.h, fps, bitrate, idrEvery); enc == nil {
					// The capture goes back to the processor; this picture,
					// on the graphics card only, is not sent.
					gpuOK.Store(false)
					last = streamFrame{}
					done(job)
					continue
				}
			} else {
				var err error
				if enc, err = newEncoder(job.w, job.h, fps, bitrate, idrEvery); err != nil {
					enc = nil
					s.end(err)
					return
				}
			}
			if resized {
				s.conn.send(context.Background(), s.video(job.w, job.h))
			}
			wantNV12.Store(enc.nv12())
			name := enc.name
			st.name.Store(&name)
			s.log.Info("stream encoder", "encoder", enc.name, "hardware", enc.hardware(), "size", fmt.Sprintf("%dx%d", job.w, job.h), "fps", fps)
		}
		if job.idr {
			enc.forceIDR()
		}
		var (
			frame []byte
			tsMs  int64
			idr   bool
			err   error
		)
		t0 := time.Now()
		if onGPU {
			frame, tsMs, idr, err = enc.encodeTexture(job.tex, job.elapsed.Milliseconds())
		} else {
			pic := job.buf
			if job.nv12 != enc.nv12() {
				// Converted before the capture knew the encoder's layout.
				scratch = slices.Grow(scratch[:0], len(pic))[:len(pic)]
				if enc.nv12() {
					i420ToNV12(scratch, pic, job.w, job.h)
				} else {
					nv12ToI420(scratch, pic, job.w, job.h)
				}
				pic = scratch
			}
			frame, tsMs, idr, err = enc.encode(pic, job.elapsed.Milliseconds())
		}
		st.encodeNs.Add(int64(time.Since(t0)))
		if err != nil {
			st.failed.Add(1)
			if enc.hardware() {
				// The next picture goes to OpenH264, which always works, and
				// is converted by the processor for it.
				s.log.Warn("the graphics card's encoder failed; OpenH264 takes over", "encoder", enc.name, "err", err)
				hardwareFailed.Store(true)
				gpuOK.Store(false)
				enc.close()
				enc = nil
				wantNV12.Store(false)
			} else {
				s.log.Warn("could not encode a picture", "err", err)
			}
			continue
		}
		if idr {
			st.idrs.Add(1)
		}
		if frame == nil {
			continue
		}
		out, err := s.encrypt(frame)
		if errors.Is(err, session.ErrStartCode) {
			enc.forceIDR()
			continue
		}
		if err != nil {
			s.log.Warn("could not encrypt a picture", "err", err)
			continue
		}
		if out == nil {
			continue
		}
		st.frames.Add(1)
		st.bytes.Add(int64(len(out)))
		if err := s.med.sendFrame(out, uint32(tsMs*90)); err != nil {
			s.end(fmt.Errorf("could not send the stream: %w", err))
			return
		}
	}
}

// previewEvery is how often the preview is made again: often enough to
// look moving, as a small part of the work of the stream.
const previewEvery = 100 * time.Millisecond

// Preview is a recent picture of what is shared, or nil.
func (s *Stream) Preview() *image.RGBA {
	return s.preview.Load()
}

// SetPreviewWidth makes the preview w pixels wide, as wide as it is shown,
// or as what is shared if narrower.
func (s *Stream) SetPreviewWidth(w int) {
	s.previewW.Store(int32(w))
}

// Viewers are who watch the stream.
func (s *Stream) Viewers() []discord.UserID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.viewers)
	for id := range s.connected {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	out = slices.DeleteFunc(out, func(id discord.UserID) bool { return id == s.p.UserID })
	slices.Sort(out)
	return out
}

// Sound reports whether the sound of the source is sent.
func (s *Stream) Sound() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.soundStop != nil
}

// SetSound sends the sound of the source along, or stops.
func (s *Stream) SetSound(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on == (s.soundStop != nil) || s.stopped.Load() {
		return
	}
	if !on {
		s.soundStop()
		s.soundStop = nil
		return
	}
	pcm := make(chan []int16, 64)
	how, stopCapture, err := startSound(s.p.Source, pcm)
	if err != nil {
		s.log.Warn("could not capture the sound", "err", err)
		return
	}
	s.log.Info("sending the sound", "of", how)
	quit := make(chan struct{})
	go s.soundLoop(pcm, quit)
	s.soundStop = func() {
		stopCapture()
		close(quit)
	}
}

// soundLoop encodes the sound captured to Opus, 20 ms at a time, encrypts
// it and sends it.
func (s *Stream) soundLoop(pcm <-chan []int16, quit <-chan struct{}) {
	const frame = 960 * 2 // 20 ms of stereo at 48 kHz
	enc, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: 48000, Channels: 2, Bitrate: 128000})
	if err != nil {
		s.log.Warn("could not make the sound encoder", "err", err)
		return
	}
	var (
		buf    []int16
		packet = make([]byte, 4000)
		out    = make([]byte, s.conn.dave.MaxEncryptedFrameSize(4000))
		ts     uint32
		last   time.Time
	)
	for {
		select {
		case <-quit:
			return
		case <-s.done:
			return
		case chunk := <-pcm:
			buf = append(buf, chunk...)
		}
		for len(buf) >= frame {
			// Silence sends nothing, and the time it lasted moves the
			// timestamps on, for the sound after it not to come late.
			now := time.Now()
			marker := last.IsZero()
			if gap := now.Sub(last); !last.IsZero() && gap > 100*time.Millisecond {
				ts += uint32(gap*48000/time.Second) / 960 * 960
				marker = true
			}
			last = now
			n, err := enc.Encode(buf[:frame], packet)
			buf = buf[frame:]
			if err != nil {
				continue
			}
			m, err := s.conn.dave.Encrypt(s.med.audioSSRC, packet[:n], out)
			if err != nil {
				continue
			}
			if err := s.med.sendAudio(out[:m], ts, marker); err != nil {
				return
			}
			ts += 960
		}
		// Keep what is left from growing if sending falls behind.
		if len(buf) > 10*frame {
			buf = buf[len(buf)-frame:]
		}
	}
}

// end stops the stream for err, telling OnEnd.
func (s *Stream) end(err error) {
	if s.stopped.Swap(true) {
		return
	}
	s.log.Info("stream ended", "err", err)
	go func() {
		s.teardown()
		if s.p.OnEnd != nil {
			s.p.OnEnd(err)
		}
	}()
}

// Stop ends the stream.
func (s *Stream) Stop() {
	if s.stopped.Swap(true) {
		return
	}
	s.teardown()
}

// teardown tells Discord the stream ended and closes its connections.
func (s *Stream) teardown() {
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}
	for _, rm := range s.rm {
		rm()
	}
	s.mu.Lock()
	if s.soundStop != nil {
		s.soundStop()
		s.soundStop = nil
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.p.Main.SendGateway(ctx, &streamDeleteCommand{StreamKey: s.key}); err != nil {
		s.log.Warn("could not end the stream", "err", err)
	}
	if s.med != nil {
		s.med.close()
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// encrypt encrypts a picture end to end, veiled for a stream for Linkcord
// only.
func (s *Stream) encrypt(pic []byte) ([]byte, error) {
	for range 8 {
		out, err := s.conn.dave.EncryptH264(pic)
		if err != nil || !s.p.LinkcordOnly || veil(out) {
			return out, err
		}
		// Not encrypted end to end yet, so not to be veiled either: not
		// sent, as Discord's clients would show it.
		if !frame.LooksLikeDAVEFrame(out) {
			return nil, nil
		}
	}
	return nil, errors.New("golive: could not veil a picture")
}

// SetSource shares src in place of what is shared, without ending the
// stream, and moves the sound along with it.
func (s *Stream) SetSource(src Source) error {
	if s.stopped.Load() {
		return errors.New("golive: the stream ended")
	}
	capt := newCapturer(src, true)
	if _, _, gone := capt.grab(); gone {
		capt.close()
		return ErrSourceGone
	}
	select {
	case old := <-s.source:
		old.close()
	default:
	}
	s.source <- capt
	s.mu.Lock()
	sound := s.soundStop != nil
	s.p.Source = src
	s.mu.Unlock()
	if sound {
		s.SetSound(false)
		s.SetSound(true)
	}
	return nil
}
