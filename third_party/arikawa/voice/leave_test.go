package voice

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// silentSession is a MainSession that Discord never answers.
type silentSession struct{ sent []discord.ChannelID }

func (s *silentSession) SendGateway(_ context.Context, m ws.Event) error {
	s.sent = append(s.sent, m.(*gateway.UpdateVoiceStateCommand).ChannelID)
	return nil
}

func (s *silentSession) Channel(id discord.ChannelID) (*discord.Channel, error) {
	return &discord.Channel{ID: id, GuildID: 2}, nil
}

func (s *silentSession) Me() (*discord.User, error) { return &discord.User{ID: 3}, nil }

func TestLeaveAfterFailedJoin(t *testing.T) {
	main := &silentSession{}
	s := NewSessionCustom(main, handler.New(), 3)
	s.WSMaxRetry, s.WSRetryDelay, s.WSWaitDuration = 1, 0, 10*time.Millisecond

	if err := s.JoinChannel(context.Background(), 1, true, false); err == nil {
		t.Fatal("joined without an answer from Discord")
	}

	if err := s.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}

	if want := []discord.ChannelID{1, discord.NullChannelID}; !slices.Equal(main.sent, want) {
		t.Fatalf("sent %v, want %v", main.sent, want)
	}
}
