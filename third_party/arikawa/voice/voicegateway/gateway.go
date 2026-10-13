//
// For the brave souls who get this far: You are the chosen ones,
// the valiant knights of programming who toil away, without rest,
// fixing our most awful code.  To you, true saviors, kings of men,
// I say this: never gonna give you up, never gonna let you down,
// never gonna run around and desert you.  Never gonna make you cry,
// never gonna say goodbye.  Never gonna tell a lie and hurt you.
//

package voicegateway

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/disgoorg/godave"
)

// Version represents the current version of the Discord Gateway Gateway this package uses.
const Version = "9"

var (
	ErrNoSessionID = errors.New("no sessionID was received")
	ErrNoEndpoint  = errors.New("no endpoint was received")
)

// State contains state information of a voice gateway.
type State struct {
	UserID    discord.UserID  // constant
	GuildID   discord.GuildID // constant
	ChannelID discord.ChannelID

	SessionID string
	Token     string
	Endpoint  string
}

// Gateway represents a Discord Gateway Gateway connection.
type Gateway struct {
	gateway *ws.Gateway
	state   State // constant

	mutex sync.RWMutex
	ready *ReadyEvent
	// seq is the sequence number of the last event received.
	seq int64

	dave  godave.Session
	ssrcs map[uint32]discord.UserID // guarded by mutex
}

// DefaultGatewayOpts contains the default options to be used for connecting to
// the gateway.
var DefaultGatewayOpts = ws.GatewayOpts{
	ReconnectDelay: func(try int) time.Duration {
		// minimum 4 seconds
		return time.Duration(4+(2*try)) * time.Second
	},
	// FatalCloseCodes contains the default gateway close codes that will cause
	// the gateway to exit. In other words, it's a list of unrecoverable close
	// codes.
	FatalCloseCodes: []int{
		4003, // not authenticated
		4004, // authentication failed
		4006, // session invalid
		4009, // session timed out
		4011, // server not found
		4012, // unknown protocol
		4014, // disconnected
		4016, // unknown encryption mode
		4017, // E2EE required
		4021, // rate limited
		4022, // call terminated
	},
	DialTimeout:           0,
	ReconnectAttempt:      0,
	AlwaysCloseGracefully: true,
}

// New creates a new voice gateway that encrypts end to end using a session
// from dave, or does not if dave is nil.
func New(state State, dave godave.SessionCreateFunc) *Gateway {
	if dave == nil {
		dave = godave.NewNoopSession
	}

	// https://discord.com/developers/docs/topics/voice-connections#establishing-a-voice-websocket-connection
	endpoint := "wss://" + strings.TrimSuffix(state.Endpoint, ":80") + "/?v=" + Version

	g := &Gateway{
		gateway: ws.NewGateway(ws.NewWebsocket(ws.NewCodec(OpUnmarshalers), endpoint), &DefaultGatewayOpts),
		state:   state,
		ssrcs:   make(map[uint32]discord.UserID),
	}
	g.dave = dave(slog.Default(), godave.UserID(state.UserID.String()), (*daveCallbacks)(g))
	g.dave.SetChannelID(godave.ChannelID(state.ChannelID))
	return g
}

// DAVE returns the session that encrypts end to end.
func (g *Gateway) DAVE() godave.Session {
	return g.dave
}

// User returns the user that sends with the given SSRC, or 0 if it is not known.
func (g *Gateway) User(ssrc uint32) discord.UserID {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	return g.ssrcs[ssrc]
}

// daveCallbacks is what the DAVE session sends with.
type daveCallbacks Gateway

func (g *daveCallbacks) SendMLSKeyPackage(b []byte) error {
	return g.gateway.Send(context.Background(), new(MLSKeyPackageCommand(b)))
}

func (g *daveCallbacks) SendMLSCommitWelcome(b []byte) error {
	return g.gateway.Send(context.Background(), new(MLSCommitWelcomeCommand(b)))
}

func (g *daveCallbacks) SendReadyForTransition(id uint16) error {
	return g.gateway.Send(context.Background(), &DAVETransitionReadyCommand{TransitionID: id})
}

func (g *daveCallbacks) SendInvalidCommitWelcome(id uint16) error {
	return g.gateway.Send(context.Background(), &MLSInvalidCommitWelcomeCommand{TransitionID: id})
}

// Ready returns the ready event.
func (g *Gateway) Ready() *ReadyEvent {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	return g.ready
}

// LastError returns the last error that the gateway has received. It only
// returns a valid error if the gateway's event loop as exited. If the event
// loop hasn't been started AND stopped, the function will panic.
func (g *Gateway) LastError() error {
	return g.gateway.LastError()
}

// Send is a function to send an Op payload to the Gateway.
func (g *Gateway) Send(ctx context.Context, data ws.Event) error {
	return g.gateway.Send(ctx, data)
}

// Speaking sends a Speaking operation (opcode 5) to the Gateway Gateway.
func (g *Gateway) Speaking(ctx context.Context, flag SpeakingFlag) error {
	g.mutex.RLock()
	ready := g.ready
	g.mutex.RUnlock()

	if ready == nil {
		return errors.New("Speaking called before gateway was ready")
	}

	return g.gateway.Send(ctx, &SpeakingEvent{
		Speaking: flag,
		Delay:    0,
		SSRC:     ready.SSRC,
	})
}

func (g *Gateway) Connect(ctx context.Context) <-chan ws.Op {
	return g.gateway.Connect(ctx, (*gatewayImpl)(g))
}

var (
	// ErrMissingForIdentify is an error when we are missing information to
	// identify.
	ErrMissingForIdentify = errors.New("missing GuildID, UserID, SessionID, or Token for identify")
	// ErrMissingForResume is an error when we are missing information to
	// resume.
	ErrMissingForResume = errors.New("missing GuildID, SessionID, or Token for resuming")
)

type gatewayImpl Gateway

// serverID returns the guild ID, or the channel ID for a private channel.
func (g *gatewayImpl) serverID() discord.GuildID {
	if g.state.GuildID.IsValid() {
		return g.state.GuildID
	}
	return discord.GuildID(g.state.ChannelID)
}

func (g *gatewayImpl) sendIdentify(ctx context.Context) error {
	id := IdentifyCommand{
		GuildID:   g.serverID(),
		ChannelID: g.state.ChannelID,
		UserID:    g.state.UserID,
		SessionID: g.state.SessionID,
		Token:     g.state.Token,
	}
	if !id.GuildID.IsValid() || id == (IdentifyCommand{}) {
		return ErrMissingForIdentify
	}
	id.MaxDAVEProtocolVersion = g.dave.MaxSupportedProtocolVersion()

	return g.gateway.Send(ctx, &id)
}

func (g *gatewayImpl) sendResume(ctx context.Context) error {
	if !g.serverID().IsValid() || g.state.SessionID == "" || g.state.Token == "" {
		return ErrMissingForResume
	}

	return g.gateway.Send(ctx, &ResumeCommand{
		GuildID:   g.serverID(),
		ChannelID: g.state.ChannelID,
		SessionID: g.state.SessionID,
		Token:     g.state.Token,
		SeqAck:    g.seq,
	})
}

func (g *gatewayImpl) OnOp(ctx context.Context, op ws.Op) bool {
	if op.Sequence != 0 {
		g.seq = op.Sequence
	}

	switch data := op.Data.(type) {
	case *HelloEvent:
		g.gateway.ResetHeartbeat(data.HeartbeatInterval.Duration())

		// Send Discord either the Identify packet (if it's a fresh
		// connection), or a Resume packet (if it's a dead connection).
		if g.ready == nil {
			// SessionID is empty, so this is a completely new session.
			if err := g.sendIdentify(ctx); err != nil {
				g.gateway.SendErrorWrap(err, "failed to send identify")
				g.gateway.QueueReconnect()
			}
		} else {
			if err := g.sendResume(ctx); err != nil {
				g.gateway.SendErrorWrap(err, "failed to send resume")
				g.gateway.QueueReconnect()
			}
		}
	case *ReadyEvent:
		g.mutex.Lock()
		g.ready = data
		g.mutex.Unlock()
		g.dave.AssignSsrcToCodec(data.SSRC, godave.CodecOpus)
	case *SpeakingEvent:
		g.mutex.Lock()
		g.ssrcs[data.SSRC] = data.UserID
		g.mutex.Unlock()

	case *SessionDescriptionEvent:
		g.dave.OnSelectProtocolAck(data.DAVEProtocolVersion)
	case *ClientsConnectEvent:
		for _, id := range data.UserIDs {
			g.dave.AddUser(godave.UserID(id.String()))
		}
	case *ClientDisconnectEvent:
		g.dave.RemoveUser(godave.UserID(data.UserID.String()))
	case *DAVEPrepareTransitionEvent:
		g.dave.OnDavePrepareTransition(data.TransitionID, data.ProtocolVersion)
	case *DAVEExecuteTransitionEvent:
		g.dave.OnDaveExecuteTransition(data.TransitionID)
	case *DAVEPrepareEpochEvent:
		g.dave.OnDavePrepareEpoch(data.Epoch, data.ProtocolVersion)
	case *MLSExternalSenderEvent:
		g.dave.OnDaveMLSExternalSenderPackage(*data)
	case *MLSProposalsEvent:
		g.dave.OnDaveMLSProposals(*data)
	case *MLSAnnounceCommitEvent:
		g.dave.OnDaveMLSPrepareCommitTransition(data.TransitionID, data.Commit)
	case *MLSWelcomeEvent:
		g.dave.OnDaveMLSWelcome(data.TransitionID, data.Welcome)
	}

	return true
}

func (g *gatewayImpl) SendHeartbeat(ctx context.Context) {
	heartbeat := HeartbeatCommand{T: time.Now().UnixMilli(), SeqAck: g.seq}
	if err := g.gateway.Send(ctx, &heartbeat); err != nil {
		g.gateway.SendErrorWrap(err, "heartbeat error")
		g.gateway.QueueReconnect()
	}
}

func (g *gatewayImpl) Close() error {
	return g.dave.Close()
}
