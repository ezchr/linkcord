// Package golive shares a screen or a window in a voice channel, as
// Discord's Go Live does: it captures it with GDI, which Windows 7 has,
// encodes it to H264 with Cisco's OpenH264, encrypts it end to end with
// DAVE, and sends it to the stream server that Discord gives.
package golive

import (
	"context"
	"errors"
	"image"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// SourceKind tells a screen from a window.
type SourceKind int

const (
	Screen SourceKind = iota
	Window
)

// Source is a screen or a window that can be shared.
type Source struct {
	Kind SourceKind
	// Window is the window shared, for a window.
	Window uintptr
	// X, Y, W and H are where a screen is on the desktop; W and H are also
	// the size of a window.
	X, Y, W, H int
	Primary    bool
	Title      string
	// Thumb is a picture of what the source shows, if it could be taken.
	Thumb *image.RGBA
}

// Quality is how a stream is sent: Height lines high, 0 for the source's,
// FPS pictures a second.
type Quality struct {
	Height, FPS int
}

// size returns the size of the pictures sent of a source w by h: as high as
// q asks and no higher than the source, with its proportions, in even
// numbers as H264 needs.
func (q Quality) size(w, h int) (int, int) {
	if q.Height > 0 && h > q.Height {
		w = w * q.Height / h
		h = q.Height
	}
	return max(w&^1, 16), max(h&^1, 16)
}

// bitrate is the bits a second to send pictures of w by h at q's rate:
// screens compress well, so about a tenth of a bit a pixel, between half a
// megabit and eight.
func (q Quality) bitrate(w, h int) int {
	b := w * h * max(q.FPS, 1) / 10
	return min(max(b, 500_000), 8_000_000)
}

// Main is the session of the main gateway that a stream is started on.
type Main interface {
	SendGateway(ctx context.Context, ev ws.Event) error
}

// Params is what a stream needs.
type Params struct {
	Main Main
	// Subscribe adds a handler of events of the main gateway, a func taking
	// a *StreamCreateEvent, *StreamServerUpdateEvent, *StreamUpdateEvent or
	// *StreamDeleteEvent, and returns what removes it.
	Subscribe func(handler any) (rm func())
	// GuildID and ChannelID are the voice channel streamed to, which the
	// user is in; GuildID is not valid for a call.
	GuildID   discord.GuildID
	ChannelID discord.ChannelID
	UserID    discord.UserID
	// SessionID is the session of the main gateway.
	SessionID string
	Source    Source
	Quality   Quality
	// Sound sends the sound of the source along, until SetSound says not.
	Sound bool
	// CodecDir is where OpenH264 is kept.
	CodecDir string
	// OnEnd is called once the stream ended by itself, with why.
	OnEnd func(err error)
}

// ErrSourceGone is why a stream ends when the window shared is closed.
var ErrSourceGone = errors.New("the window shared was closed")
