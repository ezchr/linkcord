package gui

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

// client_uuid is laid out as the web client's generator writes it.
func TestClientUUID(t *testing.T) {
	g := clientUUID{prefix: 0xAABBCCDD, created: 1700000000000}
	b, err := base64.StdEncoding.DecodeString(g.generate(discord.UserID(123456789012345678)))
	if err != nil || len(b) != 24 {
		t.Fatalf("decode: %v, %d bytes", err, len(b))
	}
	if binary.LittleEndian.Uint64(b[0:]) != 123456789012345678 || binary.LittleEndian.Uint32(b[8:]) != 0xAABBCCDD ||
		binary.LittleEndian.Uint64(b[12:]) != 1700000000000 || binary.LittleEndian.Uint32(b[20:]) != 0 {
		t.Fatalf("layout: %x", b)
	}
	b2, _ := base64.StdEncoding.DecodeString(g.generate(1))
	if binary.LittleEndian.Uint32(b2[20:]) != 1 {
		t.Fatal("sequence number did not advance")
	}
}

// Events carry the standard properties, numbered, and are throttled by key.
func TestScienceTrack(t *testing.T) {
	s := &science{last: map[string]time.Time{}, started: time.Now(), sessionID: "sess", wake: make(chan struct{}, 1), userID: 5}
	s.track("channel_opened", map[string]any{"channel_id": "1"}, "k", time.Minute)
	s.track("channel_opened", map[string]any{"channel_id": "1"}, "k", time.Minute)
	s.track("app_opened", nil, "", 0)
	if len(s.queue) != 2 {
		t.Fatalf("throttle: %d events queued", len(s.queue))
	}
	p := s.queue[0].Properties
	for _, k := range []string{"client_track_timestamp", "client_heartbeat_session_id", "event_sequence_number", "client_uuid", "rendered_locale", "uptime_app", "cpu_core_count"} {
		if _, ok := p[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if p["channel_id"] != "1" || s.queue[1].Properties["event_sequence_number"] != 2 {
		t.Fatalf("properties: %v", p)
	}
	s.off = func() bool { return true }
	s.track("app_opened", nil, "", 0)
	if len(s.queue) != 2 {
		t.Fatal("tracked while turned off")
	}
}
