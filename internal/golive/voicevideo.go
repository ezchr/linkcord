package golive

import (
	"log/slog"

	"github.com/ezchr/linkcord/internal/voice"
)

// Cameras shows the cameras of a voice channel: it is the voice session's
// VideoBackend, each camera received by a VideoReceiver as a stream's
// video is.
type Cameras struct {
	// OnFrame is called, from another goroutine, when a camera has a new
	// picture.
	OnFrame func()
}

// NewVideoReceiver receives the camera of p.User.
func (c Cameras) NewVideoReceiver(p voice.VideoReceiverParams) voice.VideoReceiver {
	return NewVideoReceiver(VideoParams{
		Log:          slog.Default().With("camera", p.User),
		Decrypt:      p.Decrypt,
		MaxDecrypted: p.MaxDecrypted,
		SendRTCP:     p.SendRTCP,
		Ours:         p.Ours,
		OnFrame:      c.OnFrame,
	})
}

// NewTransportFeedback keeps which packets arrived when, for the senders
// of video to adapt to.
func (Cameras) NewTransportFeedback() voice.TransportFeedback { return newTransportFeedback() }
