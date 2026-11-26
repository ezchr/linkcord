//go:build !no_voice

package voice

import (
	"errors"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

func newTestSession() *Session {
	return &Session{
		proc:     newProcessor(),
		echo:     newEcho(),
		updates:  make(chan struct{}, 1),
		mic:      make(chan []byte, 4),
		frames:   make(chan []byte, 8),
		speaking: make(map[discord.UserID]bool),
		heardAt:  make(map[discord.UserID]time.Time),
		speakers: make(map[uint32]*speaker),
		volumes:  make(map[discord.UserID]float32),
		lost:     make(chan error, 1),
	}
}

// TestLostAgainEndsCall checks that a connection lost again right after it
// was got back ends the call, which Lost tells, rather than leave it
// joined on a dead connection.
func TestLostAgainEndsCall(t *testing.T) {
	recoverDelay = 10 * time.Millisecond
	s := newTestSession()
	s.setStatus(Status{ChannelID: 5, Joined: true})
	s.recoveredAt.Store(time.Now().UnixNano())

	if s.Connected() {
		t.Fatal("a call with no connection is taken for connected")
	}
	s.connectionLost(errors.New("voice gateway stopped"))
	select {
	case err := <-s.Lost():
		if err == nil {
			t.Fatal("lost with no reason")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the call that lost its connection was not ended")
	}
	if st := s.Status(); st.Joined || st.ChannelID.IsValid() {
		t.Fatalf("status %+v after the call ended, want none", st)
	}
}

// TestLostAfterLeaveIgnored checks that the connection closing once the
// user left, or Discord moved them out, does nothing.
func TestLostAfterLeaveIgnored(t *testing.T) {
	recoverDelay = 10 * time.Millisecond
	s := newTestSession()
	s.connectionLost(errors.New("closed"))
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-s.Lost():
		t.Fatalf("lost %v with no call", err)
	default:
	}
}
