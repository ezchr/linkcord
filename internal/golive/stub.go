//go:build !windows || no_voice

package golive

import (
	"context"
	"errors"
	"image"

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
	OnFrame    func()
	OnEnd      func(err error)
}

// Watch is someone's stream being watched. There is none here.
type Watch struct{}

// StartWatching fails.
func StartWatching(ctx context.Context, p WatchParams) (*Watch, error) { return nil, errUnsupported }

func (*Watch) Info() (int, int, int)    { return 0, 0, 0 }
func (*Watch) Frame() *image.RGBA       { return nil }
func (*Watch) Streamer() discord.UserID { return 0 }
func (*Watch) Stop()                    {}
