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
func (*Stream) Viewers() []discord.UserID { return nil }
func (*Stream) Stop()                     {}

func (*Stream) Sound() bool   { return false }
func (*Stream) SetSound(bool) {}
