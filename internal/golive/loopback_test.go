//go:build windows && !no_voice

package golive

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/gorilla/websocket"
)

// fakeDiscord stands for Discord: its main gateway, which gives stream
// servers, and one stream server, which relays a stream's packets from its
// streamer to its viewers, dropping some on purpose if dropRate says so.
type fakeDiscord struct {
	t        *testing.T
	wsURL    string
	udp      *net.UDPConn
	dropRate float64
	// noReplay leaves out telling who joins later of the videos before, to
	// check that the viewer finds the resend stream by itself.
	noReplay bool
	dropped  atomic.Int64
	relayed  atomic.Int64

	mu       sync.Mutex
	handlers []any
	peers    map[string]*net.UDPAddr
	conns    []*fakeConn
	nextSSRC uint32
	// videos are the last video ops of each user, told to who joins later,
	// as Discord does.
	videos map[string]map[string]any
}

type fakeConn struct {
	mu     sync.Mutex
	c      *websocket.Conn
	userID string
}

func (c *fakeConn) send(op int, d any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.c.WriteJSON(map[string]any{"op": op, "d": d})
}

var fakeSecret [32]byte

func newFakeDiscord(t *testing.T) *fakeDiscord {
	for i := range fakeSecret {
		fakeSecret[i] = byte(i*13 + 1)
	}
	f := &fakeDiscord{t: t, peers: make(map[string]*net.UDPAddr), nextSSRC: 1000}
	var err error
	f.udp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f.udp.SetReadBuffer(8 << 20)
	go f.relay()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go f.serve(&fakeConn{c: c})
	}))
	t.Cleanup(func() { srv.Close(); f.udp.Close() })
	f.wsURL = "ws://" + strings.TrimPrefix(srv.URL, "http://")
	return f
}

// The main gateway: a stream asked for, created or watched, gets its
// server at once.
func (f *fakeDiscord) SendGateway(_ context.Context, ev ws.Event) error {
	var key string
	switch c := ev.(type) {
	case *streamCreateCommand:
		key = StreamKey(*c.GuildID, c.ChannelID, 1)
	case *streamWatchCommand:
		key = c.StreamKey
	default:
		return nil
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.emit(&StreamCreateEvent{StreamKey: key, RTCServerID: "5000", RTCChannelID: "4999"})
		f.emit(&StreamServerUpdateEvent{StreamKey: key, Endpoint: f.wsURL, Token: "token"})
	}()
	return nil
}

func (f *fakeDiscord) Subscribe(h any) func() {
	f.mu.Lock()
	f.handlers = append(f.handlers, h)
	f.mu.Unlock()
	return func() {}
}

func (f *fakeDiscord) emit(ev any) {
	f.mu.Lock()
	hs := append([]any(nil), f.handlers...)
	f.mu.Unlock()
	for _, h := range hs {
		v := reflect.ValueOf(h)
		if v.Type().In(0) == reflect.TypeOf(ev) {
			v.Call([]reflect.Value{reflect.ValueOf(ev)})
		}
	}
}

// serve is the stream server's gateway for one client.
func (f *fakeDiscord) serve(c *fakeConn) {
	defer c.c.Close()
	c.send(8, map[string]any{"heartbeat_interval": 41250})
	f.mu.Lock()
	f.conns = append(f.conns, c)
	audio := f.nextSSRC
	f.nextSSRC += 10
	f.mu.Unlock()
	for {
		var msg struct {
			Op int             `json:"op"`
			D  json.RawMessage `json:"d"`
		}
		if err := c.c.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Op {
		case 0: // identify
			var id struct {
				UserID string `json:"user_id"`
			}
			json.Unmarshal(msg.D, &id)
			c.userID = id.UserID
			_, port, _ := net.SplitHostPort(f.udp.LocalAddr().String())
			var p int
			json.Unmarshal([]byte(port), &p)
			// Told after the ready, which this goroutine sends first.
			go func() {
				time.Sleep(50 * time.Millisecond)
				f.mu.Lock()
				if f.noReplay {
					f.mu.Unlock()
					return
				}
				for user, v := range f.videos {
					if user != c.userID {
						go c.send(12, v)
					}
				}
				f.mu.Unlock()
			}()
			c.send(2, map[string]any{"ssrc": audio, "ip": "127.0.0.1", "port": p,
				"modes":   []string{"aead_aes256_gcm_rtpsize"},
				"streams": []map[string]any{{"type": "video", "rid": "100", "ssrc": audio + 1, "rtx_ssrc": audio + 2, "active": false, "quality": 100}}})
		case 3: // heartbeat
			c.send(6, map[string]any{"t": time.Now().UnixMilli()})
		case 1: // select protocol
			c.send(4, map[string]any{"mode": "aead_aes256_gcm_rtpsize", "secret_key": fakeSecret,
				"dave_protocol_version": 0, "audio_codec": "opus", "video_codec": "H264"})
		case 12: // video: told to everyone else, with who sends it
			var v map[string]any
			json.Unmarshal(msg.D, &v)
			v["user_id"] = c.userID
			f.mu.Lock()
			if f.videos == nil {
				f.videos = make(map[string]map[string]any)
			}
			f.videos[c.userID] = v
			for _, o := range f.conns {
				if o != c {
					go o.send(12, v)
				}
			}
			f.mu.Unlock()
		}
	}
}

// relay answers address discovery and sends every other packet to the other
// peers, dropping video packets at dropRate.
func (f *fakeDiscord) relay() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := buf[:n]
		if n == 74 && binary.BigEndian.Uint16(pkt) == 1 {
			reply := make([]byte, 74)
			binary.BigEndian.PutUint16(reply, 2)
			binary.BigEndian.PutUint16(reply[2:], 70)
			copy(reply[4:8], pkt[4:8])
			copy(reply[8:], from.IP.String())
			binary.BigEndian.PutUint16(reply[72:], uint16(from.Port))
			f.udp.WriteToUDP(reply, from)
			f.mu.Lock()
			f.peers[from.String()] = from
			f.mu.Unlock()
			continue
		}
		isVideo := n >= 12 && pkt[0]>>6 == 2 && pkt[1]&0x7F == payloadH264
		if isVideo && f.dropRate > 0 && rand.Float64() < f.dropRate {
			f.dropped.Add(1)
			continue
		}
		f.mu.Lock()
		for k, p := range f.peers {
			if k != from.String() {
				f.udp.WriteToUDP(pkt, p)
			}
		}
		f.mu.Unlock()
		if isVideo {
			f.relayed.Add(1)
		}
	}
}

// TestStreamLoopback streams this computer's screen with Linkcord's own
// streamer through a fake stream server and watches it with Linkcord's own
// viewer: how soon the first picture shows, how many show a second, at what
// size, and whether the picture keeps moving with packets lost.
func TestStreamLoopback(t *testing.T) {
	if os.Getenv("LINKCORD_LOOPBACK") == "" {
		t.Skip("LINKCORD_LOOPBACK is not set: it streams the screen for about 20 s")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	codecDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "linkcord")
	// A window that always moves: a test pattern, which ffplay plays.
	ffplay, err := exec.LookPath("ffplay")
	if err != nil {
		t.Skip("ffplay is not installed")
	}
	player := exec.Command(ffplay, "-hide_banner", "-loglevel", "error", "-window_title", "linkcord-loopback",
		"-x", "960", "-y", "540", "-left", "40", "-top", "40", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30")
	if err := player.Start(); err != nil {
		t.Skip(err)
	}
	defer player.Process.Kill()
	var screen *Source
	for range 50 {
		for _, s := range Sources(0, 160) {
			if s.Kind == Window && strings.Contains(s.Title, "linkcord-loopback") {
				s := s
				screen = &s
			}
		}
		if screen != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if screen == nil {
		t.Skip("the test pattern window did not show")
	}
	for _, run := range []struct {
		loss     float64
		noReplay bool
	}{{0, false}, {0.02, false}, {0.02, true}} {
		loss := run.loss
		f := newFakeDiscord(t)
		f.dropRate, f.noReplay = loss, run.noReplay
		guild, channel := discord.GuildID(10), discord.ChannelID(20)
		stream, err := Start(context.Background(), Params{
			Main: f, Subscribe: f.Subscribe, GuildID: guild, ChannelID: channel, UserID: 1,
			SessionID: "s1", Source: *screen, Quality: Quality{Height: 1080, FPS: 30}, CodecDir: codecDir,
		})
		if err != nil {
			t.Fatalf("streaming: %v", err)
		}
		start := time.Now()
		var first time.Duration
		var once sync.Once
		w, err := StartWatching(context.Background(), WatchParams{
			Main: f, Subscribe: f.Subscribe, GuildID: guild, ChannelID: channel, StreamerID: 1, UserID: 2,
			SessionID: "s2", FFmpeg: ffmpeg,
			OnFrame: func() { once.Do(func() { first = time.Since(start) }) },
		})
		if err != nil {
			stream.Stop()
			t.Fatalf("watching: %v", err)
		}
		// Watch for 8 s, sampling how many pictures showed each second.
		var perSecond []int64
		last := w.frames.Load()
		for range 8 {
			time.Sleep(time.Second)
			n := w.frames.Load()
			perSecond = append(perSecond, n-last)
			last = n
		}
		wd, ht, _ := w.Info()
		stream.med.mu.Lock()
		resent, missed := stream.med.resent, stream.med.resendMissed
		stream.med.mu.Unlock()
		w.mu.Lock()
		t.Logf("resends: sent %d, not possible %d; received %d, ignored %d; streamer rtx %d, viewer expects video %d rtx %d", resent, missed, w.rtxSeen.Load(), w.rtxIgnored.Load(), stream.med.rtxSSRC, w.videoSSRC, w.rtxSSRC)
		w.mu.Unlock()
		t.Logf("loss %.0f%%: first picture after %v, %dx%d, pictures a second %v, relayed %d, dropped %d, lost pictures %d, dave failures %d",
			loss*100, first.Round(time.Millisecond), wd, ht, perSecond, f.relayed.Load(), f.dropped.Load(), w.lost.Load(), w.daveFailed.Load())
		w.Stop()
		stream.Stop()
		if first == 0 {
			t.Fatalf("loss %.0f%%: no picture showed", loss*100)
		}
		// Frozen: a whole second with no new picture after the first ones.
		for i, n := range perSecond[2:] {
			if n == 0 {
				t.Errorf("loss %.0f%%: the picture froze in second %d", loss*100, i+3)
			}
		}
	}
}
