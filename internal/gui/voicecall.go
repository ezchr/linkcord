package gui

import (
	"cmp"
	"image"
	"image/color"
	"slices"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconCamera     = mustIcon(icons.AVVideocam)
	iconScreen     = mustIcon(icons.CommunicationScreenShare)
	iconActivity   = mustIcon(icons.HardwareGamepad)
	iconSoundboard = mustIcon(icons.ImageMusicNote)
	iconMore       = mustIcon(icons.NavigationMoreHoriz)
	iconInvite     = mustIcon(icons.SocialPersonAdd)
	iconArrowUp    = mustIcon(icons.NavigationExpandLess)
)

// callView is the voice channel open in the middle of the window, with a
// card for everyone in it, as Discord shows it when its name is clicked.
type callView struct {
	open bool
	// full reports whether the call fills the whole screen.
	full    bool
	channel discord.ChannelID

	join, invite, fullBtn, leave widget.Clickable
	// stopWatch stops watching someone's stream, beside the button to hang
	// up while one is watched.
	stopWatch widget.Clickable
	// streamClip clips the stream watched, when stream clips are kept.
	streamClip                         widget.Clickable
	mic, micMenu, cam, camMenu         widget.Clickable
	screen, activity, soundboard, more widget.Clickable
	// streamSound turns the sound of the stream on or off.
	streamSound widget.Clickable
	// focus is whose card is shown big, with the others in a row under it,
	// as a click on a card does; cards are their cards' clicks.
	focus discord.UserID
	cards map[discord.UserID]*widget.Clickable
	// viewers is the pointer over the faces of who watches the user's stream.
	viewers gesture.Hover
}

// soundboard is the sounds that can be played to everyone in the voice
// channel.
type soundboard struct {
	loaded  map[discord.GuildID]bool
	loading bool
	sounds  []soundboardSound
	// others reports whether the sounds of the user's other servers were
	// asked for, which play anywhere with Nitro.
	others bool
}

// soundboardSound is a sound of Discord's soundboard, or of a server's.
type soundboardSound struct {
	ID        discord.Snowflake `json:"sound_id"`
	Name      string            `json:"name"`
	EmojiName string            `json:"emoji_name"`
	GuildID   discord.GuildID   `json:"guild_id"`
	Available *bool             `json:"available"`
}

// demoSounds are some of Discord's own sounds, for the demo.
var demoSounds = []soundboardSound{
	{ID: 1, Name: "quack", EmojiName: "🦆"},
	{ID: 2, Name: "airhorn", EmojiName: "🔊"},
	{ID: 3, Name: "cricket", EmojiName: "🦗"},
	{ID: 4, Name: "golf clap", EmojiName: "⛳"},
	{ID: 5, Name: "sad horn", EmojiName: "🎺"},
	{ID: 7, Name: "ba dum tss", EmojiName: "🥁"},
}

// openCall shows the voice channel ch in the middle of the window.
func (c *chatView) openCall(ch discord.Channel) {
	if ch.GuildID != c.selGuild {
		c.selectGuild(ch.GuildID)
	}
	c.closeChannel()
	c.call.open, c.call.channel = true, ch.ID
	c.setTitle(ch.Name)
}

// closeCall puts the window back from the voice channel to the chat.
func (c *chatView) closeCall() {
	if c.call.full {
		c.u.win.Option(app.Windowed.Option())
	}
	c.call.open, c.call.full = false, false
}

// callStates are the people in the voice channel chID, by name.
func (c *chatView) callStates(chID discord.ChannelID) []discord.VoiceState {
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil {
		return nil
	}
	states, _ := c.st.Cabinet.VoiceStates(ch.GuildID)
	var in []discord.VoiceState
	for _, vs := range states {
		if vs.ChannelID == chID {
			in = append(in, vs)
		}
	}
	names := make(map[discord.UserID]string, len(in))
	for _, vs := range in {
		names[vs.UserID] = strings.ToLower(c.memberName(ch.GuildID, vs.UserID, vs.Member))
	}
	slices.SortFunc(in, func(a, b discord.VoiceState) int {
		return cmp.Or(strings.Compare(names[a.UserID], names[b.UserID]), cmp.Compare(a.UserID, b.UserID))
	})
	return in
}

func (c *chatView) updateCall(gtx layout.Context) {
	cv := &c.call
	u := c.u
	ch, err := c.st.Cabinet.Channel(cv.channel)
	if err != nil {
		c.closeCall()
		return
	}
	if cv.join.Clicked(gtx) {
		c.joinVoice(*ch)
	}
	if cv.leave.Clicked(gtx) {
		u.leaveVoice()
	}
	if cv.streamClip.Clicked(gtx) {
		c.openStreamClip()
	}
	if cv.stopWatch.Clicked(gtx) {
		c.stopWatching()
	}
	if cv.mic.Clicked(gtx) {
		u.toggleVoice(false)
		u.syncDemoVoice()
	}
	if cv.micMenu.Clicked(gtx) {
		u.loadDevices()
		c.openMenu(c.pointerAt, 280, c.deviceItems)
	}
	if cv.cam.Clicked(gtx) || cv.camMenu.Clicked(gtx) {
		u.voiceInfo("The camera is not supported yet")
	}
	if cv.screen.Clicked(gtx) {
		c.screenShareClicked()
	}
	if cv.streamSound.Clicked(gtx) {
		u.toggleStreamSound()
	}
	for id, cl := range cv.cards {
		if cl.Clicked(gtx) {
			if cv.focus == id {
				cv.focus = 0
			} else {
				cv.focus = id
			}
		}
	}
	if cv.activity.Clicked(gtx) {
		u.voiceInfo("Activities only work in Discord's own app")
	}
	if cv.soundboard.Clicked(gtx) {
		c.openSoundboard()
	}
	if cv.more.Clicked(gtx) {
		c.openMenu(c.pointerAt, 220, c.callMoreItems)
	}
	if cv.invite.Clicked(gtx) {
		c.copyInvite(cv.channel)
	}
	// Escape leaves the full screen, as in Discord.
	for cv.full {
		e, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := e.(key.Event); ok && ke.State == key.Press {
			cv.full = false
			u.win.Option(app.Windowed.Option())
		}
	}
	if cv.fullBtn.Clicked(gtx) {
		cv.full = !cv.full
		if cv.full {
			u.win.Option(app.Fullscreen.Option())
		} else {
			u.win.Option(app.Windowed.Option())
		}
	}
}

// layoutCall is the voice channel open in the middle: its name, a card for
// everyone in it, and the buttons of the call.
func (c *chatView) layoutCall(gtx layout.Context) layout.Dimensions {
	c.updateCall(gtx)
	cv := &c.call
	if !cv.open {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	th := c.u.th
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
	ch, _ := c.st.Cabinet.Channel(cv.channel)
	states := c.callStates(cv.channel)
	joined := c.u.voiceStatus().ChannelID == cv.channel

	dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// The channel's name, at the top left.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(size.X, gtx.Dp(52)))
			return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconVoice, 20, colorText) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(oneLine(bold(label(th, 16, colorHeader, ch.Name))).Layout),
					)
				})
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if len(states) == 0 {
					return layout.Center.Layout(gtx, label(th, 16, colorMuted, "No one is here yet").Layout)
				}
				return c.layoutCallGrid(gtx, ch.GuildID, states)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.layoutCallBar(gtx, joined)
		}),
	)
	c.layoutCallNote(gtx)
	return dims
}

// gridFit is how many columns of cards of 16:9 fill w by h best with n
// cards, gap apart, and how big they are then.
func gridFit(n, w, h, gap int) (cols, tw, th int) {
	best := -1
	for k := 1; k <= n; k++ {
		rows := (n + k - 1) / k
		cw := (w - gap*(k-1)) / k
		ch := cw * 9 / 16
		if rows*ch+gap*(rows-1) > h {
			ch = (h - gap*(rows-1)) / rows
			cw = ch * 16 / 9
		}
		if cw <= 0 || ch <= 0 {
			continue
		}
		if area := cw * ch; area > best {
			best, cols, tw, th = area, k, cw, ch
		}
	}
	return cols, tw, th
}

func (c *chatView) layoutCallGrid(gtx layout.Context, guildID discord.GuildID, states []discord.VoiceState) layout.Dimensions {
	size := gtx.Constraints.Max
	gap := gtx.Dp(8)
	if i := slices.IndexFunc(states, func(vs discord.VoiceState) bool { return vs.UserID == c.call.focus }); i >= 0 {
		return c.layoutCallFocus(gtx, guildID, states, i)
	}
	c.call.focus = 0
	cols, tw, th := gridFit(len(states), size.X, size.Y, gap)
	if cols == 0 {
		return layout.Dimensions{Size: size}
	}
	rows := (len(states) + cols - 1) / cols
	y := (size.Y - (rows*th + gap*(rows-1))) / 2
	for r := range rows {
		n := min(cols, len(states)-r*cols)
		x := (size.X - (n*tw + gap*(n-1))) / 2
		for i := range n {
			vs := states[r*cols+i]
			t := op.Offset(image.Pt(x+i*(tw+gap), y+r*(th+gap))).Push(gtx.Ops)
			cgtx := gtx
			cgtx.Constraints = layout.Exact(image.Pt(tw, th))
			c.layoutCallCard(cgtx, guildID, vs, false)
			t.Pop()
		}
	}
	return layout.Dimensions{Size: size}
}

// layoutCallFocus shows the card of states[focus] big, and the others in a
// row under it, as many as fit.
func (c *chatView) layoutCallFocus(gtx layout.Context, guildID discord.GuildID, states []discord.VoiceState, focus int) layout.Dimensions {
	size := gtx.Constraints.Max
	gap := gtx.Dp(8)
	others := slices.Delete(slices.Clone(states), focus, focus+1)
	stripH := 0
	if len(others) > 0 {
		stripH = min(gtx.Dp(110), size.Y/4)
	}
	// The big card, 16:9, as large as fits above the row.
	areaH := size.Y - stripH
	if stripH > 0 {
		areaH -= gap
	}
	bw, bh := size.X, size.X*9/16
	if bh > areaH {
		bh = areaH
		bw = bh * 16 / 9
	}
	t := op.Offset(image.Pt((size.X-bw)/2, (areaH-bh)/2)).Push(gtx.Ops)
	cgtx := gtx
	cgtx.Constraints = layout.Exact(image.Pt(bw, bh))
	c.layoutCallCard(cgtx, guildID, states[focus], true)
	t.Pop()

	if stripH == 0 {
		return layout.Dimensions{Size: size}
	}
	// The cards of the row shrink for all to fit, down to a size where
	// they still read; past it, those that do not fit are left out.
	tw := min(stripH*16/9, (size.X-gap*(len(others)-1))/len(others))
	if minW := gtx.Dp(120); tw < minW {
		tw = minW
	}
	stripH = min(stripH, tw*9/16)
	n := min(len(others), max((size.X+gap)/(tw+gap), 1))
	x := (size.X - (n*tw + gap*(n-1))) / 2
	for i := range n {
		t := op.Offset(image.Pt(x+i*(tw+gap), size.Y-stripH)).Push(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints = layout.Exact(image.Pt(tw, stripH))
		c.layoutCallCard(cgtx, guildID, others[i], false)
		t.Pop()
	}
	return layout.Dimensions{Size: size}
}

// cardClick is the click of the card of id, which shows it big or back.
func (cv *callView) cardClick(id discord.UserID) *widget.Clickable {
	if cv.cards == nil {
		cv.cards = make(map[discord.UserID]*widget.Clickable)
	}
	return clickOf(cv.cards, id)
}

// cardColor is the color of the card of a user without video: their color,
// darkened as Discord does.
func cardColor(id discord.UserID) color.NRGBA {
	col := colorFor(uint64(id))
	mix := func(v uint8) uint8 { return uint8(int(v) * 45 / 100) }
	return color.NRGBA{R: mix(col.R), G: mix(col.G), B: mix(col.B), A: 0xff}
}

// layoutCallCard is the card of someone in the call: their picture in the
// middle, and their name at the bottom left.
//
// A click on it shows it big, or back among the others if big already.
func (c *chatView) layoutCallCard(gtx layout.Context, guildID discord.GuildID, vs discord.VoiceState, big bool) layout.Dimensions {
	size := gtx.Constraints.Max
	radius := gtx.Dp(8)
	// The click goes under what is drawn on the card, which may take the
	// pointer itself.
	cl := c.call.cardClick(vs.UserID)
	cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
	// Green while they speak, blue while a sound they played sounds.
	ringCol, speaking := c.voiceRingColor(gtx, vs.UserID)
	rr := clip.UniformRRect(image.Rectangle{Max: size}, radius)
	paint.FillShape(gtx.Ops, cardColor(vs.UserID), rr.Op(gtx.Ops))

	name := c.memberName(guildID, vs.UserID, vs.Member)
	if vs.UserID == c.me.ID {
		if img := c.u.streamPreview(); img != nil {
			c.layoutStreamCard(gtx, img, size, radius)
			// The preview is made as wide as the card, and shown as it is.
			if s := c.u.voice.stream; s != nil {
				s.SetPreviewWidth(size.X)
			}
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
			c.layoutCardName(gtx, vs, name, size, true)
			if rightClicked(gtx, c.rightTag("card"+vs.UserID.String()), size) {
				c.openUserMenu(vs.UserID, guildID, vs.ChannelID)
			}
			// Who watches: how many on a small card; on a big one their
			// faces at the top right, and their names under the pointer.
			pad := gtx.Dp(8)
			if big {
				c.layoutViewerFaces(gtx, guildID, size)
			} else {
				t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
				c.layoutViewers(gtx, guildID)
				t.Pop()
			}
			return layout.Dimensions{Size: size}
		}
	}
	px := min(size.Y*2/5, gtx.Dp(96), size.X*2/5)
	// Someone else's stream: shown once watched, as on Discord, with a
	// button to watch it under their avatar until then.
	var watchBtn func()
	if vs.UserID != c.me.ID && vs.SelfStream {
		shown, btn := c.layoutWatchedCard(gtx, vs, size, radius, (size.Y+px)/2+gtx.Dp(12))
		watchBtn = btn
		if shown {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(30 * time.Millisecond)})
			c.layoutCardName(gtx, vs, name, size, true)
			if rightClicked(gtx, c.rightTag("card"+vs.UserID.String()), size) {
				c.openUserMenu(vs.UserID, guildID, vs.ChannelID)
			}
			return layout.Dimensions{Size: size}
		}
	} else if c.watch.streamer == vs.UserID && !vs.SelfStream {
		// They stopped sharing.
		c.stopWatching()
	}
	dp := unit.Dp(float32(px) / gtx.Metric.PxPerDp)
	center := op.Offset(image.Pt((size.X-px)/2, (size.Y-px)/2)).Push(gtx.Ops)
	if speaking {
		ring := gtx.Dp(3)
		paint.FillShape(gtx.Ops, ringCol, clip.Ellipse(image.Rect(-ring, -ring, px+ring, px+ring)).Op(gtx.Ops))
	}
	c.u.picture(gtx, c.voiceAvatarURL(guildID, vs), dp, colorFor(uint64(vs.UserID)), name, -1)
	center.Pop()
	if watchBtn != nil {
		watchBtn()
	}

	c.layoutCardName(gtx, vs, name, size, vs.SelfStream)

	if speaking {
		w := gtx.Dp(2)
		paint.FillShape(gtx.Ops, ringCol, clip.Stroke{Path: clip.UniformRRect(image.Rectangle{Max: size}.Inset(w/2), radius).Path(gtx.Ops), Width: float32(w)}.Op())
	}
	if rightClicked(gtx, c.rightTag("card"+vs.UserID.String()), size) {
		c.openUserMenu(vs.UserID, guildID, vs.ChannelID)
	}
	return layout.Dimensions{Size: size}
}

// layoutStreamCard fills the card with the picture of what is shared.
func (c *chatView) layoutStreamCard(gtx layout.Context, img *image.RGBA, size image.Point, radius int) {
	src := c.u.voice.previewOp(img)
	defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
	igtx := gtx
	igtx.Constraints = layout.Exact(size)
	b := img.Bounds()
	scale := min(float32(size.X)/float32(b.Dx()), float32(size.Y)/float32(b.Dy())) / gtx.Metric.PxPerDp
	widget.Image{Src: src, Fit: widget.Contain, Scale: scale}.Layout(igtx)
}

// layoutCardName is the name on a card, with the microphone or headset
// struck through, and "LIVE" for who shares their screen, on a dark patch
// at the bottom left.
func (c *chatView) layoutCardName(gtx layout.Context, vs discord.VoiceState, name string, size image.Point, live bool) {
	th := c.u.th
	pad := gtx.Dp(8)
	macro := op.Record(gtx.Ops)
	ngtx := gtx
	ngtx.Constraints = layout.Constraints{Max: image.Pt(size.X-2*pad, size.Y)}
	ndims := rounded(ngtx, withAlpha(rgb(0x000000), 0x99), 6, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d := voiceStateIcons(gtx, vs, 16)
					if d.Size.X > 0 {
						d.Size.X += gtx.Dp(4)
					}
					return d
				}),
				layout.Rigid(oneLine(bold(label(th, 14, colorHeader, name))).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !live {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return liveBadge(gtx, th)
					})
				}),
			)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt(pad, size.Y-pad-ndims.Size.Y)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// callButton is a button of the bar of the call: ic on a rounded patch of
// bg, w wide.
func callButton(gtx layout.Context, click *widget.Clickable, ic *widget.Icon, fg, bg color.NRGBA, w unit.Dp) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := image.Pt(gtx.Dp(w), gtx.Dp(44))
		if click.Hovered() {
			bg = lighten(bg)
		}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(10)).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 22, fg) })
	})
}

// splitButton is a button with an arrow beside it that opens its options,
// as the microphone and the camera have.
func splitButton(gtx layout.Context, main, arrow *widget.Clickable, ic *widget.Icon, fg, bg color.NRGBA) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return callButton(gtx, main, ic, fg, bg, 52) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(2)}.Layout),
		// The arrow is darker, to tell it from the button.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return callButton(gtx, arrow, iconArrowUp, colorText, rgb(0x141517), 26)
		}),
	)
}

// callBarColor is the color of the buttons of the bar of the call.
var callBarColor = rgb(0x232428)

// layoutCallBar is the bar at the bottom of the call: the invite at the
// left, the buttons of the call in the middle and the full screen at the
// right.
func (c *chatView) layoutCallBar(gtx layout.Context, joined bool) layout.Dimensions {
	cv := &c.call
	v := &c.u.voice
	th := c.u.th
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(76))
	gtx.Constraints = layout.Exact(size)
	gap := layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout)
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
					return callButton(gtx, &cv.invite, iconInvite, colorText, callBarColor, 44)
				})
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = 0
					ic := iconFullscreen
					if cv.full {
						ic = iconExitFull
					}
					return callButton(gtx, &cv.fullBtn, ic, colorText, callBarColor, 44)
				})
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = gtx.Constraints.Max
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if !joined {
						return button(gtx, th, &cv.join, colorGreenButton, "Join Voice")
					}
					micIcon, micFg, micBg := iconMic, colorHeader, callBarColor
					if v.muted {
						micIcon, micFg, micBg = iconMicOff, colorRed, rgb(0x3b1d20)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return splitButton(gtx, &cv.mic, &cv.micMenu, micIcon, micFg, micBg)
						}),
						gap,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return splitButton(gtx, &cv.cam, &cv.camMenu, iconCamera, colorHeader, callBarColor)
						}),
						gap,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							bg := callBarColor
							if c.u.streaming() {
								bg = colorGreenButton
							}
							return callButton(gtx, &cv.screen, iconScreen, colorHeader, bg, 52)
						}),
						// While live, the sound of the stream can be turned
						// on and off.
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !c.u.streaming() {
								return layout.Dimensions{}
							}
							ic, fg, bg := iconSoundOn, colorHeader, colorGreenButton
							if !c.u.streamSound() {
								ic, fg, bg = iconSoundOff, colorRed, rgb(0x3b1d20)
							}
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return callButton(gtx, &cv.streamSound, ic, fg, bg, 52)
							})
						}),
						gap,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return callButton(gtx, &cv.activity, iconActivity, colorHeader, callBarColor, 52)
						}),
						gap,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return callButton(gtx, &cv.soundboard, iconSoundboard, colorHeader, callBarColor, 52)
						}),
						gap,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return callButton(gtx, &cv.more, iconMore, colorHeader, callBarColor, 52)
						}),
						gap,
						// While a stream is watched, a button stops watching
						// it, beside the one that hangs up.
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !c.watch.streamer.IsValid() {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								if c.u.app.StreamClipMinutes == 0 {
									return callButton(gtx, &cv.stopWatch, iconStopWatching, colorHeader, colorRed, 52)
								}
								// And one clips it, a Linkcord feature.
								return layout.Flex{}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return callButton(gtx, &cv.streamClip, iconClip, colorHeader, callBarColor, 52)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return callButton(gtx, &cv.stopWatch, iconStopWatching, colorHeader, colorRed, 52)
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return callButton(gtx, &cv.leave, iconHangUp, colorHeader, colorRed, 64)
						}),
					)
				})
			}),
		)
	})
}

// layoutCallNote shows what the last voice action said, above the bar of the
// call, where the voice panel is not seen.
func (c *chatView) layoutCallNote(gtx layout.Context) {
	msg, failed := c.u.voiceMessage()
	if msg == "" {
		return
	}
	th := c.u.th
	size := gtx.Constraints.Max
	col := colorHeader
	if failed {
		col = colorRed
	}
	macro := op.Record(gtx.Ops)
	ngtx := gtx
	ngtx.Constraints = layout.Constraints{Max: image.Pt(min(size.X-gtx.Dp(32), gtx.Dp(480)), size.Y)}
	dims := rounded(ngtx, colorMenu, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(10)).Layout(gtx, label(th, 14, col, msg).Layout)
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, size.Y-gtx.Dp(88)-dims.Size.Y)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// callMoreItems are the other things to do in the call, under "…".
func (c *chatView) callMoreItems() []menuItem {
	u := c.u
	cv := &c.call
	return []menuItem{
		{text: "Deafen", check: checked(u.voice.deafened), act: func() {
			u.toggleVoice(true)
			u.syncDemoVoice()
			c.menu.stay = true
		}},
		{text: "Open Text Chat", act: func() {
			if ch, err := c.st.Cabinet.Channel(cv.channel); err == nil {
				c.closeCall()
				c.selectChannel(*ch)
			}
		}},
		{sep: true},
		{text: "Disconnect", col: colorRed, act: u.leaveVoice},
	}
}

// deviceItems are the microphones and speakers to choose from, under the
// arrow of the microphone.
func (c *chatView) deviceItems() []menuItem {
	u := c.u
	v := &u.voice
	settings := u.voiceSettings()
	device := func(input bool, d voice.Device) menuItem {
		cur := settings.Output
		prefix := "out:"
		if input {
			cur, prefix = settings.Input, "in:"
		}
		text := d.Name
		if d.ID == "" {
			text = "Default"
		}
		return menuItem{key: prefix + d.ID, text: text, check: checked(cur == d.ID), act: func() {
			if input {
				u.setDevices(d.ID, settings.Output)
			} else {
				u.setDevices(settings.Input, d.ID)
			}
			c.menu.stay = true
		}}
	}
	items := []menuItem{{title: true, text: "INPUT DEVICE"}, device(true, voice.Device{})}
	for _, d := range v.inputs {
		items = append(items, device(true, d))
	}
	items = append(items, menuItem{sep: true}, menuItem{title: true, text: "OUTPUT DEVICE"}, device(false, voice.Device{}))
	for _, d := range v.outputs {
		items = append(items, device(false, d))
	}
	items = append(items,
		menuItem{sep: true},
		menuItem{text: "Mute", check: checked(v.muted), act: func() { u.toggleVoice(false); u.syncDemoVoice(); c.menu.stay = true }},
		menuItem{text: "Deafen", check: checked(v.deafened), act: func() { u.toggleVoice(true); u.syncDemoVoice(); c.menu.stay = true }},
	)
	return items
}

// copyInvite makes an invite to the voice channel chID and copies its link.
func (c *chatView) copyInvite(chID discord.ChannelID) {
	if c.demo {
		c.copyText = "https://discord.gg/demo"
		c.u.voiceInfo("Invite link copied")
		return
	}
	if !c.myPerms(chID).Has(discord.PermissionCreateInstantInvite) {
		c.u.voiceInfo("You do not have permission to invite people here")
		return
	}
	go func() {
		inv, err := c.st.CreateInvite(chID, api.CreateInviteData{})
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not make an invite", err)
				return
			}
			c.copyText = "https://discord.gg/" + inv.Code
			c.u.voiceInfo("Invite link copied: discord.gg/" + inv.Code)
		})
	}()
}

// openSoundboard opens the sounds to play to the voice channel joined,
// loading those of its server the first time.
func (c *chatView) openSoundboard() {
	chID := c.u.voiceChannel()
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil {
		c.u.voiceInfo("Join a voice channel to use the soundboard")
		return
	}
	sb := &c.sounds
	if sb.loaded == nil {
		sb.loaded = make(map[discord.GuildID]bool)
	}
	if c.demo {
		sb.sounds, sb.loaded[0] = demoSounds, true
		// A sound of another server, to show them.
		if guilds, err := c.st.Cabinet.Guilds(); err == nil {
			for _, g := range guilds {
				if g.ID != ch.GuildID {
					sb.sounds = append(slices.Clone(demoSounds),
						soundboardSound{ID: 8, Name: "victory", EmojiName: "🏆", GuildID: g.ID},
						soundboardSound{ID: 9, Name: "bruh", EmojiName: "😐", GuildID: g.ID})
					break
				}
			}
		}
	}
	if !sb.others && !c.demo {
		sb.others = true
		c.loadOtherSounds(ch.GuildID)
	}
	if !sb.loaded[ch.GuildID] && !sb.loading && !c.demo {
		sb.loading = true
		gid := ch.GuildID
		go func() {
			var all []soundboardSound
			def, err := c.st.Client.RequestJSON[[]soundboardSound]("GET", api.Endpoint+"soundboard-default-sounds")
			if err == nil {
				all = append(all, def...)
			}
			if gid.IsValid() {
				own, err := c.st.Client.RequestJSON[struct {
					Items []soundboardSound `json:"items"`
				}]("GET", api.EndpointGuilds+gid.String()+"/soundboard-sounds")
				if err == nil {
					all = append(all, own.Items...)
				}
			}
			c.u.post(func() {
				sb.loading = false
				sb.loaded[gid] = true
				sb.sounds = slices.DeleteFunc(append(slices.DeleteFunc(sb.sounds, func(s soundboardSound) bool {
					return s.GuildID == gid || !s.GuildID.IsValid()
				}), all...), func(s soundboardSound) bool { return s.Available != nil && !*s.Available })
			})
		}()
	}
	c.openMenu(c.pointerAt, 240, c.soundItems)
}

// loadOtherSounds asks for the sounds of the user's servers but here, one
// server at a time, for the soundboard to offer them too.
func (c *chatView) loadOtherSounds(here discord.GuildID) {
	guilds, err := c.st.Cabinet.Guilds()
	if err != nil {
		return
	}
	sb := &c.sounds
	go func() {
		for _, g := range guilds {
			if g.ID == here {
				continue
			}
			res, err := c.st.Client.RequestJSON[struct {
				Items []soundboardSound `json:"items"`
			}]("GET", api.EndpointGuilds+g.ID.String()+"/soundboard-sounds")
			if err == nil && len(res.Items) > 0 {
				gid, items := g.ID, res.Items
				for i := range items {
					items[i].GuildID = gid
				}
				c.u.post(func() {
					sb.sounds = append(slices.DeleteFunc(sb.sounds, func(s soundboardSound) bool { return s.GuildID == gid }),
						slices.DeleteFunc(items, func(s soundboardSound) bool { return s.Available != nil && !*s.Available })...)
				})
			}
			select {
			case <-c.done:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
}

// soundItems are the sounds of the soundboard: the server's, those of the
// user's other servers, then Discord's.
func (c *chatView) soundItems() []menuItem {
	sb := &c.sounds
	ch, err := c.st.Cabinet.Channel(c.u.voiceChannel())
	if err != nil {
		return []menuItem{{title: true, text: "Not in a voice channel"}}
	}
	items := []menuItem{{title: true, text: "SOUNDBOARD"}}
	if sb.loading {
		items = append(items, menuItem{title: true, text: "Loading…"})
	}
	var own, def []menuItem
	others := make(map[discord.GuildID][]menuItem)
	for _, s := range sb.sounds {
		text := s.Name
		if s.EmojiName != "" {
			text = s.EmojiName + "  " + s.Name
		}
		it := menuItem{key: "sound" + s.ID.String(), text: text, act: func() { c.playSound(ch.ID, s) }}
		switch {
		case s.GuildID == ch.GuildID && s.GuildID.IsValid():
			own = append(own, it)
		case s.GuildID.IsValid():
			others[s.GuildID] = append(others[s.GuildID], it)
		default:
			def = append(def, it)
		}
	}
	if len(own) > 0 {
		items = append(items, own...)
	}
	// Those of the other servers, in the order of the server list, under
	// their server's name.
	order := slices.Clone(c.guildOrder)
	for id := range others {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	for _, id := range order {
		its := others[id]
		if len(its) == 0 {
			continue
		}
		name := id.String()
		if g, err := c.st.Cabinet.Guild(id); err == nil {
			name = g.Name
		}
		title := strings.ToUpper(name)
		if !c.hasNitro() && !c.demo {
			// Discord lets only Nitro play them out of their server.
			title += " · NITRO"
		}
		items = append(items, menuItem{sep: true}, menuItem{title: true, text: title})
		items = append(items, its...)
	}
	if len(def) > 0 {
		items = append(items, menuItem{sep: true}, menuItem{title: true, text: "DEFAULT"})
	}
	items = append(items, def...)
	if !sb.loading && len(own)+len(def)+len(others) == 0 {
		items = append(items, menuItem{title: true, text: "No sounds"})
	}
	return items
}

// playSound plays s to everyone in the voice channel chID.
func (c *chatView) playSound(chID discord.ChannelID, s soundboardSound) {
	if c.demo {
		c.u.voiceInfo("Played “" + s.Name + "”")
		c.noteSoundPlayed(c.me.ID, soundGuess)
		return
	}
	body := map[string]any{"sound_id": s.ID}
	if s.GuildID.IsValid() {
		body["source_guild_id"] = s.GuildID
	}
	go func() {
		err := c.st.Client.FastRequest("POST", api.EndpointChannels+chID.String()+"/send-soundboard-sound", httputil.WithJSONBody(body))
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not play “"+s.Name+"”", err)
			}
		})
	}()
}
