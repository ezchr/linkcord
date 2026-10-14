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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/disgoorg/godave"
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

// The size pictures of a watched stream are decoded at, letterboxed: the
// biggest a card or the full window shows.
const watchW, watchH = 1280, 720

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
	frame   *image.RGBA
	frames  atomic.Int64
	created bool
	once    sync.Once
	decoder *exec.Cmd
	decIn   io.WriteCloser
	done    chan struct{}
}

// streamWatchCommand is Op 20: it asks to watch a stream.
type streamWatchCommand struct {
	StreamKey string `json:"stream_key"`
}

func (*streamWatchCommand) Op() ws.OpCode           { return 20 }
func (*streamWatchCommand) EventType() ws.EventType { return "" }

// mediaSinkWantsCommand is Op 15: what quality of whose video the client
// wants; "any" at 100 asks for everything at its best.
type mediaSinkWantsCommand struct {
	Any int `json:"any"`
}

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
	// video and streams are said in the identify; slot describes the video
	// slots the server gives before asking for the media.
	video   bool
	streams []streamInfo
	slot    bool
}

var joinVariants = []joinVariant{
	{name: "screen-slot", video: true, streams: []streamInfo{{Type: "screen", RID: "100", Quality: 100}}, slot: true},
	{name: "screen", video: true, streams: []streamInfo{{Type: "screen", RID: "100", Quality: 100}}},
	{name: "plain", video: true},
	{name: "no-video"},
}

// StartWatching starts watching the stream of p.StreamerID.
func StartWatching(ctx context.Context, p WatchParams) (*Watch, error) {
	var errs []error
	for _, v := range joinVariants {
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
	// As Discord's clients do, viewers too: the video slots the server gave,
	// not sending.
	if v.slot && len(r.Streams) > 0 {
		st := r.Streams[0]
		w.conn.send(wait, &videoCommand{
			AudioSSRC: r.SSRC, VideoSSRC: st.SSRC, RTXSSRC: st.RTXSSRC,
			Streams: []videoStream{{Type: "video", RID: "100", SSRC: st.SSRC, Quality: 100, RTXSSRC: st.RTXSSRC,
				MaxResolution: maxResolution{Type: "fixed"}}},
		})
	}
	if err := w.conn.send(wait, &selectProtocolCommand{
		Protocol: "udp",
		Codecs: []codecInfo{
			{Name: "opus", Type: "audio", Priority: 1000, PayloadType: payloadOpus},
			{Name: "H264", Type: "video", Priority: 2000, PayloadType: payloadH264, RTXPayloadType: payloadH264RTX, Decode: true},
		},
		Data:            voicegateway.SelectProtocolData{Address: ip, Port: port, Mode: modes[mode]},
		Address:         ip,
		Port:            port,
		Mode:            modes[mode],
		RTCConnectionID: newUUID(),
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
	w.conn.send(wait, &mediaSinkWantsCommand{Any: 100})
	w.log.Info("watching", "mode", d.Mode, "video_codec", d.VideoCodec, "dave", d.DAVEProtocolVersion)

	if err := w.startDecoder(); err != nil {
		return nil, err
	}
	ok = true
	go w.follow(early)
	go w.receive()
	go w.askKeyframes()
	return w, nil
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
			for _, s := range v.Streams {
				if s.Active && s.SSRC != 0 {
					ssrc = s.SSRC
				}
			}
			if ssrc != 0 {
				w.mu.Lock()
				w.videoSSRC = ssrc
				w.mu.Unlock()
				w.log.Info("stream video found", "ssrc", ssrc)
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
	return pkt[:n], plain[4*extWords:], nil
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
		// RTCP (types 200–223) and anything not RTP version 2 are skipped.
		if n < 12 || pkt[0]>>6 != 2 || pkt[1]&0x7F >= 72 && pkt[1]&0x7F <= 95 {
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
		seq := binary.BigEndian.Uint16(hdr[2:])
		ts := binary.BigEndian.Uint32(hdr[4:])
		marker := hdr[1]&0x80 != 0
		if haveSeq && seq != lastSeq+1 {
			lost = true
		}
		lastSeq, haveSeq = seq, true
		if ts != auTS {
			// A new picture: the last one was cut short.
			au, auTS, lost, fuActive = au[:0], ts, false, false
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
		}
		au, lost, fuActive = au[:0], false, false
	}
}

// decode removes the end-to-end encryption of a picture and hands it to the
// decoder.
func (w *Watch) decode(au []byte) {
	out := make([]byte, w.conn.dave.MaxDecryptedFrameSize(godave.UserID(w.p.StreamerID.String()), len(au)))
	n, err := w.conn.dave.Decrypt(godave.UserID(w.p.StreamerID.String()), au, out)
	if err != nil {
		return
	}
	w.mu.Lock()
	in := w.decIn
	w.mu.Unlock()
	if in != nil {
		in.Write(out[:n])
	}
}

// startDecoder runs FFmpeg, which takes the pictures as H264 and gives them
// as RGBA, letterboxed to watchW by watchH, as fast as they come.
func (w *Watch) startDecoder() error {
	if w.p.FFmpeg == "" {
		return errors.New("golive: FFmpeg is needed to watch streams")
	}
	cmd := exec.Command(w.p.FFmpeg, "-hide_banner", "-loglevel", "error",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "32", "-analyzeduration", "0",
		"-f", "h264", "-i", "pipe:0",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", watchW, watchH, watchW, watchH),
		"-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("golive: could not start FFmpeg: %w", err)
	}
	w.mu.Lock()
	w.decoder, w.decIn = cmd, in
	w.mu.Unlock()
	go func() {
		for {
			img := image.NewRGBA(image.Rect(0, 0, watchW, watchH))
			if _, err := io.ReadFull(out, img.Pix); err != nil {
				return
			}
			w.mu.Lock()
			w.frame = img
			w.mu.Unlock()
			w.frames.Add(1)
			if w.p.OnFrame != nil {
				w.p.OnFrame()
			}
		}
	}()
	return nil
}

// askKeyframes asks the streamer for a whole picture until one decodes:
// pictures only decode from one.
func (w *Watch) askKeyframes() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if w.frames.Load() == 0 {
			w.sendPLI()
		}
		select {
		case <-t.C:
		case <-w.done:
			return
		}
	}
}

// sendPLI sends an RTCP picture loss indication for the streamer's video.
func (w *Watch) sendPLI() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.videoSSRC == 0 || w.aead == nil {
		return
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
	w.udp.Write(pkt)
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
