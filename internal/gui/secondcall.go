package gui

import (
	"context"
	"log/slog"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/session"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	clientgateway "github.com/ezchr/linkcord/internal/gateway"
	"github.com/ezchr/linkcord/internal/dcsound"
	"github.com/ezchr/linkcord/internal/http"
	"github.com/ezchr/linkcord/internal/voice"
)

// Being in two voice channels at once: Discord lets each connection be in
// one, as it lets a phone and a computer each be in a call. The second call
// has a connection of its own, which keeps nothing of the account, as only
// its voice is needed, and is closed with the call.

// secondCall is the second voice channel the user is in, if any.
type secondCall struct {
	st       *state.State
	session  *voice.Session
	channel  discord.Channel
	joining  bool
	leaveBtn widget.Clickable
}

// voiceOnlyState is a connection of the user's that keeps nothing of the
// account: its voice is all that is used.
func voiceOnlyState(token string) *state.State {
	id := gateway.NewIdentifier(gateway.IdentifyCommand{
		Token:      token,
		Properties: http.IdentifyProperties(),
		Capabilities: gateway.LazyUserNotes |
			gateway.VersionedReadStates |
			gateway.VersionedUserGuildSetttings |
			gateway.DedupeUserObjects |
			gateway.PrioritizedReadyPayload,
	})
	sess := session.NewWithGateway(clientgateway.New(id), handler.New())
	sess.Client = http.NewClient(token)
	st := state.NewFromSession(sess, store.NoopCabinet)
	st.OnRequest = append(st.OnRequest, httputil.WithHeaders(http.Headers()))
	return st
}

// inSecondCall reports whether ch is the channel of the second call.
func (u *ui) inSecondCall(ch discord.ChannelID) bool {
	sc := u.second
	return sc != nil && sc.channel.ID == ch
}

// wantsSecondCall reports whether joining ch, while in the voice channel
// joined, goes in a second call: a call of a direct message or group while
// in a server, or the other way around, or any channel with Shift held.
func (u *ui) wantsSecondCall(ch discord.Channel, shift bool) bool {
	cur := u.voiceChannel()
	if !cur.IsValid() || cur == ch.ID || u.chat == nil || u.chat.demo {
		return false
	}
	if shift {
		return true
	}
	if c, err := u.chat.st.Cabinet.Channel(cur); err == nil {
		return c.GuildID.IsValid() != ch.GuildID.IsValid()
	}
	return false
}

// joinSecondCall joins ch in the second call, leaving the second call's
// channel before, if any.
func (c *chatView) joinSecondCall(ch discord.Channel) {
	u := c.u
	if err := c.joinable(ch); err != nil {
		u.voiceFailed("Could not join "+ch.Name, err)
		return
	}
	u.leaveSecondCall()
	sc := &secondCall{channel: ch, joining: true}
	u.second = sc
	settings := u.voiceSettings()
	muted, deafened := u.voice.muted, u.voice.deafened
	token := u.token
	go func() {
		st := voiceOnlyState(token)
		// What the connection hears of voice, for when a join fails.
		st.AddHandler(func(ev *gateway.VoiceStateUpdateEvent) {
			slog.Info("second call voice state", "channel", ev.ChannelID, "guild", ev.GuildID, "user", ev.UserID)
		})
		st.AddHandler(func(ev *gateway.VoiceServerUpdateEvent) {
			slog.Info("second call voice server", "guild", ev.GuildID, "endpoint", ev.Endpoint != "")
		})
		err := st.Open(context.Background())
		slog.Info("second call connection", "err", err)
		var s *voice.Session
		if err == nil && !ch.GuildID.IsValid() {
			// As Discord's app, this connection asks about the call first,
			// or Discord does not tell it where the call's voice is.
			if cerr := st.SendGateway(context.Background(), &callConnectCommand{ChannelID: ch.ID}); cerr != nil {
				slog.Warn("could not ask about the call", "err", cerr)
			}
		}
		if err == nil {
			s = voice.New(st, settings.sensitivity())
			if derr := s.SetDevices(settings.Input, settings.Output); derr != nil {
				slog.Warn("could not choose the audio devices", "err", derr)
			}
			err = s.Join(ch.ID)
			if err == nil {
				err = s.SetMuteDeafen(muted, deafened)
			}
		}
		u.post(func() {
			if u.second != sc {
				// Left meanwhile.
				go closeSecondCall(st, s)
				return
			}
			sc.joining = false
			if err != nil {
				u.second = nil
				go closeSecondCall(st, s)
				u.voiceFailed("Could not join "+ch.Name, err)
				return
			}
			sc.st, sc.session = st, s
			u.playSound(dcsound.UserJoin)
			go func() {
				for range s.Updates() {
					u.win.Invalidate()
				}
			}()
		})
	}()
}

// leaveSecondCall leaves the second call, if any, and closes its
// connection.
func (u *ui) leaveSecondCall() {
	sc := u.second
	if sc == nil {
		return
	}
	u.second = nil
	if sc.session != nil {
		u.playSound(dcsound.Disconnect)
	}
	go closeSecondCall(sc.st, sc.session)
}

func closeSecondCall(st *state.State, s *voice.Session) {
	if s != nil {
		if err := s.Leave(); err != nil {
			slog.Warn("could not leave the second call", "err", err)
		}
	}
	if st != nil {
		st.Close()
	}
}

// applySecondCall gives the second call the mute and deafen of the buttons.
func (u *ui) applySecondCall() {
	sc := u.second
	if sc == nil || sc.session == nil {
		return
	}
	s := sc.session
	muted, deafened := u.voice.muted, u.voice.deafened
	go func() {
		if err := s.SetMuteDeafen(muted, deafened); err != nil {
			slog.Warn("could not mute the second call", "err", err)
		}
	}()
}

// layoutSecondCall is the line of the second call above the voice panel:
// its channel, and a button to leave it.
func (c *chatView) layoutSecondCall(gtx layout.Context) layout.Dimensions {
	u := c.u
	sc := u.second
	if sc == nil {
		return layout.Dimensions{}
	}
	th := u.th
	if sc.leaveBtn.Clicked(gtx) {
		u.leaveSecondCall()
		return layout.Dimensions{}
	}
	name := sc.channel.Name
	if !sc.channel.GuildID.IsValid() {
		name = c.dmName(sc.channel)
	}
	status, col := "Also connected", colorGreen
	if sc.joining {
		status, col = "Connecting…", colorYellow
	}
	return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(oneLine(bold(label(th, 13, col, status))).Layout),
					layout.Rigid(oneLine(label(th, 12, colorMuted, name)).Layout))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &sc.leaveBtn, iconHangUp, colorMuted, colorRed)
			}))
	})
}
