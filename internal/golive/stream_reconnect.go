//go:build windows && !no_voice

package golive

import (
	"context"
	"math/big"
	"time"

	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
)

// A lost stream server is dialed again, as Discord's client does, at most
// reconnectTries times in reconnectWithin; each waits up to reconnectWait
// for Discord to give a server and as long to join it.
const (
	reconnectTries  = 3
	reconnectWithin = 2 * time.Minute
	reconnectWait   = 15 * time.Second
)

// daveChannelOf is what the end-to-end encryption of the stream of ce is
// keyed to: its RTC channel, which older servers did not give; it was then
// the server's ID less one.
func daveChannelOf(ce *StreamCreateEvent) *big.Int {
	ch := new(big.Int)
	if _, ok := ch.SetString(ce.RTCChannelID, 10); !ok {
		ch.SetString(ce.RTCServerID, 10)
		ch.Sub(ch, big.NewInt(1))
	}
	return ch
}

// allowReconnect reports whether one more reconnect at now fits in the
// limit, given when the last ones were, and returns them with it added.
func allowReconnect(past []time.Time, now time.Time) ([]time.Time, bool) {
	kept := past[:0]
	for _, t := range past {
		if now.Sub(t) < reconnectWithin {
			kept = append(kept, t)
		}
	}
	if len(kept) >= reconnectTries {
		return kept, false
	}
	return append(kept, now), true
}

// reconnect dials the stream server again after its connection was lost,
// keeping the capture and the encoder going: it asks Discord for the server
// with a stream ping, joins the one given and starts it with a keyframe. It
// reports whether the stream goes on. It runs on watch's goroutine.
func (s *Stream) reconnect() bool {
	if s.stopped.Load() {
		return false
	}
	var ok bool
	if s.reconnects, ok = allowReconnect(s.reconnects, time.Now()); !ok {
		s.log.Warn("stream server lost too often, not reconnecting")
		return false
	}
	s.reconnecting.Store(true)
	defer s.reconnecting.Store(false)
	s.log.Info("stream server lost, reconnecting", "try", len(s.reconnects))

	ctx, cancel := context.WithTimeout(context.Background(), reconnectWait)
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Only what Discord gives after the ping.
	select {
	case <-s.servers:
	default:
	}
	select {
	case <-s.created:
	default:
	}
	if err := s.p.Main.SendGateway(ctx, &streamPingCommand{StreamKey: s.key}); err != nil {
		s.log.Warn("could not ping the stream", "err", err)
		return false
	}
	var se *StreamServerUpdateEvent
	select {
	case se = <-s.servers:
	case <-ctx.Done():
		s.log.Warn("Discord did not give a stream server again")
		return false
	}
	select {
	case ce := <-s.created:
		s.rtcServer, s.daveChannel = ce.RTCServerID, daveChannelOf(ce)
	default:
	}

	wait, cancelWait := context.WithTimeout(context.Background(), reconnectWait)
	defer cancelWait()
	go func() {
		select {
		case <-s.done:
			cancelWait()
		case <-wait.Done():
		}
	}()
	c, ops, med, connCancel, err := s.connect(wait, s.loopCtx, se)
	if err != nil {
		s.log.Warn("could not reconnect to the stream server", "err", err)
		return false
	}

	oldMed, oldCancel := s.m(), s.connCancel
	s.connP.Store(c)
	s.medP.Store(med)
	s.ops, s.connCancel = ops, connCancel
	if oldMed != nil {
		oldMed.close()
	}
	if oldCancel != nil {
		oldCancel()
	}
	// Stopped while reconnecting: teardown closed what it saw.
	select {
	case <-s.done:
		med.close()
		connCancel()
		return false
	default:
	}

	s.mu.Lock()
	clear(s.connected)
	s.mu.Unlock()
	c.send(wait, &voicegateway.SpeakingEvent{Speaking: voicegateway.Soundshare, SSRC: c.ready.SSRC})
	if w, h := int(s.sentW.Load()), int(s.sentH.Load()); w > 0 && h > 0 {
		if err := s.sendVideo(wait, c, w, h); err != nil {
			s.log.Warn("could not tell the stream server of the stream", "err", err)
			return false
		}
	}
	s.requestKeyframe()
	s.log.Info("stream server reconnected", "endpoint", se.Endpoint)
	return true
}
