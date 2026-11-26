//go:build windows && !no_voice

package golive

import "time"

// transportFeedback is the transport-wide congestion control feedback of
// the stream viewer, for a voice connection; its caller serializes it.
type transportFeedback struct{ t *twcc }

func newTransportFeedback() *transportFeedback { return &transportFeedback{t: newTWCC()} }

func (f *transportFeedback) Observe(profile uint16, ext []byte, now time.Time) {
	f.t.observe(profile, ext, now)
}

func (f *transportFeedback) Feedback(sender, media uint32) []byte {
	return f.t.feedback(sender, media)
}
