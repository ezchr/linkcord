//go:build !no_voice

package voice

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/voice"
	"github.com/ayn2op/arikawa/v3/voice/udp"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
)

// videoCodecs are the codecs told the voice server when cameras can be
// shown: those of the stream viewer, H264 being the only video decoded.
var videoCodecs = []voicegateway.CodecInfo{
	{Name: "opus", Type: "audio", Priority: 1000, PayloadType: payloadOpus},
	{Name: "H264", Type: "video", Priority: 2000, PayloadType: payloadH264, RTXPayloadType: payloadH264RTX, Encode: true, Decode: true},
}

// camera is the video of one user of the voice channel.
type camera struct {
	user       discord.UserID
	video, rtx uint32
	recv       VideoReceiver
	// hidden is since when it is not shown, zero while it is.
	hidden time.Time
}

// cameras routes the video of the voice connection to a receiver for each
// user whose camera is on, and asks the voice server for each in the
// quality of its tile.
type cameras struct {
	backend VideoBackend
	session *voice.Session
	ladder  wantsLadder

	mu     sync.Mutex
	byUser map[discord.UserID]*camera
	bySSRC map[uint32]*camera
	tw     TransportFeedback
	// sent is the wants last sent, and sentAt when.
	sent   string
	sentAt time.Time
	// changed is called when a camera starts or stops.
	changed func()
}

func newCameras(backend VideoBackend, session *voice.Session, changed func()) *cameras {
	return &cameras{
		backend: backend,
		session: session,
		ladder:  newWantsLadder(videoBudget),
		byUser:  make(map[discord.UserID]*camera),
		bySSRC:  make(map[uint32]*camera),
		tw:      backend.NewTransportFeedback(),
		changed: changed,
	}
}

// video follows Op 12: a user's camera starts, changes or stops.
func (c *cameras) video(ev *voicegateway.VideoEvent) {
	if ev == nil || !ev.UserID.IsValid() {
		return
	}
	video, rtx := ev.ActiveVideo()
	// Only cameras are received here; streams have their own server.
	c.set(ev.UserID, video, rtx)
}

// set starts, changes or, with video 0, stops the camera of user.
func (c *cameras) set(user discord.UserID, video, rtx uint32) {
	c.mu.Lock()
	cam := c.byUser[user]
	var closing VideoReceiver
	started := false
	switch {
	case video == 0 && cam != nil:
		c.dropLocked(cam)
		closing = cam.recv
	case video == 0:
	case cam == nil:
		cam = &camera{user: user, video: video, rtx: rtx}
		cam.recv = c.backend.NewVideoReceiver(c.params(user))
		cam.recv.SetSSRCs(video, rtx)
		c.byUser[user] = cam
		c.index(cam)
		started = true
	case cam.video != video || cam.rtx != rtx:
		c.unindex(cam)
		cam.video, cam.rtx = video, rtx
		cam.recv.SetSSRCs(video, rtx)
		c.index(cam)
	}
	c.mu.Unlock()
	if closing != nil {
		closing.Close()
	}
	if closing != nil || started {
		slog.Info("camera", "user", user, "on", started, "video_ssrc", video, "rtx_ssrc", rtx)
		if c.changed != nil {
			c.changed()
		}
	}
	c.sendWants(true)
}

func (c *cameras) index(cam *camera) {
	if cam.video != 0 {
		c.bySSRC[cam.video] = cam
	}
	if cam.rtx != 0 {
		c.bySSRC[cam.rtx] = cam
	}
}

func (c *cameras) unindex(cam *camera) {
	for _, s := range []uint32{cam.video, cam.rtx} {
		if s != 0 && c.bySSRC[s] == cam {
			delete(c.bySSRC, s)
		}
	}
}

func (c *cameras) dropLocked(cam *camera) {
	c.unindex(cam)
	delete(c.byUser, cam.user)
}

// params is what the receiver of user's camera decrypts and sends with.
func (c *cameras) params(user discord.UserID) VideoReceiverParams {
	s := c.session
	return VideoReceiverParams{
		User:         user,
		Decrypt:      func(au, out []byte) (int, error) { return s.DecryptFrame(user, au, out) },
		MaxDecrypted: func(n int) int { return s.MaxDecryptedFrameSize(user, n) },
		SendRTCP:     s.WriteRTCP,
		Ours:         s.SSRC,
	}
}

// left stops the camera of a user who left the voice channel.
func (c *cameras) left(user discord.UserID) { c.set(user, 0, 0) }

// reset stops every camera, as when leaving or connecting anew: the voice
// server tells the cameras on again, with their new SSRCs.
func (c *cameras) reset() {
	c.mu.Lock()
	var closing []VideoReceiver
	for _, cam := range c.byUser {
		closing = append(closing, cam.recv)
	}
	c.byUser = make(map[discord.UserID]*camera)
	c.bySSRC = make(map[uint32]*camera)
	c.sent = ""
	c.mu.Unlock()
	for _, r := range closing {
		r.Close()
	}
	if len(closing) > 0 && c.changed != nil {
		c.changed()
	}
}

// receiver is the receiver of user's camera, nil while it is off.
func (c *cameras) receiver(user discord.UserID) VideoReceiver {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cam := c.byUser[user]; cam != nil {
		return cam.recv
	}
	return nil
}

// any reports whether a camera is on.
func (c *cameras) any() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byUser) > 0
}

// handle takes the packets of the voice connection that are not Opus: the
// video of a camera goes to its receiver, anything else is dropped. All
// count for transport feedback while a camera is on. It runs on the
// goroutine that reads the connection, and must not block.
func (c *cameras) handle(p *udp.MediaPacket) bool {
	c.mu.Lock()
	if len(c.byUser) > 0 && c.tw != nil && len(p.Extension) > 0 {
		c.tw.Observe(p.ExtensionProfile(), p.Extension, time.Now())
	}
	pt := p.PayloadType()
	if pt == payloadOpus {
		c.mu.Unlock()
		return false
	}
	cam := c.bySSRC[p.SSRC()]
	var recv VideoReceiver
	if cam != nil && (pt == payloadH264 || pt == payloadH264RTX) {
		recv = cam.recv
	}
	c.mu.Unlock()
	if recv != nil && pt == payloadH264 {
		if q, ok := videoOrientation(p); ok {
			if rr, ok := recv.(interface{ SetRotation(int) }); ok {
				rr.SetRotation(q)
			}
		}
	}
	if recv != nil && len(p.Payload) > 0 {
		recv.Push(p.SSRC(), p.Sequence(), p.Timestamp(), p.Marker(), p.Payload)
	}
	return true
}

// run sends the wants as the tiles change, and transport feedback, until
// done.
func (c *cameras) run(done <-chan struct{}) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-done:
			return
		case <-t.C:
		}
		c.feedback()
		if n%3 == 0 {
			c.sendWants(false)
		}
	}
}

// feedback sends transport feedback while a camera is on.
func (c *cameras) feedback() {
	c.mu.Lock()
	var media uint32
	for _, cam := range c.byUser {
		media = cam.video
		break
	}
	var pkt []byte
	if media != 0 && c.tw != nil {
		pkt = c.tw.Feedback(c.session.SSRC(), media)
	}
	c.mu.Unlock()
	if pkt != nil {
		c.session.WriteRTCP(pkt)
	}
}

// sendWants sends the media sink wants when they changed, or every 5
// seconds while a camera is on, or with force.
func (c *cameras) sendWants(force bool) {
	now := time.Now()
	c.mu.Lock()
	if len(c.byUser) == 0 && c.sent == "" {
		c.mu.Unlock()
		return
	}
	cams := make([]cameraWant, 0, len(c.byUser))
	for _, cam := range c.byUser {
		px := cam.recv.Pixels()
		if px > 0 {
			cam.hidden = time.Time{}
		} else if cam.hidden.IsZero() {
			cam.hidden = now
		}
		cams = append(cams, cameraWant{video: cam.video, pixels: px, hidden: cam.hidden})
	}
	wants := sinkWants(c.ladder, cams, now)
	b, _ := json.Marshal(wants)
	key := string(b)
	if key == c.sent && !force && (len(c.byUser) == 0 || now.Sub(c.sentAt) < 5*time.Second) {
		c.mu.Unlock()
		return
	}
	if key == c.sent && force {
		c.mu.Unlock()
		return
	}
	c.sent, c.sentAt = key, now
	if len(c.byUser) == 0 {
		// Nothing more to ask for once the last camera stopped.
		c.sent = ""
	}
	c.mu.Unlock()
	cmd := voicegateway.MediaSinkWantsCommand(wants)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.session.Send(ctx, &cmd); err != nil {
		slog.Debug("could not send media sink wants", "err", err)
	}
}

// extVideoOrientation is the ID the voice server gives the
// urn:3gpp:video-orientation header extension of camera video.
const extVideoOrientation = 4

// videoOrientation reads the quarter turns clockwise a picture is to be
// shown at from p's one-byte header extensions, if it has them.
func videoOrientation(p *udp.MediaPacket) (int, bool) {
	if p.ExtensionProfile() != 0xBEDE {
		return 0, false
	}
	e := p.Extension
	for i := 0; i < len(e); {
		if e[i] == 0 {
			i++
			continue
		}
		id, n := int(e[i]>>4), int(e[i]&0x0F)+1
		if id == 15 || i+1+n > len(e) {
			return 0, false
		}
		if id == extVideoOrientation {
			return int(e[i+1] & 3), true
		}
		i += 1 + n
	}
	return 0, false
}
