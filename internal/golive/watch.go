//go:build windows && !no_voice

package golive

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/frame"
	"golang.org/x/crypto/chacha20poly1305"
)

// WatchParams is what watching someone's stream needs.
type WatchParams struct {
	Main      Main
	Subscribe func(handler any) (rm func())
	// GuildID and ChannelID are the voice channel of the stream; GuildID is
	// not valid for a call. StreamerID shares; UserID watches.
	GuildID    discord.GuildID
	ChannelID  discord.ChannelID
	StreamerID discord.UserID
	UserID     discord.UserID
	SessionID  string
	// FFmpeg decodes the video.
	FFmpeg string
	// OnFrame is called, from another goroutine, when a picture is ready.
	OnFrame func()
	// OnEnd is called once the stream ended, with why.
	OnEnd func(err error)
}

// Watch is someone's stream being watched.
type Watch struct {
	p      WatchParams
	key    string
	log    *slog.Logger
	cancel context.CancelFunc
	rm     []func()
	conn   *conn
	ops    <-chan ws.Op
	udp    net.Conn
	aead   cipher.AEAD

	mu    sync.Mutex
	nonce [24]byte // our counter, for the RTCP we send
	ourSSRC,
	videoSSRC uint32
	frame  *image.RGBA
	frames atomic.Int64
	// The counts of how the stream goes, which are logged.
	packets, assembled, lost, daveFailed, skipped atomic.Int64
	padded                                        atomic.Int64
	videoBytes                                    atomic.Int64
	reception                                     watchReception // guarded by mu
	needIDR                                       atomic.Bool
	lastPLI                                       atomic.Int64
	srcW, srcH, fps                               int
	created                                       bool
	once                                          sync.Once
	decoder                                       *exec.Cmd
	decIn                                         io.WriteCloser
	done                                          chan struct{}
}

// streamWatchCommand is Op 20: it asks to watch a stream.
type streamWatchCommand struct {
	StreamKey string `json:"stream_key"`
}

func (*streamWatchCommand) Op() ws.OpCode           { return 20 }
func (*streamWatchCommand) EventType() ws.EventType { return "" }

// mediaSinkWantsCommand is Op 15: what quality of whose video the client
// wants, by the SSRC of each video, and "any" for the rest; 100 asks for
// the best.
type mediaSinkWantsCommand map[string]any

func (*mediaSinkWantsCommand) Op() ws.OpCode           { return 15 }
func (*mediaSinkWantsCommand) EventType() ws.EventType { return "" }

// videoEvent is Op 12 from the server: the SSRCs someone sends video with.
type videoEvent struct {
	UserID    discord.UserID `json:"user_id"`
	AudioSSRC uint32         `json:"audio_ssrc"`
	VideoSSRC uint32         `json:"video_ssrc"`
	Streams   []videoStream  `json:"streams"`
}

func (*videoEvent) Op() ws.OpCode           { return 12 }
func (*videoEvent) EventType() ws.EventType { return "" }

func init() {
	unmarshalers.Add(func() ws.Event { return new(videoEvent) })
}

// joinVariant is a way of joining a stream server as a viewer. How viewers
// must join is not documented, and a server ends a join it does not take
// (4006, the session is no longer valid) without saying what: each is tried
// in turn, with a new token, and the log tells which one is taken.
type joinVariant struct {
	name string
	// video and streams are said in the identify.
	video   bool
	streams []streamInfo
}

var joinVariants = []joinVariant{
	// As the Userdoccers describe a viewer: offering no stream of its own.
	{name: "viewer", video: true},
	{name: "viewer-screen", video: true, streams: []streamInfo{{Type: "screen", RID: "100", Quality: 100}}},
	{name: "no-video"},
}

// StartWatching starts watching the stream of p.StreamerID.
func StartWatching(ctx context.Context, p WatchParams) (*Watch, error) {
	var errs []error
	key := StreamKey(p.GuildID, p.ChannelID, p.StreamerID)
	// Leaving the stream: after a refused join Discord still counts the user
	// as watching, and answers no new ask to watch until they leave.
	leave := func() {
		p.Main.SendGateway(context.Background(), &streamDeleteCommand{StreamKey: key})
	}
	for i, v := range joinVariants {
		if i > 0 {
			leave()
			select {
			case <-time.After(1500 * time.Millisecond):
			case <-ctx.Done():
				return nil, errors.Join(append(errs, ctx.Err())...)
			}
		}
		w, err := startWatching(ctx, p, v)
		if err == nil {
			w.log.Info("joined the stream", "variant", v.name)
			return w, nil
		}
		slog.Info("could not join the stream", "variant", v.name, "err", err)
		errs = append(errs, fmt.Errorf("%s: %w", v.name, err))
		// Only a refused join is worth another way; the rest would fail the
		// same.
		if !errors.Is(err, errJoinRefused) || ctx.Err() != nil {
			break
		}
	}
	leave()
	return nil, errors.Join(errs...)
}

// errJoinRefused is when the stream server ends the session while joining.
var errJoinRefused = errors.New("the stream server refused to join")

func startWatching(ctx context.Context, p WatchParams, v joinVariant) (*Watch, error) {
	w := &Watch{
		p:    p,
		key:  StreamKey(p.GuildID, p.ChannelID, p.StreamerID),
		done: make(chan struct{}),
	}
	w.log = slog.Default().With("watch", w.key)
	ok := false
	defer func() {
		if !ok {
			w.teardown()
		}
	}()

	created := make(chan *StreamCreateEvent, 1)
	server := make(chan *StreamServerUpdateEvent, 1)
	notFound := make(chan string, 1)
	w.rm = append(w.rm,
		p.Subscribe(func(ev *StreamCreateEvent) {
			if ev.StreamKey == w.key {
				w.log.Info("stream create", "rtc_server", ev.RTCServerID, "rtc_channel", ev.RTCChannelID, "paused", ev.Paused, "viewers", len(ev.ViewerIDs))
				w.mu.Lock()
				w.created = true
				w.mu.Unlock()
				select {
				case created <- ev:
				default:
				}
			}
		}),
		p.Subscribe(func(ev *StreamServerUpdateEvent) {
			if ev.StreamKey == w.key {
				select {
				case server <- ev:
				default:
				}
			}
		}),
		p.Subscribe(func(ev *StreamDeleteEvent) {
			if ev.StreamKey == w.key {
				w.log.Info("stream delete", "reason", ev.Reason, "unavailable", ev.Unavailable)
				// While joining, Discord's answer to an earlier try of the
				// same stream can still come; it is the answer that counts
				// only once this one has its stream.
				w.mu.Lock()
				created := w.created
				w.mu.Unlock()
				if !created {
					select {
					case notFound <- ev.Reason:
					default:
					}
					return
				}
				reason := ev.Reason
				if reason == "" {
					reason = "the stream ended"
				}
				w.end(errors.New(reason))
			}
		}),
	)
	if err := p.Main.SendGateway(ctx, &streamWatchCommand{StreamKey: w.key}); err != nil {
		return nil, fmt.Errorf("golive: could not ask to watch: %w", err)
	}

	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var ce *StreamCreateEvent
	var se *StreamServerUpdateEvent
	var reason string
	for ce == nil || se == nil {
		select {
		case ce = <-created:
		case se = <-server:
		case reason = <-notFound:
			// Perhaps an earlier try's answer: wait on, but keep it for the
			// error if nothing better comes.
		case <-wait.Done():
			if reason != "" {
				return nil, fmt.Errorf("golive: Discord says %s", reason)
			}
			return nil, errors.New("golive: Discord did not give the stream's server")
		}
	}
	daveChannel := new(big.Int)
	if _, ok := daveChannel.SetString(ce.RTCChannelID, 10); !ok {
		daveChannel.SetString(ce.RTCServerID, 10)
		daveChannel.Sub(daveChannel, big.NewInt(1))
	}

	loopCtx, loopCancel := context.WithCancel(context.Background())
	w.cancel = loopCancel
	w.conn, w.ops = dial(loopCtx, se.Endpoint, identifyCommand{
		ServerID:  ce.RTCServerID,
		ChannelID: ce.RTCChannelID,
		UserID:    p.UserID,
		SessionID: p.SessionID,
		Token:     se.Token,
		Video:     v.video,
		Streams:   v.streams,
	}, daveChannel.Uint64(), w.log)
	// The streamer's SSRCs come in an Op 12, perhaps before the ready.
	early := make(chan ws.Op, 64)
	ready, err := waitOp(wait, w.log, w.ops, early, func(ev ws.Event) bool { _, ok := ev.(*readyEvent); return ok })
	if err != nil {
		return nil, fmt.Errorf("golive: the stream server did not answer: %w", err)
	}
	r := ready.(*readyEvent)
	mode := slices.IndexFunc(modes, func(m string) bool { return slices.Contains(r.Modes, m) })
	if mode < 0 {
		return nil, fmt.Errorf("golive: no encryption mode in common with the stream server (%v)", r.Modes)
	}
	w.ourSSRC = r.SSRC
	ip, port, err := w.dialUDP(wait, fmt.Sprintf("%s:%d", r.IP, r.Port), r.SSRC)
	if err != nil {
		return nil, err
	}
	w.conn.send(wait, &versionCommand{})
	// A client must send a Speaking and a Video before it receives media,
	// or the server ends its session; a viewer's say it sends nothing: no
	// speaking, and a video with no SSRC and no streams.
	w.sendViewerState(wait, r.SSRC)
	if err := w.conn.send(wait, &selectProtocolCommand{
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
		// The same as a stream's own: a server ends the session (4006) on
		// what it does not take, and experiments left out went as null.
		Experiments: []string{"fixed_keyframe_interval"},
	}); err != nil {
		return nil, err
	}
	desc, err := waitOp(wait, w.log, w.ops, early, func(ev ws.Event) bool { _, ok := ev.(*sessionDescriptionEvent); return ok })
	if err != nil {
		return nil, fmt.Errorf("golive: the stream server did not give a key: %w", err)
	}
	d := desc.(*sessionDescriptionEvent)
	if err := w.useSecret(d.Mode, d.SecretKey); err != nil {
		return nil, err
	}
	w.sendViewerState(wait, r.SSRC)
	w.mu.Lock()
	video := w.videoSSRC
	w.mu.Unlock()
	w.sendWants(wait, video)
	w.log.Info("watching", "mode", d.Mode, "video_codec", d.VideoCodec, "dave", d.DAVEProtocolVersion)

	if w.p.FFmpeg == "" {
		return nil, errors.New("golive: FFmpeg is needed to watch streams")
	}
	ok = true
	go w.follow(early)
	go w.receive()
	go w.askKeyframes()
	return w, nil
}

// sendViewerState tells the server the viewer sends nothing.
func (w *Watch) sendViewerState(ctx context.Context, ssrc uint32) {
	w.conn.send(ctx, &voicegateway.SpeakingEvent{Speaking: 0, SSRC: ssrc})
	w.conn.send(ctx, &videoCommand{AudioSSRC: ssrc, Streams: []videoStream{}})
}

// sendWants asks for the streamer's video, by its SSRC when known.
func (w *Watch) sendWants(ctx context.Context, video uint32) {
	wants := mediaSinkWantsCommand{"any": 100}
	if video != 0 {
		key := fmt.Sprint(video)
		wants[key] = 100
		wants["pixelCounts"] = map[string]int{key: maxWatchW * maxWatchH}
	}
	cmd := wants
	w.conn.send(ctx, &cmd)
}

// waitOp waits for the op that match accepts, keeping the others in keep.
func waitOp(ctx context.Context, log *slog.Logger, ops <-chan ws.Op, keep chan ws.Op, match func(ws.Event) bool) (ws.Event, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case op, ok := <-ops:
			if !ok {
				return nil, errors.New("connection closed")
			}
			// What the server says while joining, for when it refuses.
			log.Info("stream server op", "op", op.Code, "type", fmt.Sprintf("%T", op.Data))
			switch ev := op.Data.(type) {
			case *ws.CloseEvent:
				return nil, fmt.Errorf("%w (code %d): %w", errJoinRefused, ev.Code, ev.Err)
			case *ws.BackgroundErrorEvent:
				log.Warn("stream server error", "err", ev.Err)
				continue
			}
			if match(op.Data) {
				return op.Data, nil
			}
			select {
			case keep <- op:
			default:
			}
		}
	}
}

// follow learns the streamer's video SSRC from the server's ops.
func (w *Watch) follow(early chan ws.Op) {
	handle := func(op ws.Op) {
		if v, ok := op.Data.(*videoEvent); ok && v.UserID == w.p.StreamerID {
			ssrc := v.VideoSSRC
			bestQuality, bestPixels := -1, -1
			for _, s := range v.Streams {
				pixels := s.MaxResolution.Width * s.MaxResolution.Height
				if s.Active && s.SSRC != 0 && (s.Quality > bestQuality || s.Quality == bestQuality && pixels > bestPixels) {
					ssrc, bestQuality, bestPixels = s.SSRC, s.Quality, pixels
				}
			}
			if ssrc != 0 {
				w.mu.Lock()
				w.videoSSRC = ssrc
				w.mu.Unlock()
				w.sendWants(context.Background(), ssrc)
				// The first keyframe is asked for as soon as it can be.
				if w.frames.Load() == 0 {
					w.lastPLI.Store(0)
					w.requestKeyframe()
				}
				w.log.Info("stream video found", "ssrc", ssrc, "streams", fmt.Sprint(v.Streams))
			}
		}
	}
	for {
		select {
		case op := <-early:
			handle(op)
		case op, ok := <-w.ops:
			if !ok {
				w.end(errors.New("the stream server closed the connection"))
				return
			}
			handle(op)
		case <-w.done:
			return
		}
	}
}

// dialUDP connects to the stream server and discovers our address.
func (w *Watch) dialUDP(ctx context.Context, addr string, ssrc uint32) (string, uint16, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return "", 0, fmt.Errorf("golive: could not reach the stream server: %w", err)
	}
	if uc, ok := c.(*net.UDPConn); ok {
		// A keyframe arrives as a burst.
		uc.SetReadBuffer(8 << 20)
	}
	w.udp = c
	var discovery [74]byte
	binary.BigEndian.PutUint16(discovery[0:], 1)
	binary.BigEndian.PutUint16(discovery[2:], 70)
	binary.BigEndian.PutUint32(discovery[4:], ssrc)
	if _, err := c.Write(discovery[:]); err != nil {
		return "", 0, err
	}
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var reply [74]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil {
		return "", 0, fmt.Errorf("golive: no answer to the address discovery: %w", err)
	}
	c.SetReadDeadline(time.Time{})
	body := reply[8:72]
	end := bytes.IndexByte(body, 0)
	if end < 0 {
		return "", 0, errors.New("golive: bad address discovery answer")
	}
	return string(body[:end]), binary.BigEndian.Uint16(reply[72:]), nil
}

func (w *Watch) useSecret(mode string, secret [32]byte) (err error) {
	switch mode {
	case modes[0]:
		var block cipher.Block
		if block, err = aes.NewCipher(secret[:]); err == nil {
			w.aead, err = cipher.NewGCM(block)
		}
	case modes[1]:
		w.aead, err = chacha20poly1305.NewX(secret[:])
	default:
		err = fmt.Errorf("golive: unknown encryption mode %q", mode)
	}
	return err
}

// open decrypts an RTP packet of the rtpsize modes: the header, with the
// four bytes that start an extension, is in the clear; the rest is sealed,
// with the nonce's counter in its last four bytes. It returns the header
// and the payload, without the extension's body.
func (w *Watch) open(pkt []byte) (header []byte, payload []byte, err error) {
	if len(pkt) < 12+4 {
		return nil, nil, errors.New("short packet")
	}
	n := 12 + 4*int(pkt[0]&0x0F)
	ext := pkt[0]&0x10 != 0
	extWords := 0
	if ext {
		if len(pkt) < n+4 {
			return nil, nil, errors.New("short extension")
		}
		extWords = int(binary.BigEndian.Uint16(pkt[n+2:]))
		n += 4
	}
	if len(pkt) < n+4+w.aead.Overhead() {
		return nil, nil, errors.New("short packet")
	}
	var nonce [24]byte
	copy(nonce[:4], pkt[len(pkt)-4:])
	plain, err := w.aead.Open(nil, nonce[:w.aead.NonceSize()], pkt[n:len(pkt)-4], pkt[:n])
	if err != nil {
		return nil, nil, err
	}
	if len(plain) < 4*extWords {
		return nil, nil, errors.New("short extension body")
	}
	payload = plain[4*extWords:]
	// RTP padding belongs to the transport, not the H264 picture or its
	// DAVE footer. Its last byte counts all padding bytes, including itself.
	if pkt[0]&0x20 != 0 {
		if len(payload) == 0 {
			return nil, nil, errors.New("missing RTP padding")
		}
		padding := int(payload[len(payload)-1])
		if padding == 0 || padding > len(payload) {
			return nil, nil, errors.New("invalid RTP padding")
		}
		payload = payload[:len(payload)-padding]
	}
	return pkt[:n], payload, nil
}

// receive reads the streamer's video, rebuilds each picture from its
// packets, removes the end-to-end encryption and decodes it.
func (w *Watch) receive() {
	buf := make([]byte, 2048)
	var (
		au       []byte // the picture being rebuilt, in Annex B
		auTS     uint32
		lastSeq  uint16
		haveSeq  bool
		lost     bool
		fuActive bool
	)
	startCode := []byte{0, 0, 0, 1}
	for {
		n, err := w.udp.Read(buf)
		if err != nil {
			w.end(err)
			return
		}
		pkt := buf[:n]
		if n < 8 || pkt[0]>>6 != 2 {
			continue
		}
		if pkt[1] >= 200 && pkt[1] <= 223 {
			w.receiveReport(pkt, time.Now())
			continue
		}
		if n < 12 {
			continue
		}
		pt := pkt[1] & 0x7F
		if pt != payloadH264 {
			continue
		}
		ssrc := binary.BigEndian.Uint32(pkt[8:])
		w.mu.Lock()
		want := w.videoSSRC
		if want == 0 && ssrc != w.ourSSRC {
			// Before the server says, the first video is the streamer's.
			w.videoSSRC, want = ssrc, ssrc
		}
		w.mu.Unlock()
		if ssrc != want {
			continue
		}
		hdr, payload, err := w.open(pkt)
		if err != nil || len(payload) == 0 {
			continue
		}
		w.packets.Add(1)
		if hdr[0]&0x20 != 0 {
			w.padded.Add(1)
		}
		seq := binary.BigEndian.Uint16(hdr[2:])
		ts := binary.BigEndian.Uint32(hdr[4:])
		w.videoBytes.Add(int64(len(payload)))
		w.mu.Lock()
		w.reception.record(ssrc, seq, ts, time.Now())
		w.mu.Unlock()
		marker := hdr[1]&0x80 != 0
		gap := haveSeq && seq != lastSeq+1
		lastSeq, haveSeq = seq, true
		if ts != auTS {
			// A new picture: the last one was cut short.
			if len(au) > 0 {
				w.lost.Add(1)
				w.needIDR.Store(true)
				w.requestKeyframe()
			}
			au, auTS, lost, fuActive = au[:0], ts, false, false
		}
		// After the reset: a gap before a picture's first packet loses that
		// picture too.
		if gap {
			lost = true
		}
		switch typ := payload[0] & 0x1F; {
		case typ >= 1 && typ <= 23:
			au = append(au, startCode...)
			au = append(au, payload...)
		case typ == 24: // STAP-A
			for p := payload[1:]; len(p) >= 2; {
				size := int(binary.BigEndian.Uint16(p))
				if size == 0 || len(p) < 2+size {
					break
				}
				au = append(au, startCode...)
				au = append(au, p[2:2+size]...)
				p = p[2+size:]
			}
		case typ == 28 && len(payload) >= 2: // FU-A
			start, endFU := payload[1]&0x80 != 0, payload[1]&0x40 != 0
			if start {
				au = append(au, startCode...)
				au = append(au, payload[0]&0xE0|payload[1]&0x1F)
				fuActive = true
			}
			if fuActive {
				au = append(au, payload[2:]...)
			}
			if endFU {
				fuActive = false
			}
		}
		if !marker {
			continue
		}
		if !lost && len(au) > 0 {
			w.decode(au)
		} else if lost {
			// A picture with a packet missing cannot be shown, nor those
			// after it, until a keyframe.
			w.lost.Add(1)
			w.needIDR.Store(true)
			w.requestKeyframe()
		}
		au, lost, fuActive = au[:0], false, false
	}
}

// decode removes the end-to-end encryption of a picture and hands it to the
// decoder, started, or started again, at the size its SPS gives.
func (w *Watch) decode(au []byte) {
	w.assembled.Add(1)
	out := make([]byte, w.conn.dave.MaxDecryptedFrameSize(godave.UserID(w.p.StreamerID.String()), len(au)))
	n, err := w.conn.dave.Decrypt(godave.UserID(w.p.StreamerID.String()), au, out)
	if err != nil {
		if n := w.daveFailed.Add(1); n <= 5 || n%100 == 0 {
			parsed, footerErr := frame.Parse(au)
			var layout []string
			for _, nal := range splitNALs(au) {
				if len(nal) > 0 {
					layout = append(layout, fmt.Sprintf("%d:%d", nal[0]&0x1F, len(nal)))
					if len(layout) == 16 {
						break
					}
				}
			}
			attrs := []any{
				"err", err, "bytes", len(au), "failures", n,
				"dave_frame", frame.LooksLikeDAVEFrame(au), "footer_err", footerErr,
				"nals", strings.Join(layout, ","),
				"replay_rejected", w.conn.dave.Stats().RejectedReplayFrames,
			}
			if parsed != nil {
				attrs = append(attrs, "dave_nonce", parsed.TruncatedNonce,
					"footer_bytes", parsed.SupplementalSize, "ranges", fmt.Sprint(parsed.UnencryptedRanges))
			}
			w.log.Info("could not decrypt a picture", attrs...)
		}
		w.needIDR.Store(true)
		w.requestKeyframe()
		return
	}
	pic := out[:n]
	// After a picture is missing, the ones built on it would smear: they
	// wait for a keyframe, which starts afresh.
	if w.needIDR.Load() {
		if !hasIDR(pic) {
			w.skipped.Add(1)
			w.requestKeyframe()
			return
		}
		w.needIDR.Store(false)
	}
	for _, nal := range splitNALs(pic) {
		if len(nal) > 1 && nal[0]&0x1F == 7 {
			if sw, sh, err := spsSize(nal[1:]); err == nil {
				w.mu.Lock()
				changed := sw != w.srcW || sh != w.srcH
				w.srcW, w.srcH = sw, sh
				w.mu.Unlock()
				if changed && w.p.FFmpeg != "" {
					w.log.Info("stream size", "width", sw, "height", sh)
					if err := w.startDecoder(sw, sh); err != nil {
						w.log.Warn("could not start the decoder", "err", err)
					}
				}
			}
		}
	}
	w.mu.Lock()
	in := w.decIn
	w.mu.Unlock()
	if in == nil {
		// Nothing decodes before a keyframe, which carries the SPS.
		w.requestKeyframe()
		return
	}
	in.Write(pic)
}

// maxWatchW and maxWatchH bound the pictures decoded: bigger streams are
// scaled down to them.
const maxWatchW, maxWatchH = 1920, 1080

// startDecoder runs FFmpeg, which takes the pictures as H264 and gives them
// as RGBA as fast as they come, at the stream's size up to the bound. One
// already running is stopped.
// oldFFmpeg is set once FFmpeg turned out to be older than 5.1.
var oldFFmpeg atomic.Bool

func (w *Watch) startDecoder(sw, sh int) error {
	ow, oh := sw, sh
	if ow > maxWatchW || oh > maxWatchH {
		k := min(float64(maxWatchW)/float64(ow), float64(maxWatchH)/float64(oh))
		ow, oh = int(float64(ow)*k)&^1, int(float64(oh)*k)&^1
	}
	args := []string{"-hide_banner", "-loglevel", "error",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "32", "-analyzeduration", "0",
		"-f", "h264", "-i", "pipe:0"}
	if ow != sw || oh != sh {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic", ow, oh))
	}
	// Raw H264 has no timestamps: FFmpeg would take it as 25 FPS and drop
	// the pictures that come sooner, which live is all of them. FFmpeg
	// before 5.1 calls -fps_mode -vsync.
	mode := "-fps_mode"
	if oldFFmpeg.Load() {
		mode = "-vsync"
	}
	args = append(args, mode, "passthrough", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	cmd := exec.Command(w.p.FFmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("golive: could not start FFmpeg: %w", err)
	}
	w.mu.Lock()
	old, oldIn := w.decoder, w.decIn
	w.decoder, w.decIn = cmd, in
	w.mu.Unlock()
	if oldIn != nil {
		oldIn.Close()
	}
	if old != nil && old.Process != nil {
		old.Process.Kill()
		go old.Wait()
	}
	go func() {
		for {
			img := image.NewRGBA(image.Rect(0, 0, ow, oh))
			if _, err := io.ReadFull(out, img.Pix); err != nil {
				if msg := strings.TrimSpace(stderr.String()); msg != "" {
					w.log.Info("decoder stopped", "ffmpeg", msg)
					// An FFmpeg too old for -fps_mode is started again with
					// -vsync, from a keyframe asked for at once.
					if strings.Contains(msg, "fps_mode") && !oldFFmpeg.Swap(true) {
						w.mu.Lock()
						current := w.decoder == cmd
						w.mu.Unlock()
						if current {
							if err := w.startDecoder(sw, sh); err != nil {
								w.log.Warn("could not start the decoder", "err", err)
							}
							w.lastPLI.Store(0)
							w.requestKeyframe()
						}
					}
				}
				return
			}
			w.mu.Lock()
			current := w.decoder == cmd
			if current {
				w.frame = img
			}
			w.mu.Unlock()
			if !current {
				return
			}
			w.frames.Add(1)
			if w.p.OnFrame != nil {
				w.p.OnFrame()
			}
		}
	}()
	return nil
}

// requestKeyframe asks the streamer for a whole picture, at most twice a
// second: after a loss, nothing decodes until one comes.
func (w *Watch) requestKeyframe() {
	now := time.Now().UnixNano()
	last := w.lastPLI.Load()
	if now-last < int64(500*time.Millisecond) {
		return
	}
	// Counted only once sent: before the streamer's video is known, nothing
	// can be asked for.
	if w.sendPLI() {
		w.lastPLI.CompareAndSwap(last, now)
	}
}

// askKeyframes asks for a whole picture until one decodes, and logs how
// the stream goes every 5 seconds, which the FPS shown comes from.
func (w *Watch) askKeyframes() {
	// Every 250 ms until the first picture, then every second.
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	var lastFrames int64
	var lastBytes, lastPictures, lastShown int64
	lastStats := time.Now()
	var quarter int
	tick := 0
	for {
		frames := w.frames.Load()
		if frames == 0 {
			w.requestKeyframe()
		}
		if quarter++; quarter%4 != 0 {
			select {
			case <-t.C:
				continue
			case <-w.done:
				return
			}
		}
		w.mu.Lock()
		w.fps = int(frames - lastFrames)
		w.mu.Unlock()
		w.sendReport()
		lastFrames = frames
		if tick++; tick%5 == 0 {
			now := time.Now()
			seconds := now.Sub(lastStats).Seconds()
			octets, pictures := w.videoBytes.Load(), w.assembled.Load()
			w.log.Info("watching", "packets", w.packets.Load(), "pictures", pictures, "lost", w.lost.Load(),
				"dave_failed", w.daveFailed.Load(), "skipped", w.skipped.Load(), "padded", w.padded.Load(), "shown", frames,
				"video_kbps", int(float64(octets-lastBytes)*8/seconds/1000),
				"received_fps", fmt.Sprintf("%.1f", float64(pictures-lastPictures)/seconds),
				"decoded_fps", fmt.Sprintf("%.1f", float64(frames-lastShown)/seconds))
			lastBytes, lastPictures, lastShown, lastStats = octets, pictures, frames, now
			w.mu.Lock()
			video := w.videoSSRC
			w.mu.Unlock()
			w.sendWants(context.Background(), video)
		}
		select {
		case <-t.C:
		case <-w.done:
			return
		}
	}
}

// hasIDR reports whether an Annex B picture holds a keyframe slice.
func hasIDR(pic []byte) bool {
	for _, nal := range splitNALs(pic) {
		if len(nal) > 0 && nal[0]&0x1F == 5 {
			return true
		}
	}
	return false
}

// maxBitrate is what the viewer says it can take, in bits a second: servers
// lower a stream's quality for viewers that say nothing.
const maxBitrate = 8_000_000

// sendReport sends an RTCP receiver report with a REMB, as WebRTC clients
// do every second: how the video arrives, and the bitrate it can take.
func (w *Watch) sendReport() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.videoSSRC == 0 || w.aead == nil {
		return
	}
	rr := w.reception.report(w.ourSSRC, w.videoSSRC, time.Now())
	// REMB (206, format 15): "REMB", one SSRC, the bitrate as exponent and
	// 18-bit mantissa.
	exp, mant := 0, maxBitrate
	for mant >= 1<<18 {
		mant >>= 1
		exp++
	}
	remb := make([]byte, 8+16)
	remb[0], remb[1] = 0x8F, 206
	binary.BigEndian.PutUint16(remb[2:], 5)
	binary.BigEndian.PutUint32(remb[4:], w.ourSSRC)
	copy(remb[12:], "REMB")
	remb[16] = 1
	remb[17] = byte(exp<<2) | byte(mant>>16)
	binary.BigEndian.PutUint16(remb[18:], uint16(mant))
	binary.BigEndian.PutUint32(remb[20:], w.videoSSRC)
	for _, pkt := range [][]byte{rr, remb} {
		header := pkt[:8]
		binary.BigEndian.PutUint32(w.nonce[:4], binary.BigEndian.Uint32(w.nonce[:4])+1)
		sealed := w.aead.Seal(append([]byte(nil), header...), w.nonce[:w.aead.NonceSize()], pkt[8:], header)
		w.udp.Write(append(sealed, w.nonce[:4]...))
	}
}

// Info is the stream's size and how many pictures a second show, 0 before
// they are known.
func (w *Watch) Info() (width, height, fps int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.srcW, w.srcH, w.fps
}

// sendPLI sends an RTCP picture loss indication for the streamer's video,
// reporting whether it could.
func (w *Watch) sendPLI() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.videoSSRC == 0 || w.aead == nil {
		return false
	}
	header := make([]byte, 8)
	header[0], header[1] = 0x81, 206
	binary.BigEndian.PutUint16(header[2:], 2)
	binary.BigEndian.PutUint32(header[4:], w.ourSSRC)
	media := make([]byte, 4)
	binary.BigEndian.PutUint32(media, w.videoSSRC)
	binary.BigEndian.PutUint32(w.nonce[:4], binary.BigEndian.Uint32(w.nonce[:4])+1)
	pkt := w.aead.Seal(append([]byte(nil), header...), w.nonce[:w.aead.NonceSize()], media, header)
	pkt = append(pkt, w.nonce[:4]...)
	_, err := w.udp.Write(pkt)
	return err == nil
}

// Frame is the latest picture, or nil before the first.
func (w *Watch) Frame() *image.RGBA {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frame
}

// Streamer is who is watched.
func (w *Watch) Streamer() discord.UserID { return w.p.StreamerID }

func (w *Watch) end(err error) {
	w.once.Do(func() {
		close(w.done)
		w.log.Info("watching ended", "err", err)
		w.teardown()
		if w.p.OnEnd != nil {
			w.p.OnEnd(err)
		}
	})
}

// Stop stops watching.
func (w *Watch) Stop() {
	w.Main().SendGateway(context.Background(), &streamDeleteCommand{StreamKey: w.key})
	w.end(nil)
}

// Main is the main gateway the stream is watched on.
func (w *Watch) Main() Main { return w.p.Main }

func (w *Watch) teardown() {
	for _, rm := range w.rm {
		rm()
	}
	w.rm = nil
	if w.cancel != nil {
		w.cancel()
	}
	if w.udp != nil {
		w.udp.Close()
	}
	w.mu.Lock()
	if w.decIn != nil {
		w.decIn.Close()
		w.decIn = nil
	}
	if w.decoder != nil && w.decoder.Process != nil {
		w.decoder.Process.Kill()
		go w.decoder.Wait()
		w.decoder = nil
	}
	w.mu.Unlock()
}
