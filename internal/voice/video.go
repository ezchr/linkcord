package voice

import (
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

// VideoReceiver receives the video of one user of the voice channel, their
// camera. The voice session gives it the packets of the user's video and
// of its resends; what decodes and shows it is up to the VideoBackend.
type VideoReceiver interface {
	// SetSSRCs sets the SSRCs of the video and of its resends.
	SetSSRCs(video, rtx uint32)
	// Push gives a packet of the video or of its resends; it is copied.
	// It must not block.
	Push(ssrc uint32, seq uint16, ts uint32, marker bool, payload []byte)
	// Pixels is how many pixels the video is shown in, 0 when it is not
	// shown.
	Pixels() int
	// Close stops receiving.
	Close()
}

// VideoReceiverParams is what a VideoReceiver is made with: the session
// decrypts and sends for it.
type VideoReceiverParams struct {
	User discord.UserID
	// Decrypt removes the end-to-end encryption of a picture of User into
	// out, which MaxDecrypted(len(au)) bytes fit.
	Decrypt      func(au, out []byte) (int, error)
	MaxDecrypted func(size int) int
	// SendRTCP sends an RTCP packet on the voice connection, which seals
	// it; Ours is the SSRC of this end, which it is from.
	SendRTCP func(pkt []byte) error
	Ours     func() uint32
}

// TransportFeedback keeps which packets of the voice connection arrived
// when, by the transport-wide sequence number of their header extension, for
// the RTCP transport feedback the senders of video adapt to.
type TransportFeedback interface {
	Observe(profile uint16, ext []byte, now time.Time)
	// Feedback is the feedback to send for what arrived since the last, nil
	// for none.
	Feedback(sender, media uint32) []byte
}

// VideoBackend decodes and shows the cameras of a voice channel. Without
// one, the voice connection takes audio only.
type VideoBackend interface {
	NewVideoReceiver(p VideoReceiverParams) VideoReceiver
	NewTransportFeedback() TransportFeedback
}

// The payload types the voice connection is told for Opus and H264, as
// Discord's clients number them, and as the stream viewer has them.
const (
	payloadOpus    = 120
	payloadH264    = 103
	payloadH264RTX = 104
)
