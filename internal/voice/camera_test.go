//go:build !no_voice

package voice

import (
	"encoding/binary"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/voice"
	"github.com/ayn2op/arikawa/v3/voice/udp"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
)

func TestVideoEventParse(t *testing.T) {
	var ev voicegateway.VideoEvent
	raw := `{"user_id":"42","audio_ssrc":10,"video_ssrc":11,"rtx_ssrc":12,"streams":[
		{"type":"video","rid":"50","ssrc":13,"rtx_ssrc":14,"active":true,"quality":50},
		{"type":"video","rid":"100","ssrc":11,"rtx_ssrc":12,"active":true,"quality":100,"max_resolution":{"type":"fixed","width":1280,"height":720}}]}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.UserID != 42 || ev.AudioSSRC != 10 {
		t.Fatalf("parsed %+v", ev)
	}
	if v, r := ev.ActiveVideo(); v != 11 || r != 12 {
		t.Fatalf("active video %d/%d, want the best stream 11/12", v, r)
	}
	// Stopping: no active stream, or no SSRC.
	for _, raw := range []string{
		`{"user_id":"42","audio_ssrc":10,"video_ssrc":0,"rtx_ssrc":0,"streams":[{"type":"video","rid":"100","ssrc":11,"active":false,"quality":100}]}`,
		`{"user_id":"42","audio_ssrc":10,"video_ssrc":0,"rtx_ssrc":0,"streams":[]}`,
		`{"user_id":"42","audio_ssrc":10,"video_ssrc":0,"rtx_ssrc":0}`,
	} {
		var ev voicegateway.VideoEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		if v, _ := ev.ActiveVideo(); v != 0 {
			t.Fatalf("%s: video %d, want none", raw, v)
		}
	}
	// No streams: the event's own SSRCs.
	ev = voicegateway.VideoEvent{UserID: 1, VideoSSRC: 5, RTXSSRC: 6}
	if v, r := ev.ActiveVideo(); v != 5 || r != 6 {
		t.Fatalf("video %d/%d", v, r)
	}
	// The op is known to the voice gateway.
	if fn := voicegateway.OpUnmarshalers.Lookup(12, ""); fn == nil {
		t.Fatal("op 12 is not unmarshaled")
	}
	if fn := voicegateway.OpUnmarshalers.Lookup(15, ""); fn == nil {
		t.Fatal("op 15 is not unmarshaled")
	}
}

type fakeReceiver struct {
	mu         sync.Mutex
	video, rtx uint32
	pushed     []uint32
	closed     bool
	pixels     int
}

func (r *fakeReceiver) SetSSRCs(v, x uint32) { r.mu.Lock(); r.video, r.rtx = v, x; r.mu.Unlock() }
func (r *fakeReceiver) Push(ssrc uint32, _ uint16, _ uint32, _ bool, _ []byte) {
	r.mu.Lock()
	r.pushed = append(r.pushed, ssrc)
	r.mu.Unlock()
}
func (r *fakeReceiver) Pixels() int { return r.pixels }
func (r *fakeReceiver) Close()      { r.mu.Lock(); r.closed = true; r.mu.Unlock() }

type fakeBackend struct{ made []*fakeReceiver }

func (b *fakeBackend) NewVideoReceiver(VideoReceiverParams) VideoReceiver {
	r := &fakeReceiver{}
	b.made = append(b.made, r)
	return r
}
func (b *fakeBackend) NewTransportFeedback() TransportFeedback { return nil }

func rtp(pt byte, ssrc uint32) *udp.MediaPacket {
	h := make([]byte, 12)
	h[0], h[1] = 0x80, pt
	binary.BigEndian.PutUint32(h[8:], ssrc)
	return &udp.MediaPacket{Header: h, Payload: []byte{0x65, 1, 2}}
}

func TestCameraRouting(t *testing.T) {
	b := &fakeBackend{}
	ses := voice.NewSessionCustom(nil, handler.New(), 1)
	changes := 0
	c := newCameras(b, ses, func() { changes++ })

	c.video(&voicegateway.VideoEvent{UserID: 7, VideoSSRC: 100, RTXSSRC: 101,
		Streams: []voicegateway.VideoStream{{SSRC: 100, RTXSSRC: 101, Active: true, Quality: 100}}})
	if len(b.made) != 1 || c.receiver(7) == nil || changes != 1 {
		t.Fatalf("camera not started: %d receivers, %d changes", len(b.made), changes)
	}
	r := b.made[0]
	if r.video != 100 || r.rtx != 101 {
		t.Fatalf("SSRCs %d/%d", r.video, r.rtx)
	}
	if c.handle(rtp(payloadOpus, 50)) {
		t.Fatal("Opus was taken from the audio")
	}
	if !c.handle(rtp(payloadH264, 100)) || !c.handle(rtp(payloadH264RTX, 101)) {
		t.Fatal("video not taken")
	}
	if !c.handle(rtp(payloadH264, 999)) || !c.handle(rtp(96, 100)) {
		t.Fatal("unknown video not dropped")
	}
	if len(r.pushed) != 2 || r.pushed[0] != 100 || r.pushed[1] != 101 {
		t.Fatalf("pushed %v", r.pushed)
	}

	// New SSRCs: same receiver, routed anew.
	c.video(&voicegateway.VideoEvent{UserID: 7, VideoSSRC: 200, RTXSSRC: 201})
	if len(b.made) != 1 || r.video != 200 {
		t.Fatal("SSRC change not followed")
	}
	c.handle(rtp(payloadH264, 100))
	if len(r.pushed) != 2 {
		t.Fatal("old SSRC still routed")
	}

	// Stopping closes it.
	c.video(&voicegateway.VideoEvent{UserID: 7, Streams: []voicegateway.VideoStream{{SSRC: 200, Active: false}}})
	if !r.closed || c.receiver(7) != nil || c.any() {
		t.Fatal("camera not stopped")
	}
	c.handle(rtp(payloadH264, 200))
	if len(r.pushed) != 2 {
		t.Fatal("stopped camera still routed")
	}

	// Leaving, and a new connection, close them too.
	c.video(&voicegateway.VideoEvent{UserID: 8, VideoSSRC: 300})
	c.video(&voicegateway.VideoEvent{UserID: 9, VideoSSRC: 400})
	c.left(discord.UserID(8))
	if !b.made[1].closed || b.made[2].closed {
		t.Fatal("leaving did not stop only their camera")
	}
	c.reset()
	if !b.made[2].closed || c.any() {
		t.Fatal("reset left a camera")
	}
	// Garbage does not crash.
	c.video(nil)
	c.video(&voicegateway.VideoEvent{})
}

func TestWantsLadder(t *testing.T) {
	l := newWantsLadder(videoBudget)
	if len(l.steps) == 0 || l.steps[len(l.steps)-1].want != 100 || l.steps[len(l.steps)-1].pixels > videoBudget {
		t.Fatalf("ladder %+v", l.steps)
	}
	if v := l.maxSinkValue(1, 0); v != 100 {
		t.Fatalf("one video: %d, want 100", v)
	}
	if v := l.maxSinkValue(4, 0); v >= 100 || v <= 0 {
		t.Fatalf("four videos: %d", v)
	}
	if small, big := l.maxSinkValue(1, 320*180), l.maxSinkValue(1, 1280*720); small >= big {
		t.Fatalf("a small tile %d is not asked for less than a big one %d", small, big)
	}

	now := time.Now()
	cams := []cameraWant{{video: 1, pixels: 640 * 360}}
	for i := 0; i < 5; i++ {
		cams = append(cams, cameraWant{video: uint32(10 + i), hidden: now.Add(-time.Duration(i+1) * time.Second)})
	}
	cams = append(cams, cameraWant{video: 20, hidden: now.Add(-time.Minute)})
	w := sinkWants(l, cams, now)
	if w["any"] != 100 {
		t.Fatal("audio not kept at 100")
	}
	if w["1"].(int) <= 0 || w["pixelCounts"].(map[string]int)["1"] != 640*360 {
		t.Fatalf("shown camera: %v", w)
	}
	for _, k := range []string{"10", "11", "12"} {
		if w[k].(int) <= 0 {
			t.Fatalf("recently hidden %s stopped: %v", k, w)
		}
	}
	for _, k := range []string{"13", "14", "20"} {
		if w[k].(int) != 0 {
			t.Fatalf("hidden %s still asked for: %v", k, w)
		}
	}
}
