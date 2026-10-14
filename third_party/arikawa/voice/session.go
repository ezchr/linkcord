package voice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/session"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/utils/ws/ophandler"
	"github.com/ayn2op/arikawa/v3/voice/udp"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/disgoorg/godave"
)

// Silence is the Opus frame of silence. Five of them should be written after
// what is said, for it not to be interpolated with what is said next.
//
// https://docs.discord.food/topics/voice-connections#voice-data-interpolation
const Silence = "\xF8\xFF\xFE"

// ErrAlreadyConnecting is returned when the session is already connecting.
var ErrAlreadyConnecting = errors.New("already connecting")

// ErrCannotSend is an error when audio is sent to a closed channel.
var ErrCannotSend = errors.New("cannot send audio to closed channel")

// WSTimeout is the duration to wait for a gateway operation including Session
// to complete before erroring out. This only applies to functions that don't
// take in a context already.
const WSTimeout = 25 * time.Second

// ReconnectError is emitted into Session.Handler everytime the voice gateway
// fails to be reconnected. It implements the error interface.
type ReconnectError struct {
	Err error
}

// Error implements error.
func (e ReconnectError) Error() string {
	return "voice reconnect error: " + e.Err.Error()
}

// Unwrap returns e.Err.
func (e ReconnectError) Unwrap() error { return e.Err }

var (
	_ MainSession = (*session.Session)(nil)
	_ MainSession = (*state.State)(nil)
)

// MainSession abstracts both session.Session and state.State.
type MainSession interface {
	// Me returns the current user.
	Me() (*discord.User, error)
	// Channel queries for the channel with the given ID.
	Channel(discord.ChannelID) (*discord.Channel, error)
	// SendGateway is a helper to send messages over the gateway.
	SendGateway(ctx context.Context, m ws.Event) error
}

// Session is a single voice session that wraps around the voice gateway and UDP
// connection.
type Session struct {
	*handler.Handler
	session MainSession
	events  *handler.Handler

	mut sync.RWMutex
	// connected is a non-nil blocking channel after Join is called and is
	// closed once Leave is called.
	disconnected chan struct{}

	state voicegateway.State // guarded except UserID

	detachReconnect []func()

	// udpManager is the manager for a UDP connection. The user can use this to
	// plug in a custom UDP dialer.
	udpManager *udp.Manager

	// DAVE creates the session that encrypts end to end for each channel
	// joined. Discord needs it for all voice channels but stage channels. It
	// is any implementation of godave, and nil by default, which does not
	// encrypt.
	DAVE godave.SessionCreateFunc
	// sendBuf and recvBuf are what frames are encrypted and decrypted into.
	sendBuf, recvBuf []byte

	gateway  *voicegateway.Gateway
	gwCancel context.CancelFunc
	gwDone   <-chan struct{}
	// speaking is what Speaking told Discord last, told every new voice
	// gateway again: one that Discord moves the call to knows nothing of it,
	// and the others hear nothing until it does.
	speaking voicegateway.SpeakingFlag

	WSTimeout      time.Duration // global WSTimeout
	WSMaxRetry     int           // 2
	WSRetryDelay   time.Duration // 2s
	WSWaitDuration time.Duration // 5s

	// joining determines the behavior of incoming event callbacks (Update).
	// If this is true, incoming events will just send into Updated channels. If
	// false, events will trigger a reconnection.
	joining atomic.Bool
	// disconnectClosed is true if connected is already closed. It is only used
	// to keep track of closing connected.
	disconnectClosed bool
}

// NewSession creates a new voice session for the current user. events is the handler that the main session dispatches gateway events to.
func NewSession(state MainSession, events *handler.Handler) (*Session, error) {
	u, err := state.Me()
	if err != nil {
		return nil, fmt.Errorf("failed to get me: %w", err)
	}

	return NewSessionCustom(state, events, u.ID), nil
}

// NewSessionCustom creates a new voice session from the given session, its event handler and user ID.
func NewSessionCustom(ses MainSession, events *handler.Handler, userID discord.UserID) *Session {
	closed := make(chan struct{})
	close(closed)

	session := &Session{
		Handler: handler.New(),
		session: ses,
		events:  events,
		state: voicegateway.State{
			UserID: userID,
		},
		udpManager:     udp.NewManager(),
		WSTimeout:      WSTimeout,
		WSMaxRetry:     2,
		WSRetryDelay:   2 * time.Second,
		WSWaitDuration: 5 * time.Second,

		// Set this pair of value in so we never have to nil-check the channel.
		// We can just assume that it's either closed or connected.
		disconnected:     closed,
		disconnectClosed: true,
	}

	return session
}

// SetUDPDialer sets the given dialer to be used for dialing UDP voice
// connections.
func (s *Session) SetUDPDialer(d udp.DialFunc) {
	s.udpManager.SetDialer(d)
}

func (s *Session) acquireUpdate(f func()) {
	if s.joining.Load() {
		return
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	// Ignore if we haven't connected yet or we're still joining.
	if s.udpManager.IsClosed() {
		return
	}

	f()
}

// updateServer is specifically used to monitor for reconnects.
func (s *Session) updateServer(ev *gateway.VoiceServerUpdateEvent) {
	s.acquireUpdate(func() {
		if s.state.GuildID != ev.GuildID || ev.Endpoint == "" {
			return
		}

		s.state.Endpoint = ev.Endpoint
		s.state.Token = ev.Token

		ctx, cancel := context.WithTimeout(context.Background(), WSTimeout)
		defer cancel()

		s.reconnectCtx(ctx)
	})
}

// updateState is specifically used after connecting to monitor when the bot is
// forced across channels or out of them.
func (s *Session) updateState(ev *gateway.VoiceStateUpdateEvent) {
	s.acquireUpdate(func() {
		if s.state.GuildID != ev.GuildID || s.state.UserID != ev.UserID {
			return
		}
		// Disconnected by someone else, or by connecting from another client.
		if !ev.ChannelID.IsValid() || ev.SessionID != s.state.SessionID {
			s.ensureClosed()
			return
		}

		// Moved to another channel, which a voice server update follows that
		// reconnects. Anything else that changed, such as being muted, is no
		// reason to.
		s.state.ChannelID = ev.ChannelID
	})
}

// JoinChannelAndSpeak is a convenient function that calls JoinChannel then
// Speaking.
func (s *Session) JoinChannelAndSpeak(ctx context.Context, chID discord.ChannelID, mute, deaf bool) error {
	if err := s.JoinChannel(ctx, chID, mute, deaf); err != nil {
		return fmt.Errorf("cannot join channel: %w", err)
	}
	return s.Speaking(ctx, voicegateway.Microphone)
}

type waitEventChs struct {
	serverUpdate chan *gateway.VoiceServerUpdateEvent
	stateUpdate  chan *gateway.VoiceStateUpdateEvent
}

// JoinChannel joins the given voice channel with the default timeout.
func (s *Session) JoinChannel(ctx context.Context, chID discord.ChannelID, mute, deaf bool) error {
	var ch *discord.Channel

	if chID.IsValid() {
		var err error
		ch, err = s.session.Channel(chID)
		if err != nil {
			return fmt.Errorf("invalid channel ID: %w", err)
		}
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	// Error out if we're already joining. JoinChannel shouldn't be called
	// concurrently.
	if !s.joining.CompareAndSwap(false, true) {
		return errors.New("JoinChannel working elsewhere")
	}

	defer s.joining.Store(false)

	// Set the state.
	if ch != nil {
		s.state.ChannelID = ch.ID
		s.state.GuildID = ch.GuildID
	} else {
		s.state.GuildID = 0
		// Ensure that if `cID` is zero that it passes null to the update event.
		s.state.ChannelID = discord.NullChannelID
	}

	// What a previous join got is of no use to this one: Discord sends a new
	// session ID, token and endpoint.
	s.state.SessionID, s.state.Token, s.state.Endpoint = "", "", ""

	if s.detachReconnect == nil {
		s.detachReconnect = []func(){
			s.events.AddHandler(s.updateServer),
			s.events.AddHandler(s.updateState),
		}
	}

	chs := waitEventChs{
		serverUpdate: make(chan *gateway.VoiceServerUpdateEvent),
		stateUpdate:  make(chan *gateway.VoiceStateUpdateEvent),
	}

	// Bind the handlers.
	cancels := []func(){
		s.events.AddChanHandler(chs.serverUpdate),
		s.events.AddChanHandler(chs.stateUpdate),
	}
	// Disconnects the handlers once the function exits.
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	// Ensure gateway and voiceUDP are already closed.
	s.ensureClosed()

	// https://discord.com/developers/docs/topics/voice-connections#retrieving-voice-server-information
	// Send a Voice State Update event to the gateway.
	data := &gateway.UpdateVoiceStateCommand{
		GuildID:   s.state.GuildID,
		ChannelID: s.state.ChannelID,
		SelfMute:  mute,
		SelfDeaf:  deaf,
	}

	var err error
	var timer *time.Timer

	// Retry 3 times maximum.
	for i := 0; i < s.WSMaxRetry; i++ {
		if err = s.askDiscord(ctx, data, chs); err == nil {
			break
		}

		// If this is the first attempt and the context timed out, it's
		// probably the context that's waiting for gateway events. Retry the
		// loop.
		if i == 0 && errors.Is(err, ctx.Err()) {
			continue
		}

		if timer == nil {
			// Set up a timer.
			timer = time.NewTimer(s.WSRetryDelay)
			defer timer.Stop()
		} else {
			timer.Reset(s.WSRetryDelay)
		}

		select {
		case <-timer.C:
			continue
		case <-ctx.Done():
			return fmt.Errorf("cannot ask Discord for events: %w", err)
		}
	}

	if err != nil {
		return fmt.Errorf("cannot ask Discord for events: %w", err)
	}

	// These 2 methods should've updated s.state before sending into these
	// channels. Since s.state is already filled, we can go ahead and connect.

	// Mark the session as connected and move on. This allows one of the
	// connected handlers to reconnect on its own.
	s.disconnected = make(chan struct{})

	return s.reconnectCtx(ctx)
}

func (s *Session) askDiscord(
	ctx context.Context, data *gateway.UpdateVoiceStateCommand, chs waitEventChs) error {

	// https://discord.com/developers/docs/topics/voice-connections#retrieving-voice-server-information
	// Send a Voice State Update event to the gateway.
	if err := s.session.SendGateway(ctx, data); err != nil {
		return fmt.Errorf("failed to send Voice State Update event: %w", err)
	}

	// Wait for 2 replies. The above command should reply with these 2 events.
	if err := s.waitForIncoming(ctx, chs); err != nil {
		return fmt.Errorf("failed to wait for needed gateway events: %w", err)
	}

	return nil
}

func (s *Session) waitForIncoming(ctx context.Context, chs waitEventChs) error {
	ctx, cancel := context.WithTimeout(ctx, s.WSWaitDuration)
	defer cancel()

	state := false
	// server is true when we already have the token and endpoint, meaning that
	// we don't have to wait for another such event.
	server := s.state.Token != "" && s.state.Endpoint != ""

	// Loop until timeout or until we have all the information that we need.
	for !(server && state) {
		select {
		case ev := <-chs.serverUpdate:
			slog.Info("voice join: server update", "guild", ev.GuildID, "want_guild", s.state.GuildID, "endpoint", ev.Endpoint != "")
			// An empty endpoint means the voice server is being reallocated,
			// and another event follows once it is.
			if s.state.GuildID != ev.GuildID || ev.Endpoint == "" {
				continue
			}
			s.state.Endpoint = ev.Endpoint
			s.state.Token = ev.Token
			server = true

		case ev := <-chs.stateUpdate:
			if s.state.UserID == ev.UserID {
				slog.Info("voice join: state update", "guild", ev.GuildID, "want_guild", s.state.GuildID, "channel", ev.ChannelID)
			}
			if s.state.GuildID != ev.GuildID || s.state.UserID != ev.UserID {
				continue
			}
			s.state.SessionID = ev.SessionID
			s.state.ChannelID = ev.ChannelID
			state = true

		case <-ctx.Done():
			slog.Info("voice join: gave up waiting", "have_server", server, "have_state", state)
			return ctx.Err()
		}
	}

	return nil
}

// reconnect uses the current state to reconnect to a new gateway and UDP
// connection.
func (s *Session) reconnectCtx(ctx context.Context) error {
	ws.WSDebug("Sending stop handle.")

	if err := s.udpManager.Pause(ctx); err != nil {
		return fmt.Errorf("cannot pause UDP manager: %w", err)
	}
	defer func() {
		if !s.udpManager.Continue() {
			panic("UDP manager continued but invalid lock ownership")
		}
	}()

	s.ensureClosed()

	ws.WSDebug("Start gateway.")
	s.gateway = voicegateway.New(s.state, s.DAVE)

	// Open the voice gateway. The function will block until Ready is received.
	gwctx, gwcancel := context.WithCancel(context.Background())
	s.gwCancel = gwcancel

	gwch := s.gateway.Connect(gwctx)
	ws.WSDebug("Voice Gateway connected")

	if err := s.spinGateway(ctx, gwch); err != nil {
		ws.WSDebug("Voice spinGateway error:", err)
		// Early cancel the gateway.
		gwcancel()
		// Nil this so future reconnects don't use the invalid gwDone.
		s.gwCancel = nil
		// Emit the error. It's fine to do this here since this is the only
		// place that can error out.
		s.Handler.Call(&ReconnectError{err})
		return fmt.Errorf("cannot wait for event sequence from voice gateway: %w", err)
	}

	// Start dispatching.
	s.gwDone = ophandler.Loop(gwch, s.Handler)

	if s.speaking != 0 {
		if err := s.gateway.Speaking(ctx, s.speaking); err != nil {
			ws.WSDebug("Voice could not tell the new gateway it speaks:", err)
		}
	}

	ws.WSDebug("Voice reconnectCtx finished with no error")

	return nil
}

func (s *Session) spinGateway(ctx context.Context, gwch <-chan ws.Op) error {
	var err error
	var conn *udp.Connection

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-gwch:
			if !ok {
				return fmt.Errorf("voice gateway error: %w", s.gateway.LastError())
			}

			switch data := ev.Data.(type) {
			case *ws.CloseEvent:
				return fmt.Errorf("voice gateway error: %w", data)

			case *voicegateway.ReadyEvent:
				ws.WSDebug("Got ready from voice gateway, SSRC:", data.SSRC)

				// Use the encryption mode that we prefer of those offered.
				modes := udp.Modes()
				mode := slices.IndexFunc(modes, func(mode string) bool { return slices.Contains(data.Modes, mode) })
				if mode < 0 {
					return fmt.Errorf("no supported encryption mode in %q", data.Modes)
				}

				// Prepare the UDP voice connection.
				conn, err = s.udpManager.Dial(ctx, data.Addr(), data.SSRC)
				if err != nil {
					return fmt.Errorf("failed to open voice UDP connection: %w", err)
				}

				if err := s.gateway.Send(ctx, &voicegateway.SelectProtocolCommand{
					Protocol: "udp",
					Data: voicegateway.SelectProtocolData{
						Address: conn.GatewayIP,
						Port:    conn.GatewayPort,
						Mode:    modes[mode],
					},
				}); err != nil {
					return fmt.Errorf("failed to send SelectProtocolCommand: %w", err)
				}

			case *voicegateway.SessionDescriptionEvent:
				if conn == nil {
					return errors.New("server bug: SessionDescription before Ready")
				}

				ws.WSDebug("Received secret key from voice gateway")

				// We're done.
				return conn.UseSecret(data.Mode, data.SecretKey)
			}

			// Dispatch this event to the handler.
			s.Handler.Call(ev.Data)
		}
	}
}

// Speaking tells Discord we're speaking. This method should not be called
// concurrently.
//
// If only NotSpeaking (0) is given, then even if the gateway cannot be reached,
// a nil error will be returned. This is because sending Discord a not-speaking
// event is a destruction command that doesn't affect the outcome of anything
// done after whatsoever.
func (s *Session) Speaking(ctx context.Context, flag voicegateway.SpeakingFlag) error {
	s.mut.Lock()
	gateway := s.gateway
	s.speaking = flag
	s.mut.Unlock()

	if err := gateway.Speaking(ctx, flag); err != nil && flag != 0 {
		return err
	}

	return nil
}

// Write writes into the UDP voice connection. This method is thread safe as far
// as calling other methods of Session goes; HOWEVER it is not thread safe to
// call Write itself concurrently.
func (s *Session) Write(b []byte) (int, error) {
	s.mut.RLock()
	gateway := s.gateway
	s.mut.RUnlock()

	if gateway == nil || gateway.Ready() == nil {
		return 0, ErrCannotSend
	}
	dave := gateway.DAVE()

	// Until the others in the channel can decrypt what is sent, send silence
	// in its place, which is not encrypted and keeps the pace.
	frame := b
	if !dave.Ready() {
		frame = []byte(Silence)
	}

	s.sendBuf = slices.Grow(s.sendBuf[:0], dave.MaxEncryptedFrameSize(len(frame)))
	n, err := dave.Encrypt(gateway.Ready().SSRC, frame, s.sendBuf[:cap(s.sendBuf)])
	if err != nil {
		return 0, fmt.Errorf("failed to encrypt end to end: %w", err)
	}
	if _, err := s.udpManager.Write(s.sendBuf[:n]); err != nil {
		return 0, err
	}
	return len(b), nil
}

// ReadPacket reads a single packet from the UDP connection. This is NOT at all
// thread safe, and must be used very carefully. The backing buffer is always
// reused.
func (s *Session) ReadPacket() (*udp.Packet, error) {
	p, err := s.udpManager.ReadPacket()
	if err != nil {
		return nil, err
	}

	s.mut.RLock()
	gateway := s.gateway
	s.mut.RUnlock()

	dave := gateway.DAVE()
	user := godave.UserID(gateway.User(p.SSRC()).String())

	s.recvBuf = slices.Grow(s.recvBuf[:0], dave.MaxDecryptedFrameSize(user, len(p.Opus)))
	n, err := dave.Decrypt(user, p.Opus, s.recvBuf[:cap(s.recvBuf)])
	if err != nil {
		return nil, fmt.Errorf("%w end to end for SSRC %d: %w", udp.ErrDecryptionFailed, p.SSRC(), err)
	}
	p.Opus = s.recvBuf[:n]
	return p, nil
}

// Latency returns how long the voice server took to answer the last ping that
// ReadPacket read the answer to, or 0 if it has read none.
func (s *Session) Latency() time.Duration {
	return s.udpManager.Latency()
}

// User returns the user that sends the packets with the given SSRC, or 0 if it
// is not known yet.
func (s *Session) User(ssrc uint32) discord.UserID {
	s.mut.RLock()
	defer s.mut.RUnlock()

	if s.gateway == nil {
		return 0
	}
	return s.gateway.User(ssrc)
}

// Leave disconnects the current voice session from the currently connected
// channel.
func (s *Session) Leave(ctx context.Context) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.ensureClosed()

	// Unbind the handlers.
	if s.detachReconnect != nil {
		for _, detach := range s.detachReconnect {
			detach()
		}
		s.detachReconnect = nil
	}

	// Notify Discord that we're leaving, even if we never connected: it may
	// have us in the channel regardless when joining failed.
	sendErr := s.session.SendGateway(ctx, &gateway.UpdateVoiceStateCommand{
		GuildID:   s.state.GuildID,
		ChannelID: discord.ChannelID(discord.NullSnowflake),
		SelfMute:  true,
		SelfDeaf:  true,
	})

	// Wait for the gateway to exit first before we tell the user of the gateway
	// send error.
	if err := s.cancelGateway(ctx); err != nil {
		return err
	}

	if sendErr != nil {
		return fmt.Errorf("failed to update voice state: %w", sendErr)
	}

	return nil
}

func (s *Session) cancelGateway(ctx context.Context) error {
	if s.gwCancel != nil {
		s.gwCancel()
		s.gwCancel = nil

		// Wait for the previous gateway to finish closing up, but make sure to
		// bail if the context expires.
		if err := ophandler.WaitForDone(ctx, s.gwDone); err != nil {
			return fmt.Errorf("cannot wait for gateway to close: %w", err)
		}
	}

	return nil
}

// close ensures everything is closed. It does not acquire the mutex.
func (s *Session) ensureClosed() {
	// Disconnect the UDP connection. If not permanent, then pause.
	s.udpManager.Close()

	if s.gwCancel != nil {
		s.gwCancel()
		// Don't actually clear this field, because we still want the caller to
		// be able to wait for the gateway to completely exit using
		// cancelGateway.
	}
}
