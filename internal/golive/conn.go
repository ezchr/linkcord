package golive

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
)

// The voice gateway of a stream server. It is the voice gateway of voice
// channels, identified for video, which Arikawa's does not do.
//
// https://docs.discord.food/topics/voice-connections

// streamInfo is a stream that a client asks to send.
type streamInfo struct {
	Type    string `json:"type"`
	RID     string `json:"rid"`
	Quality int    `json:"quality"`
}

// identifyCommand is Op 0 for a stream server.
type identifyCommand struct {
	ServerID               string         `json:"server_id"`
	ChannelID              string         `json:"channel_id,omitempty"`
	UserID                 discord.UserID `json:"user_id"`
	SessionID              string         `json:"session_id"`
	Token                  string         `json:"token"`
	Video                  bool           `json:"video"`
	Streams                []streamInfo   `json:"streams,omitempty"`
	MaxDAVEProtocolVersion int            `json:"max_dave_protocol_version"`
}

// readyStream is a stream that the server is ready to take, with the SSRCs
// to send it with.
type readyStream struct {
	Type    string `json:"type"`
	RID     string `json:"rid"`
	SSRC    uint32 `json:"ssrc"`
	RTXSSRC uint32 `json:"rtx_ssrc"`
	Quality int    `json:"quality"`
	Active  bool   `json:"active"`
}

// readyEvent is Op 2, with the streams.
type readyEvent struct {
	SSRC    uint32        `json:"ssrc"`
	IP      string        `json:"ip"`
	Port    int           `json:"port"`
	Modes   []string      `json:"modes"`
	Streams []readyStream `json:"streams"`
}

// sessionDescriptionEvent is Op 4, with the codecs the server chose.
type sessionDescriptionEvent struct {
	voicegateway.SessionDescriptionEvent
	AudioCodec     string `json:"audio_codec"`
	VideoCodec     string `json:"video_codec"`
	MediaSessionID any    `json:"media_session_id"`
}

func (*sessionDescriptionEvent) Op() ws.OpCode           { return 4 }
func (*sessionDescriptionEvent) EventType() ws.EventType { return "" }

// codecInfo is a codec that the client can send and receive.
type codecInfo struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Priority       int    `json:"priority"`
	PayloadType    int    `json:"payload_type"`
	RTXPayloadType int    `json:"rtx_payload_type,omitempty"`
	Encode         bool   `json:"encode,omitempty"`
	Decode         bool   `json:"decode,omitempty"`
}

// The payload types of Opus and H264, as Discord's clients number them now.
const (
	payloadOpus    = 120
	payloadH264    = 103
	payloadH264RTX = 104
)

// selectProtocolCommand is Op 1 with the codecs, as Discord's clients send
// it: the address also outside data, a connection ID and experiments.
type selectProtocolCommand struct {
	Protocol        string                          `json:"protocol"`
	Codecs          []codecInfo                     `json:"codecs"`
	Data            voicegateway.SelectProtocolData `json:"data"`
	Address         string                          `json:"address"`
	Port            uint16                          `json:"port"`
	Mode            string                          `json:"mode"`
	RTCConnectionID string                          `json:"rtc_connection_id"`
	Experiments     []string                        `json:"experiments"`
}

// versionCommand is Op 16, which asks the server its version, as clients do
// before they send media.
type versionCommand struct{}

func (*versionCommand) Op() ws.OpCode           { return 16 }
func (*versionCommand) EventType() ws.EventType { return "" }

// videoStream is what is sent of a stream.
type videoStream struct {
	Type          string        `json:"type"`
	RID           string        `json:"rid"`
	SSRC          uint32        `json:"ssrc"`
	Active        bool          `json:"active"`
	Quality       int           `json:"quality"`
	RTXSSRC       uint32        `json:"rtx_ssrc"`
	MaxBitrate    int           `json:"max_bitrate"`
	MaxFramerate  int           `json:"max_framerate"`
	MaxResolution maxResolution `json:"max_resolution"`
}

type maxResolution struct {
	Type   string `json:"type"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// videoCommand is Op 12: what video the client sends.
type videoCommand struct {
	AudioSSRC uint32        `json:"audio_ssrc"`
	VideoSSRC uint32        `json:"video_ssrc"`
	RTXSSRC   uint32        `json:"rtx_ssrc"`
	Streams   []videoStream `json:"streams"`
}

func (*identifyCommand) Op() ws.OpCode                 { return 0 }
func (*identifyCommand) EventType() ws.EventType       { return "" }
func (*readyEvent) Op() ws.OpCode                      { return 2 }
func (*readyEvent) EventType() ws.EventType            { return "" }
func (*selectProtocolCommand) Op() ws.OpCode           { return 1 }
func (*selectProtocolCommand) EventType() ws.EventType { return "" }
func (*videoCommand) Op() ws.OpCode                    { return 12 }
func (*videoCommand) EventType() ws.EventType          { return "" }

// unmarshalers are the voice gateway's, with the Ready that has streams.
var unmarshalers = func() ws.OpUnmarshalers {
	m := ws.NewOpUnmarshalers()
	voicegateway.OpUnmarshalers.Each(func(_ ws.OpCode, _ ws.EventType, fn ws.OpFunc) bool {
		m.Add(fn)
		return false
	})
	m.Add(func() ws.Event { return new(readyEvent) })
	m.Add(func() ws.Event { return new(sessionDescriptionEvent) })
	return m
}()

// conn is the connection to a stream server's gateway.
type conn struct {
	gw       *ws.Gateway
	identify identifyCommand
	dave     *session.Session

	mu    sync.Mutex
	ready *readyEvent
	seq   int64
}

// dial connects to the stream server at endpoint, identifying with id.
// channelID is what the end-to-end encryption is keyed to.
func dial(ctx context.Context, endpoint string, id identifyCommand, channelID uint64, log *slog.Logger) (*conn, <-chan ws.Op) {
	c := &conn{identify: id}
	c.dave = session.New(godave.UserID(id.UserID.String()), (*daveCallbacks)(c), session.WithLogger(log))
	c.dave.SetChannelID(godave.ChannelID(channelID))
	c.identify.MaxDAVEProtocolVersion = c.dave.MaxSupportedProtocolVersion()
	url := "wss://" + strings.TrimSuffix(endpoint, ":80") + "/?v=" + voicegateway.Version
	// A local stream server, as the loopback test runs, is plain ws.
	if strings.HasPrefix(endpoint, "ws://") {
		url = endpoint + "/?v=" + voicegateway.Version
	}
	c.gw = ws.NewGateway(ws.NewWebsocket(ws.NewCodec(unmarshalers), url), &voicegateway.DefaultGatewayOpts)
	return c, c.gw.Connect(ctx, (*connHandler)(c))
}

func (c *conn) send(ctx context.Context, ev ws.Event) error {
	return c.gw.Send(ctx, ev)
}

// daveCallbacks is what DAVE sends with.
type daveCallbacks conn

func (c *daveCallbacks) SendMLSKeyPackage(b []byte) error {
	return c.gw.Send(context.Background(), new(voicegateway.MLSKeyPackageCommand(b)))
}

func (c *daveCallbacks) SendMLSCommitWelcome(b []byte) error {
	return c.gw.Send(context.Background(), new(voicegateway.MLSCommitWelcomeCommand(b)))
}

func (c *daveCallbacks) SendReadyForTransition(id uint16) error {
	return c.gw.Send(context.Background(), &voicegateway.DAVETransitionReadyCommand{TransitionID: id})
}

func (c *daveCallbacks) SendInvalidCommitWelcome(id uint16) error {
	return c.gw.Send(context.Background(), &voicegateway.MLSInvalidCommitWelcomeCommand{TransitionID: id})
}

// connHandler handles the gateway's ops, as Arikawa's voice gateway does.
type connHandler conn

var errNoIdentify = errors.New("golive: missing what to identify with")

func (c *connHandler) OnOp(ctx context.Context, op ws.Op) bool {
	c.mu.Lock()
	if op.Sequence != 0 {
		c.seq = op.Sequence
	}
	ready := c.ready
	seq := c.seq
	c.mu.Unlock()

	switch data := op.Data.(type) {
	case *voicegateway.HelloEvent:
		c.gw.ResetHeartbeat(data.HeartbeatInterval.Duration())
		var err error
		if ready == nil {
			if c.identify.ServerID == "" || c.identify.Token == "" {
				err = errNoIdentify
			} else {
				err = c.gw.Send(ctx, &c.identify)
			}
		} else {
			err = c.gw.Send(ctx, &voicegateway.ResumeCommand{
				GuildID:   discord.GuildID(mustSnowflake(c.identify.ServerID)),
				SessionID: c.identify.SessionID,
				Token:     c.identify.Token,
				SeqAck:    seq,
			})
		}
		if err != nil {
			c.gw.SendErrorWrap(err, "golive: could not identify")
			c.gw.QueueReconnect()
		}
	case *readyEvent:
		c.mu.Lock()
		c.ready = data
		c.mu.Unlock()
		c.dave.AssignSsrcToCodec(data.SSRC, godave.CodecOpus)
	case *sessionDescriptionEvent:
		c.dave.OnSelectProtocolAck(data.DAVEProtocolVersion)
	case *voicegateway.ClientsConnectEvent:
		for _, id := range data.UserIDs {
			c.dave.AddUser(godave.UserID(id.String()))
		}
	case *voicegateway.ClientDisconnectEvent:
		c.dave.RemoveUser(godave.UserID(data.UserID.String()))
	case *voicegateway.DAVEPrepareTransitionEvent:
		c.dave.OnDavePrepareTransition(data.TransitionID, data.ProtocolVersion)
	case *voicegateway.DAVEExecuteTransitionEvent:
		c.dave.OnDaveExecuteTransition(data.TransitionID)
	case *voicegateway.DAVEPrepareEpochEvent:
		c.dave.OnDavePrepareEpoch(data.Epoch, data.ProtocolVersion)
	case *voicegateway.MLSExternalSenderEvent:
		c.dave.OnDaveMLSExternalSenderPackage(*data)
	case *voicegateway.MLSProposalsEvent:
		c.dave.OnDaveMLSProposals(*data)
	case *voicegateway.MLSAnnounceCommitEvent:
		c.dave.OnDaveMLSPrepareCommitTransition(data.TransitionID, data.Commit)
	case *voicegateway.MLSWelcomeEvent:
		c.dave.OnDaveMLSWelcome(data.TransitionID, data.Welcome)
	}

	return true
}

func (c *connHandler) SendHeartbeat(ctx context.Context) {
	c.mu.Lock()
	seq := c.seq
	c.mu.Unlock()
	if err := c.gw.Send(ctx, &voicegateway.HeartbeatCommand{T: time.Now().UnixMilli(), SeqAck: seq}); err != nil {
		c.gw.SendErrorWrap(err, "golive: heartbeat error")
		c.gw.QueueReconnect()
	}
}

func (c *connHandler) Close() error {
	return c.dave.Close()
}

func mustSnowflake(s string) discord.Snowflake {
	sf, _ := discord.ParseSnowflake(s)
	return sf
}
