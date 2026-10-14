package gui

import (
	"image"
	"math"
	"slices"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ezchr/linkcord/internal/dcsound"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconCall = mustIcon(icons.CommunicationCall)

// cosTurn is the cosine of t turns.
func cosTurn(t float64) float64 { return math.Cos(2 * math.Pi * t) }

// call is a call in a direct message or group, as Discord tells of them:
// who it rings.
type call struct {
	ChannelID   discord.ChannelID    `json:"channel_id"`
	MessageID   discord.MessageID    `json:"message_id"`
	Ringing     []discord.UserID     `json:"ringing"`
	VoiceStates []discord.VoiceState `json:"voice_states"`
}

type callCreateEvent struct{ call }
type callUpdateEvent struct{ call }
type callDeleteEvent struct{ call }

func (*callCreateEvent) Op() ws.OpCode           { return 0 }
func (*callCreateEvent) EventType() ws.EventType { return "CALL_CREATE" }
func (*callUpdateEvent) Op() ws.OpCode           { return 0 }
func (*callUpdateEvent) EventType() ws.EventType { return "CALL_UPDATE" }
func (*callDeleteEvent) Op() ws.OpCode           { return 0 }
func (*callDeleteEvent) EventType() ws.EventType { return "CALL_DELETE" }

func init() {
	gateway.OpUnmarshalers.Add(
		func() ws.Event { return new(callCreateEvent) },
		func() ws.Event { return new(callUpdateEvent) },
		func() ws.Event { return new(callDeleteEvent) },
	)
}

// incomingCall is the card of a call ringing the user, as Discord shows it:
// who calls, with buttons to decline or join.
type incomingCall struct {
	channel         discord.ChannelID
	accept, decline widget.Clickable
	// ring stops the ringing.
	ring chan struct{}
}

// watchCalls follows the calls, to ring the user when one rings them.
func (c *chatView) watchCalls() func() {
	ringing := func(cl call) {
		c.u.post(func() {
			c.noteCall(cl)
			c.callRings(cl)
		})
	}
	stopCreate := c.st.AddHandler(func(ev *callCreateEvent) { ringing(ev.call) })
	stopUpdate := c.st.AddHandler(func(ev *callUpdateEvent) { ringing(ev.call) })
	stopDelete := c.st.AddHandler(func(ev *callDeleteEvent) {
		c.u.post(func() {
			delete(c.dmCalls, ev.ChannelID)
			if c.incoming != nil && c.incoming.channel == ev.ChannelID {
				c.endRinging()
			}
		})
	})
	return func() {
		stopCreate()
		stopUpdate()
		stopDelete()
	}
}

// callRings shows the card while the call rings the user, and hides it
// once it stops ringing them, answered elsewhere or given up.
func (c *chatView) callRings(cl call) {
	me := slices.Contains(cl.Ringing, c.me.ID)
	switch {
	case me && c.incoming == nil && c.u.voiceChannel() != cl.ChannelID:
		in := &incomingCall{channel: cl.ChannelID, ring: make(chan struct{})}
		c.incoming = in
		go c.ringLoop(in.ring)
		c.u.win.Invalidate()
	case !me && c.incoming != nil && c.incoming.channel == cl.ChannelID:
		c.endRinging()
	}
}

// ringLoop plays Discord's ring again and again until stop is closed.
func (c *chatView) ringLoop(stop chan struct{}) {
	t := time.NewTicker(5500 * time.Millisecond)
	defer t.Stop()
	for {
		c.u.post(func() { c.u.playSound(dcsound.CallRinging) })
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (c *chatView) endRinging() {
	if c.incoming != nil {
		close(c.incoming.ring)
		c.incoming = nil
		c.u.win.Invalidate()
	}
}

// layoutIncomingCall is the card, at the top in the middle of the window.
func (c *chatView) layoutIncomingCall(gtx layout.Context) {
	in := c.incoming
	th := c.u.th
	ch, err := c.st.Cabinet.Channel(in.channel)
	if err != nil {
		return
	}
	if in.decline.Clicked(gtx) {
		id := in.channel
		c.endRinging()
		go func() {
			// Declining stops the ringing for the user only, as on Discord.
			c.st.Client.FastRequest("POST", api.EndpointChannels+id.String()+"/call/stop-ringing",
				httputil.WithJSONBody(map[string]any{"recipients": []string{c.me.ID.String()}}))
		}()
		return
	}
	if in.accept.Clicked(gtx) {
		c.endRinging()
		if c.selGuild.IsValid() {
			c.selectGuild(discord.NullGuildID)
		}
		c.rebuildRows()
		c.selectChannel(*ch)
		c.joinVoice(*ch)
		return
	}
	name := c.dmName(*ch)
	avatar := ""
	if len(ch.DMRecipients) == 1 {
		avatar = cdnSize(ch.DMRecipients[0].AvatarURLWithType(discord.PNGImage))
	} else if ch.Icon != "" {
		avatar = cdnSize("https://cdn.discordapp.com/channel-icons/" + ch.ID.String() + "/" + ch.Icon + ".png")
	}
	size := gtx.Constraints.Max
	w := gtx.Dp(240)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y)}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorServers, 10, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// The avatar breathes, as Discord's does while ringing.
					px := gtx.Dp(80)
					phase := float64(gtx.Now.UnixMilli()%1200) / 1200
					ringW := gtx.Dp(3) + int(float64(gtx.Dp(5))*(0.5-0.5*cosTurn(phase)))
					gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(33 * time.Millisecond)})
					full := px + 2*gtx.Dp(8)
					paint.FillShape(gtx.Ops, withAlpha(colorGreen, 0x60), clip.Ellipse(image.Rect(gtx.Dp(8)-ringW, gtx.Dp(8)-ringW, gtx.Dp(8)+px+ringW, gtx.Dp(8)+px+ringW)).Op(gtx.Ops))
					t := op.Offset(image.Pt(gtx.Dp(8), gtx.Dp(8))).Push(gtx.Ops)
					c.u.picture(gtx, avatar, 80, colorAccent, name, -1)
					t.Pop()
					return layout.Dimensions{Size: image.Pt(full, full)}
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
				layout.Rigid(centered(oneLine(bold(label(th, 17, colorHeader, name))))),
				layout.Rigid(centered(label(th, 13, colorMuted, "Incoming Call…"))),
				layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					btn := func(cl *widget.Clickable, bg colorT, ic *widget.Icon) layout.Widget {
						return func(gtx layout.Context) layout.Dimensions {
							return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								s := gtx.Dp(48)
								if cl.Hovered() {
									bg = withAlpha(bg, 0xd0)
								}
								paint.FillShape(gtx.Ops, bg, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
								t := op.Offset(image.Pt((s-gtx.Dp(24))/2, (s-gtx.Dp(24))/2)).Push(gtx.Ops)
								icon(gtx, ic, 24, rgb(0xffffff))
								t.Pop()
								return layout.Dimensions{Size: image.Pt(s, s)}
							})
						}
					}
					gtx.Constraints.Min.X = 0
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(btn(&in.decline, colorRed, iconHangUp)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(36)}.Layout),
						layout.Rigid(btn(&in.accept, colorGreen, iconCall)))
				}),
			)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, gtx.Dp(48))).Push(gtx.Ops)
	// Clicks on the card stay on it, under its buttons.
	solid(gtx, in, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// centered lays out l across the width, its text in the middle.
func centered(l material.LabelStyle) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		l.Alignment = text.Middle
		return l.Layout(gtx)
	}
}

// callChannel calls a direct message or group: joins its call and rings
// the others, as Discord's call button does. video also turns the camera
// on, which Linkcord does not do yet.
func (c *chatView) callChannel(ch discord.Channel, video bool) {
	if c.demo {
		c.u.voiceInfo("Calls are not made in the demo")
		return
	}
	if c.u.voiceChannel() != ch.ID {
		c.joinVoice(ch)
		go func() {
			// Null recipients ring everyone in it.
			c.st.Client.FastRequest("POST", api.EndpointChannels+ch.ID.String()+"/call/ring",
				httputil.WithJSONBody(map[string]any{"recipients": nil}))
		}()
	}
	if video {
		c.u.voiceInfo("The camera is not supported yet")
	}
}

// noteCall keeps who is in the call of a direct message or group, as the
// call's events tell it.
func (c *chatView) noteCall(cl call) {
	if c.dmCalls == nil {
		c.dmCalls = make(map[discord.ChannelID][]discord.UserID)
	}
	if cl.VoiceStates == nil {
		if _, ok := c.dmCalls[cl.ChannelID]; !ok {
			c.dmCalls[cl.ChannelID] = nil
		}
		return
	}
	var in []discord.UserID
	for _, vs := range cl.VoiceStates {
		if vs.ChannelID == cl.ChannelID {
			in = append(in, vs.UserID)
		}
	}
	c.dmCalls[cl.ChannelID] = in
}

// noteCallVoice follows people joining and leaving the calls of direct
// messages and groups, whose voice states have no server.
func (c *chatView) noteCallVoice(vs discord.VoiceState) {
	if vs.GuildID.IsValid() {
		return
	}
	if c.dmCalls == nil {
		c.dmCalls = make(map[discord.ChannelID][]discord.UserID)
	}
	for ch, users := range c.dmCalls {
		c.dmCalls[ch] = slices.DeleteFunc(users, func(u discord.UserID) bool { return u == vs.UserID })
	}
	if vs.ChannelID.IsValid() {
		c.dmCalls[vs.ChannelID] = append(c.dmCalls[vs.ChannelID], vs.UserID)
	}
}

// layoutCallBanner is the call going on in the direct message or group
// open, above its messages, as Discord shows it: who is in it, and buttons
// to join it, with video or not.
func (c *chatView) layoutCallBanner(gtx layout.Context) layout.Dimensions {
	ch := c.selChannel
	if ch == nil || ch.GuildID.IsValid() {
		return layout.Dimensions{}
	}
	users := slices.Clone(c.dmCalls[ch.ID])
	// In the call, the user is in it too, and needs no button to join.
	in := c.u.voiceChannel() == ch.ID
	if in && !slices.Contains(users, c.me.ID) {
		users = append(users, c.me.ID)
	}
	if len(users) == 0 {
		return layout.Dimensions{}
	}
	if c.joinCallBtn.Clicked(gtx) {
		c.joinVoice(*ch)
	}
	if c.joinVideoBtn.Clicked(gtx) {
		c.joinVoice(*ch)
		c.u.voiceInfo("The camera is not supported yet")
	}
	w := gtx.Constraints.Max.X
	h := gtx.Dp(200)
	paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: image.Pt(w, h)}.Op())
	gtx.Constraints = layout.Exact(image.Pt(w, h))
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var faces []layout.FlexChild
				for _, id := range users {
					user := c.callUser(*ch, id)
					faces = append(faces, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							px := gtx.Dp(80)
							d := c.u.picture(gtx, c.animatedAvatarURL(0, user), 80, colorFor(uint64(id)), user.DisplayOrUsername(), -1)
							if c.u.voiceSpeaking(id) {
								speakingRing(gtx, px, rgb(0x000000))
							}
							return d
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, faces...)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if in {
					return layout.Dimensions{}
				}
				btn := func(cl *widget.Clickable, ic *widget.Icon) layout.Widget {
					return func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								bg := colorGreenButton
								if cl.Hovered() {
									bg = withAlpha(bg, 0xd0)
								}
								sz := image.Pt(gtx.Dp(80), gtx.Dp(56))
								paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(12)).Op(gtx.Ops))
								t := op.Offset(image.Pt((sz.X-gtx.Dp(26))/2, (sz.Y-gtx.Dp(26))/2)).Push(gtx.Ops)
								icon(gtx, ic, 26, rgb(0xffffff))
								t.Pop()
								return layout.Dimensions{Size: sz}
							})
						})
					}
				}
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(btn(&c.joinVideoBtn, iconCamera)),
					layout.Rigid(btn(&c.joinCallBtn, iconCall)))
			}),
		)
	})
}

// callUser is the user id of a direct message or group, from its
// recipients, or the user.
func (c *chatView) callUser(ch discord.Channel, id discord.UserID) discord.User {
	if id == c.me.ID {
		return c.me
	}
	for _, u := range ch.DMRecipients {
		if u.ID == id {
			return u
		}
	}
	if u, err := c.st.Cabinet.User(id); err == nil {
		return *u
	}
	return discord.User{ID: id}
}
