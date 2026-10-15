package gui

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"path/filepath"
	"strings"
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
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/dcsound"
	"github.com/ezchr/linkcord/internal/golive"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconEye      = mustIcon(icons.ImageRemoveRedEye)
	iconSoundOn  = mustIcon(icons.AVVolumeUp)
	iconSoundOff = mustIcon(icons.AVVolumeOff)
)

// streamPicker is the window to choose what to share and how, as Discord's.
type streamPicker struct {
	open    bool
	loading bool
	sources []golive.Source
	thumbs  []paint.ImageOp
	// screens reports whether the screens tab is shown, rather than the
	// applications.
	screens bool
	chosen  int
	height  int
	fps     int
	// changing reports whether it chooses what an ongoing stream shares.
	changing bool

	sound widget.Clickable
	// everyone and linkcordOnly choose who can watch.
	everyone, linkcordOnly widget.Clickable
	// backdrop is the dark area around the window, which a click closes it
	// from.
	backdrop      widget.Clickable
	tabs          [2]widget.Clickable
	cards         []widget.Clickable
	heights, fpss []widget.Clickable
	goLive, close widget.Clickable
	list          widget.List
}

// The resolutions and frame rates to stream at, as Discord offers them;
// those above 720p and 30 a second need Nitro.
var (
	streamHeights = []int{480, 720, 1080, 0}
	streamFPS     = []int{15, 30, 60}
)

// hasNitro reports whether the user may stream above 720p and 30 a second.
func (c *chatView) hasNitro() bool {
	return c.demo || c.me.Nitro == discord.NitroFull || c.me.Nitro == discord.NitroClassic
}

// openStreamPicker opens the window to choose what to share, loading the
// screens and windows with their pictures.
func (c *chatView) openStreamPicker() {
	u := c.u
	if !u.voiceStatus().Joined {
		u.voiceInfo("Join a voice channel to share your screen")
		return
	}
	// From the cache, as all that is checked while drawing.
	if ch, err := c.st.Cabinet.Channel(u.voiceChannel()); err == nil && ch.GuildID.IsValid() && !c.can(ch.ID, discord.PermissionStream) {
		u.voiceInfo("You do not have permission to share your screen here")
		return
	}
	sp := &u.voice.picker
	settings := u.voiceSettings()
	*sp = streamPicker{open: true, loading: true, chosen: -1, height: 720, fps: 30}
	if settings.StreamHeight != 0 || settings.StreamFPS != 0 {
		sp.height, sp.fps = settings.StreamHeight, settings.StreamFPS
	}
	if !c.hasNitro() {
		sp.height = min(sp.height, 720)
		if sp.height == 0 {
			sp.height = 720
		}
		sp.fps = min(sp.fps, 30)
	}
	sp.list.Axis = layout.Vertical
	sp.heights = make([]widget.Clickable, len(streamHeights))
	sp.fpss = make([]widget.Clickable, len(streamFPS))
	self := u.hwnd
	go func() {
		sources := golive.Sources(self, 256)
		u.post(func() {
			if !sp.open {
				return
			}
			sp.loading = false
			sp.sources = sources
			sp.thumbs = make([]paint.ImageOp, len(sources))
			for i, s := range sources {
				if s.Thumb != nil {
					sp.thumbs[i] = paint.NewImageOp(s.Thumb)
				}
			}
			sp.cards = make([]widget.Clickable, len(sources))
		})
	}()
}

// shown are the indices of the sources of the tab shown.
func (sp *streamPicker) shown() []int {
	var out []int
	for i, s := range sp.sources {
		if (s.Kind == golive.Screen) == sp.screens {
			out = append(out, i)
		}
	}
	return out
}

func (c *chatView) updateStreamPicker(gtx layout.Context) {
	sp := &c.u.voice.picker
	if sp.close.Clicked(gtx) || sp.backdrop.Clicked(gtx) {
		sp.open = false
		return
	}
	for i := range sp.tabs {
		if sp.tabs[i].Clicked(gtx) {
			sp.screens = i == 1
			sp.chosen = -1
		}
	}
	for i := range sp.cards {
		if sp.cards[i].Clicked(gtx) {
			sp.chosen = i
		}
	}
	if sp.sound.Clicked(gtx) {
		settings := c.u.voiceSettings()
		settings.StreamMute = !settings.StreamMute
		c.u.saveVoiceSettings()
	}
	for _, only := range []bool{false, true} {
		btn := &sp.everyone
		if only {
			btn = &sp.linkcordOnly
		}
		if btn.Clicked(gtx) && c.u.voiceSettings().StreamLinkcordOnly != only {
			c.u.voiceSettings().StreamLinkcordOnly = only
			c.u.saveVoiceSettings()
		}
	}
	nitro := c.hasNitro()
	for i := range sp.heights {
		if sp.heights[i].Clicked(gtx) && (nitro || streamHeights[i] != 0 && streamHeights[i] <= 720) {
			sp.height = streamHeights[i]
		}
	}
	for i := range sp.fpss {
		if sp.fpss[i].Clicked(gtx) && (nitro || streamFPS[i] <= 30) {
			sp.fps = streamFPS[i]
		}
	}
	if sp.goLive.Clicked(gtx) && len(sp.sources) > 0 {
		// Nothing chosen shares the whole screen: the main one.
		chosen := sp.chosen
		if chosen < 0 || chosen >= len(sp.sources) {
			chosen = mainScreen(sp.sources)
		}
		sp.open = false
		if sp.changing && c.u.streaming() {
			c.changeStream(sp.sources[chosen])
			return
		}
		settings := c.u.voiceSettings()
		settings.StreamHeight, settings.StreamFPS = sp.height, sp.fps
		c.u.saveVoiceSettings()
		c.startStream(sp.sources[chosen], golive.Quality{Height: sp.height, FPS: sp.fps})
	}
}

// layoutStreamPicker draws the window to choose what to share in the middle.
func (c *chatView) layoutStreamPicker(gtx layout.Context) {
	c.updateStreamPicker(gtx)
	sp := &c.u.voice.picker
	if !sp.open {
		return
	}
	th := c.u.th
	size := gtx.Constraints.Max
	// Clicking outside the window closes it.
	sp.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(640), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(40))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(16), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(bold(label(th, 20, colorHeader, "Screen Share")).Layout),
						layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							tab := func(i int, txt string) layout.FlexChild {
								return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return chip(gtx, th, &sp.tabs[i], txt, sp.screens == (i == 1), true)
									})
								})
							}
							return layout.Flex{}.Layout(gtx, tab(0, "Applications"), tab(1, "Screens"))
						}),
					)
				})
			}),
			// The screens or windows, three to a row, scrolling.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(330))
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				if sp.loading {
					return layout.Center.Layout(gtx, label(th, 14, colorMuted, "Looking for screens and windows…").Layout)
				}
				shown := sp.shown()
				if len(shown) == 0 {
					return layout.Center.Layout(gtx, label(th, 14, colorMuted, "Nothing to share here").Layout)
				}
				rows := (len(shown) + 2) / 3
				return listLayout(gtx, th, &sp.list, rows, func(gtx layout.Context, r int) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						var cells []layout.FlexChild
						for k := range 3 {
							if r*3+k >= len(shown) {
								cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
								}))
								continue
							}
							i := shown[r*3+k]
							if k > 0 {
								cells = append(cells, layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout))
							}
							cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return c.layoutSourceCard(gtx, i)
							}))
						}
						return layout.Flex{Alignment: layout.Start}.Layout(gtx, cells...)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.layoutStreamQuality(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, label(th, 11, colorMuted, golive.CodecNotice).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, &sp.close, colorSidebar, "Cancel")
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								bg := colorAccent
								if sp.loading || len(sp.sources) == 0 {
									bg = withAlpha(colorAccent, 0x60)
								}
								text := "Go Live"
								if sp.changing {
									text = "Change Stream"
								}
								return button(gtx, th, &sp.goLive, bg, text)
							}),
						)
					})
				})
			}),
		)
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, sp, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// layoutSourceCard is a screen or window to choose: its picture, outlined
// when chosen, and its name under it.
func (c *chatView) layoutSourceCard(gtx layout.Context, i int) layout.Dimensions {
	th := c.u.th
	sp := &c.u.voice.picker
	src := sp.sources[i]
	click := &sp.cards[i]
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		w := gtx.Constraints.Max.X
		h := w * 9 / 16
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				r := image.Rectangle{Max: image.Pt(w, h)}
				rr := clip.UniformRRect(r, gtx.Dp(6))
				paint.FillShape(gtx.Ops, colorServers, rr.Op(gtx.Ops))
				if img := sp.thumbs[i]; img.Size().X > 0 {
					st := rr.Push(gtx.Ops)
					igtx := gtx
					igtx.Constraints = layout.Exact(r.Max)
					widget.Image{Src: img, Fit: widget.Contain, Scale: 1 / gtx.Metric.PxPerDp}.Layout(igtx)
					st.Pop()
				}
				switch {
				case sp.chosen == i:
					bw := gtx.Dp(3)
					paint.FillShape(gtx.Ops, colorAccent, clip.Stroke{Path: clip.UniformRRect(r.Inset(bw/2), gtx.Dp(6)).Path(gtx.Ops), Width: float32(bw)}.Op())
				case click.Hovered():
					paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x20), rr.Op(gtx.Ops))
				}
				return layout.Dimensions{Size: r.Max}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := oneLine(label(th, 13, colorText, src.Title))
					if sp.chosen == i {
						l = bold(l)
					}
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// layoutStreamQuality is the choice of resolution and frame rate.
func (c *chatView) layoutStreamQuality(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	sp := &c.u.voice.picker
	nitro := c.hasNitro()
	row := func(title string, chips []layout.FlexChild) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, append([]layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(96)
						return label(th, 13, colorMuted, title).Layout(gtx)
					}),
				}, chips...)...)
			})
		})
	}
	var heights, rates []layout.FlexChild
	for i, h := range streamHeights {
		txt := fmt.Sprintf("%dp", h)
		if h == 0 {
			txt = "Source"
		}
		ok := nitro || h != 0 && h <= 720
		if !ok {
			txt += " · Nitro"
		}
		heights = append(heights, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return chip(gtx, th, &sp.heights[i], txt, sp.height == h, ok)
			})
		}))
	}
	for i, f := range streamFPS {
		txt := fmt.Sprintf("%d FPS", f)
		ok := nitro || f <= 30
		if !ok {
			txt += " · Nitro"
		}
		rates = append(rates, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return chip(gtx, th, &sp.fpss[i], txt, sp.fps == f, ok)
			})
		}))
	}
	soundText := "On"
	if c.u.voiceSettings().StreamMute {
		soundText = "Off"
	}
	sound := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return chip(gtx, th, &sp.sound, soundText, !c.u.voiceSettings().StreamMute, true)
	})}
	// Discord's own clients, or only Linkcord: they need what Linkcord's
	// stream sent before left out.
	only := c.u.voiceSettings().StreamLinkcordOnly
	who := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return chip(gtx, th, &sp.everyone, "Everyone", !only, true)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return chip(gtx, th, &sp.linkcordOnly, "Linkcord Only", only, true)
		}),
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(bold(label(th, 12, colorMuted, "STREAM QUALITY")).Layout),
		row("Resolution", heights),
		row("Frame Rate", rates),
		row("Sound", sound),
		row("Who Can Watch", who),
	)
}

// chip is a choice that is on or off, and can be locked.
func chip(gtx layout.Context, th *material.Theme, click *widget.Clickable, txt string, on, ok bool) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if ok {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		bg, fg := colorInput, colorText
		switch {
		case on:
			bg, fg = colorAccent, colorHeader
		case !ok:
			fg = colorMuted
		case click.Hovered():
			bg = colorSelected
		}
		return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(5), Bottom: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := label(th, 13, fg, txt)
				l.Alignment = text.Middle
				return l.Layout(gtx)
			})
		})
	})
}

// startStream shares src at q in the voice channel joined.
func (c *chatView) startStream(src golive.Source, q golive.Quality) {
	u := c.u
	v := &u.voice
	chID := u.voiceChannel()
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil {
		u.voiceInfo("Join a voice channel to share your screen")
		return
	}
	if c.demo {
		// The demo streams nothing, but shows itself live with a picture of
		// what it would share.
		v.demoStream = src.Thumb
		c.demoSetStreaming(ch, true)
		u.voiceInfo("You are live (demo)")
		return
	}
	if v.streamStarting || v.stream != nil {
		return
	}
	v.streamStarting = true
	u.voiceInfo("Starting your stream…")
	st := c.st
	params := golive.Params{
		Main:      st,
		Subscribe: func(h any) func() { return subscribeStream(c, h) },
		GuildID:   ch.GuildID,
		ChannelID: ch.ID,
		UserID:    c.me.ID,
		SessionID: c.inter.sessionID,
		Source:    src,
		Quality:   q,
		Sound:     !u.voiceSettings().StreamMute,
		// Only Linkcord shows it, if the user chose so.
		LinkcordOnly: u.voiceSettings().StreamLinkcordOnly,
		CodecDir:     filepath.Dir(logPath()),
	}
	var s *golive.Stream
	params.OnEnd = func(err error) {
		u.post(func() {
			if v.stream != nil && v.stream == s {
				v.stream = nil
				u.playSound(dcsound.StreamEnded)
				if err != nil {
					u.voiceInfo("Your stream ended: " + err.Error())
				}
			}
		})
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		started, err := golive.Start(ctx, params)
		// The stream server sometimes never answers a first try.
		if err != nil && ctx.Err() == nil && strings.Contains(err.Error(), "did not answer") {
			slog.Warn("the stream server did not answer; trying again", "err", err)
			started, err = golive.Start(ctx, params)
		}
		u.post(func() {
			v.streamStarting = false
			if err != nil {
				u.voiceFailed("Could not start the stream", err)
				return
			}
			// Left the channel meanwhile.
			if u.voiceChannel() != chID {
				go started.Stop()
				return
			}
			s = started
			v.stream = started
			u.playSound(dcsound.StreamStarted)
			u.voiceInfo("You are live")
		})
	}()
}

// subscribeStream adds the handler h of a stream to the state of c.
func subscribeStream(c *chatView, h any) func() {
	switch f := h.(type) {
	case func(*golive.StreamCreateEvent):
		return c.st.AddHandler(f)
	case func(*golive.StreamServerUpdateEvent):
		return c.st.AddHandler(f)
	case func(*golive.StreamUpdateEvent):
		return c.st.AddHandler(f)
	case func(*golive.StreamDeleteEvent):
		return c.st.AddHandler(f)
	}
	return func() {}
}

// streaming reports whether the user shares their screen or is starting to.
func (u *ui) streaming() bool {
	v := &u.voice
	return v.stream != nil || v.streamStarting || v.demoStream != nil
}

// stopStream stops sharing the screen.
func (u *ui) stopStream() {
	v := &u.voice
	if s := v.stream; s != nil {
		v.stream = nil
		u.playSound(dcsound.StreamEnded)
		go s.Stop()
	}
	if v.demoStream != nil {
		v.demoStream = nil
		if c := u.chat; c != nil {
			if ch, err := c.st.Cabinet.Channel(v.demoChannel); err == nil {
				c.demoSetStreaming(ch, false)
			}
		}
	}
}

// demoSetStreaming shows the user live, or not, in the demo's channel.
func (c *chatView) demoSetStreaming(ch *discord.Channel, on bool) {
	if vs, err := c.st.Cabinet.VoiceState(ch.GuildID, c.me.ID); err == nil {
		s := *vs
		s.SelfStream = on
		c.st.Cabinet.VoiceStateSet(ch.GuildID, &s, true)
		c.rebuildRows()
	}
}

// screenShareClicked starts choosing what to share, or offers to stop.
func (c *chatView) screenShareClicked() {
	u := c.u
	if !u.streaming() {
		c.openStreamPicker()
		return
	}
	c.openMenu(c.pointerAt, 200, func() []menuItem {
		return []menuItem{
			{text: "Stream Sound", check: checked(u.streamSound()), act: func() {
				u.toggleStreamSound()
				c.menu.stay = true
			}},
			{text: "Change Stream", act: func() {
				c.openStreamPicker()
				u.voice.picker.changing = true
			}},
			{sep: true},
			{text: "Stop Streaming", col: colorRed, act: u.stopStream},
		}
	})
}

// streamSound reports whether the stream carries the sound of what it
// shows.
func (u *ui) streamSound() bool {
	return !u.voiceSettings().StreamMute
}

// toggleStreamSound sends the sound of what is shared, or stops, and keeps
// the choice for the next stream.
func (u *ui) toggleStreamSound() {
	settings := u.voiceSettings()
	settings.StreamMute = !settings.StreamMute
	u.saveVoiceSettings()
	if s := u.voice.stream; s != nil {
		on := !settings.StreamMute
		go func() {
			s.SetSound(on)
			u.win.Invalidate()
		}()
	}
}

// viewerUsers are who watch the user's stream.
func (c *chatView) viewerUsers(guildID discord.GuildID) []discord.User {
	s := c.u.voice.stream
	if s == nil {
		return nil
	}
	var users []discord.User
	for _, id := range s.Viewers() {
		user := discord.User{ID: id}
		if m, err := c.st.Cabinet.Member(guildID, id); err == nil {
			user = m.User
		} else if u, err := c.st.Cabinet.User(id); err == nil {
			user = *u
		}
		users = append(users, user)
	}
	return users
}

// layoutViewerFaces draws the faces of who watches the user's stream at the
// top right of its big card, and, while the pointer is over them, their
// names under them.
func (c *chatView) layoutViewerFaces(gtx layout.Context, guildID discord.GuildID, size image.Point) {
	users := c.viewerUsers(guildID)
	if len(users) == 0 {
		return
	}
	th := c.u.th
	cv := &c.call
	pad := gtx.Dp(10)
	face := gtx.Dp(28)
	step := face * 3 / 4
	shown := min(len(users), 6)
	w := face + step*(shown-1)
	origin := image.Pt(size.X-pad-w, pad)
	hovered := cv.viewers.Update(gtx.Source)
	t := op.Offset(origin).Push(gtx.Ops)
	for i := shown - 1; i >= 0; i-- {
		u := users[i]
		ft := op.Offset(image.Pt(i*step, 0)).Push(gtx.Ops)
		// A ring of the card's black keeps the faces apart.
		ring := gtx.Dp(2)
		paint.FillShape(gtx.Ops, rgb(0x000000), clip.Ellipse(image.Rect(-ring, -ring, face+ring, face+ring)).Op(gtx.Ops))
		c.u.picture(gtx, c.authorAvatarURL(guildID, u), unit.Dp(float32(face)/gtx.Metric.PxPerDp), colorFor(uint64(u.ID)), u.DisplayOrUsername(), -1)
		ft.Pop()
	}
	area := clip.Rect{Max: image.Pt(w, face)}.Push(gtx.Ops)
	cv.viewers.Add(gtx.Ops)
	area.Pop()
	t.Pop()
	if !hovered {
		return
	}

	// The names, in a box under the faces.
	listW := min(gtx.Dp(220), size.X-2*pad)
	lgtx := gtx
	lgtx.Constraints = layout.Constraints{Min: image.Pt(listW, 0), Max: image.Pt(listW, size.Y-origin.Y-face-2*pad)}
	macro := op.Record(gtx.Ops)
	dims := rounded(lgtx, colorMenu, 6, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, bold(label(th, 12, colorMuted, fmt.Sprintf("WATCHING — %d", len(users)))).Layout)
			})}
			for _, u := range users {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return c.u.picture(gtx, c.authorAvatarURL(guildID, u), 20, colorFor(uint64(u.ID)), u.DisplayOrUsername(), -1)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Flexed(1, oneLine(label(th, 14, colorText, c.memberName(guildID, u.ID, nil))).Layout),
						)
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	lt := op.Offset(image.Pt(size.X-pad-dims.Size.X, origin.Y+face+gtx.Dp(6))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	lt.Pop()
}

// layoutViewers is who watches the user's stream, over its card: an eye,
// how many, and their pictures.
func (c *chatView) layoutViewers(gtx layout.Context, guildID discord.GuildID) layout.Dimensions {
	s := c.u.voice.stream
	if s == nil {
		return layout.Dimensions{}
	}
	ids := s.Viewers()
	th := c.u.th
	return rounded(gtx, withAlpha(rgb(0x000000), 0x99), 6, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3), Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconEye, 16, colorHeader) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Rigid(bold(label(th, 13, colorHeader, fmt.Sprint(len(ids)))).Layout),
			}
			for i, id := range ids {
				if i == 5 {
					break
				}
				user := discord.User{ID: id}
				if m, err := c.st.Cabinet.Member(guildID, id); err == nil {
					user = m.User
				} else if u, err := c.st.Cabinet.User(id); err == nil {
					user = *u
				}
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, c.authorAvatarURL(guildID, user), 18, colorFor(uint64(id)), user.DisplayOrUsername(), -1)
					})
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	})
}

// previewOp is the picture shown of what is shared, made again only when
// it changes.
func (v *voiceState) previewOp(img *image.RGBA) paint.ImageOp {
	if v.previewImg != img {
		v.previewImg, v.previewImgOp = img, paint.NewImageOp(img)
	}
	return v.previewImgOp
}

// streamPreview is the picture of what the user shares, or nil.
func (u *ui) streamPreview() *image.RGBA {
	v := &u.voice
	if v.stream != nil {
		return v.stream.Preview()
	}
	return v.demoStream
}

// liveBadge is the red "LIVE" of who shares their screen.
func liveBadge(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return rounded(gtx, colorRed, 4, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx,
			bold(label(th, 10, colorHeader, "LIVE")).Layout)
	})
}

// changeStream shares src in place of what the stream shares, without
// ending it.
func (c *chatView) changeStream(src golive.Source) {
	u := c.u
	v := &u.voice
	if v.demoStream != nil {
		v.demoStream = src.Thumb
		return
	}
	s := v.stream
	if s == nil {
		return
	}
	go func() {
		err := s.SetSource(src)
		u.post(func() {
			if err != nil {
				u.voiceFailed("Could not change the stream", err)
				return
			}
			u.voiceInfo("Your stream changed")
		})
	}()
}

// mainScreen is the index of the main screen of sources, or of the first
// screen, or 0.
func mainScreen(sources []golive.Source) int {
	first := -1
	for i, s := range sources {
		if s.Kind != golive.Screen {
			continue
		}
		if s.Primary {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return max(first, 0)
}
