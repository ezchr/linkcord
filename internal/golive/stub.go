//go:build !windows || no_voice

package golive

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

// errUnsupported is why streams do not start where GDI or the C compiler
// is missing.
var errUnsupported = errors.New("golive: screen sharing needs Windows and a build with voice")

// Stream is a screen or a window being shared. There is none here.
type Stream struct{}

// Sources finds nothing to share.
func Sources(self uintptr, thumbW int) []Source { return nil }

// Start fails.
func Start(ctx context.Context, p Params) (*Stream, error) { return nil, errUnsupported }

func (*Stream) Preview() *image.RGBA      { return nil }
func (*Stream) SetPreviewWidth(int)       {}
func (*Stream) Viewers() []discord.UserID { return nil }
func (*Stream) Stop()                     {}

func (*Stream) Sound() bool   { return false }
func (*Stream) SetSound(bool) {}

// WatchParams is what watching someone's stream needs.
type WatchParams struct {
	Main       Main
	Subscribe  func(handler any) (rm func())
	GuildID    discord.GuildID
	ChannelID  discord.ChannelID
	StreamerID discord.UserID
	UserID     discord.UserID
	SessionID  string
	FFmpeg     string
	NeedFFmpeg func() (string, error)
	OnFrame    func()
	OnEnd      func(err error)
}

// InProcessDecoding reports whether streams decode without FFmpeg: never
// here.
func InProcessDecoding() bool { return false }

// Watch is someone's stream being watched. There is none here.
type Watch struct{}

// StartWatching fails.
func StartWatching(ctx context.Context, p WatchParams) (*Watch, error) { return nil, errUnsupported }

func (*Watch) Info() (int, int, int)    { return 0, 0, 0 }
func (*Watch) Frame() *image.RGBA       { return nil }
func (*Watch) Streamer() discord.UserID { return 0 }
func (*Watch) Stop()                    {}

func (*Watch) SetView(int, int)            {}
func (*Watch) SetAudioVolume(float32)      {}
func (*Watch) AudioVolume() float32        { return 1 }
func (*Watch) SetAudioMuted(bool)          {}
func (*Watch) AudioMuted() bool            { return false }
func (*Watch) SetClipLength(time.Duration) {}
func (*Watch) SaveClip(context.Context, string, time.Duration, string) error {
	return errors.New("streams cannot be watched in this build")
}

// VideoParams is what receiving one sender's video needs.
type VideoParams struct {
	Log          *slog.Logger
	Decrypt      func(au, out []byte) (int, error)
	MaxDecrypted func(size int) int
	SendRTCP     func(pkt []byte) error
	Ours         func() uint32
	OnFrame      func()
	NoDecoder    bool
	OnPicture    func(pic []byte)
}

// VideoReceiver receives a video; nothing is decoded here.
type VideoReceiver struct {
	video, rtx uint32
	view       uint64
	viewAt     time.Time
	closed     bool
}

// NewVideoReceiver returns a receiver that shows nothing.
func NewVideoReceiver(p VideoParams) *VideoReceiver { return &VideoReceiver{} }

func (r *VideoReceiver) SetSSRCs(video, rtx uint32)              { r.video, r.rtx = video, rtx }
func (r *VideoReceiver) SSRCs() (uint32, uint32)                 { return r.video, r.rtx }
func (*VideoReceiver) Push(uint32, uint16, uint32, bool, []byte) {}
func (r *VideoReceiver) SetView(w, h int) {
	r.view, r.viewAt = uint64(max(w, 0))<<32|uint64(max(h, 0)), time.Now()
}
func (r *VideoReceiver) Shown() bool {
	return !r.viewAt.IsZero() && time.Since(r.viewAt) < 2*time.Second
}
func (r *VideoReceiver) Pixels() int {
	if !r.Shown() {
		return 0
	}
	return int(r.view>>32) * int(r.view&0xFFFFFFFF)
}
func (*VideoReceiver) Frame() *image.RGBA           { return nil }
func (*VideoReceiver) SharedFrame() *SharedFrame    { return nil }
func (*VideoReceiver) Info() (int, int, int64)      { return 0, 0, 0 }
func (*VideoReceiver) Unavailable() bool            { return true }
func (*VideoReceiver) Stats() (int64, int64, int64) { return 0, 0, 0 }
func (r *VideoReceiver) Closed() bool               { return r.closed }
func (r *VideoReceiver) Close()                     { r.closed = true }

// transportFeedback tells nothing here.
type transportFeedback struct{}

func newTransportFeedback() *transportFeedback                  { return &transportFeedback{} }
func (*transportFeedback) Observe(uint16, []byte, time.Time)    {}
func (*transportFeedback) Feedback(sender, media uint32) []byte { return nil }
