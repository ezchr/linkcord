package golive

import (
	"fmt"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// The events and commands of Go Live on the main gateway, which Arikawa does
// not know.
//
// https://docs.discord.food/topics/gateway-events#stream-create

// StreamCreateEvent is sent when a stream starts, to its streamer and to
// those who watch it, with the stream server to connect to.
type StreamCreateEvent struct {
	StreamKey   string `json:"stream_key"`
	RTCServerID string `json:"rtc_server_id"`
	// RTCChannelID is what the stream's end-to-end encryption is keyed to,
	// and what the stream server is told.
	RTCChannelID string           `json:"rtc_channel_id"`
	Region       string           `json:"region"`
	ViewerIDs    []discord.UserID `json:"viewer_ids"`
	Paused       bool             `json:"paused"`
}

// StreamServerUpdateEvent gives the address and token of a stream server.
type StreamServerUpdateEvent struct {
	StreamKey string `json:"stream_key"`
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
}

// StreamUpdateEvent is sent when who watches a stream changes.
type StreamUpdateEvent struct {
	StreamKey string           `json:"stream_key"`
	ViewerIDs []discord.UserID `json:"viewer_ids"`
	Paused    bool             `json:"paused"`
}

// StreamDeleteEvent is sent when a stream ends.
type StreamDeleteEvent struct {
	StreamKey   string `json:"stream_key"`
	Reason      string `json:"reason"`
	Unavailable bool   `json:"unavailable"`
}

func (*StreamCreateEvent) Op() ws.OpCode                 { return 0 }
func (*StreamCreateEvent) EventType() ws.EventType       { return "STREAM_CREATE" }
func (*StreamServerUpdateEvent) Op() ws.OpCode           { return 0 }
func (*StreamServerUpdateEvent) EventType() ws.EventType { return "STREAM_SERVER_UPDATE" }
func (*StreamUpdateEvent) Op() ws.OpCode                 { return 0 }
func (*StreamUpdateEvent) EventType() ws.EventType       { return "STREAM_UPDATE" }
func (*StreamDeleteEvent) Op() ws.OpCode                 { return 0 }
func (*StreamDeleteEvent) EventType() ws.EventType       { return "STREAM_DELETE" }

func init() {
	// Before any gateway is made, as each copies the registry.
	gateway.OpUnmarshalers.Add(
		func() ws.Event { return new(StreamCreateEvent) },
		func() ws.Event { return new(StreamServerUpdateEvent) },
		func() ws.Event { return new(StreamUpdateEvent) },
		func() ws.Event { return new(StreamDeleteEvent) },
	)
}

// streamCreateCommand starts a stream in the voice channel the user is in.
type streamCreateCommand struct {
	Type            string            `json:"type"`
	GuildID         *discord.GuildID  `json:"guild_id"`
	ChannelID       discord.ChannelID `json:"channel_id"`
	PreferredRegion *string           `json:"preferred_region"`
}

// streamDeleteCommand ends a stream.
type streamDeleteCommand struct {
	StreamKey string `json:"stream_key"`
}

// streamSetPausedCommand pauses a stream or resumes it.
type streamSetPausedCommand struct {
	StreamKey string `json:"stream_key"`
	Paused    bool   `json:"paused"`
}

func (*streamCreateCommand) Op() ws.OpCode              { return 18 }
func (*streamCreateCommand) EventType() ws.EventType    { return "" }
func (*streamDeleteCommand) Op() ws.OpCode              { return 19 }
func (*streamDeleteCommand) EventType() ws.EventType    { return "" }
func (*streamSetPausedCommand) Op() ws.OpCode           { return 22 }
func (*streamSetPausedCommand) EventType() ws.EventType { return "" }

// StreamKey names the stream of userID in the voice channel channelID of
// guildID, or of the call channelID if guildID is not valid.
func StreamKey(guildID discord.GuildID, channelID discord.ChannelID, userID discord.UserID) string {
	if guildID.IsValid() {
		return fmt.Sprintf("guild:%d:%d:%d", guildID, channelID, userID)
	}
	return fmt.Sprintf("call:%d:%d", channelID, userID)
}
