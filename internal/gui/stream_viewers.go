package gui

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/golive"
)

// watchViewers keeps who watches each stream, as Discord tells its streamer
// and those who watch it.
func (c *chatView) watchViewers() func() {
	set := func(key string, ids []discord.UserID) {
		c.u.post(func() {
			if c.viewers == nil {
				c.viewers = make(map[string][]discord.UserID)
			}
			c.viewers[key] = ids
		})
	}
	stops := []func(){
		c.st.AddHandler(func(ev *golive.StreamCreateEvent) { set(ev.StreamKey, ev.ViewerIDs) }),
		c.st.AddHandler(func(ev *golive.StreamUpdateEvent) { set(ev.StreamKey, ev.ViewerIDs) }),
		c.st.AddHandler(func(ev *golive.StreamDeleteEvent) {
			c.u.post(func() { delete(c.viewers, ev.StreamKey) })
		}),
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// streamViewers are who watch the stream of vs's user, as far as Discord
// said, and whether it did.
func (c *chatView) streamViewers(vs discord.VoiceState) ([]discord.UserID, bool) {
	ids, ok := c.viewers[c.streamKeyOf(vs)]
	if c.demo && !ok && vs.SelfStream {
		return []discord.UserID{c.me.ID}, true
	}
	return ids, ok
}

// layoutStreamViewers is an eye with how many watch, and the first of them,
// overlapping, as Discord shows them.
func (c *chatView) layoutStreamViewers(gtx layout.Context, guildID discord.GuildID, ids []discord.UserID) layout.Dimensions {
	th := c.u.th
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconEye, 16, colorHeader) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(bold(label(th, 13, colorHeader, fmt.Sprint(len(ids)))).Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			n := min(len(ids), 6)
			if n == 0 {
				return layout.Dimensions{}
			}
			s, step := gtx.Dp(20), gtx.Dp(14)
			for i := n - 1; i >= 0; i-- {
				id := ids[i]
				user := discord.User{ID: id}
				if u, err := c.st.Cabinet.User(id); err == nil {
					user = *u
				} else if id == c.me.ID {
					user = c.me
				}
				t := op.Offset(image.Pt(i*step, 0)).Push(gtx.Ops)
				c.u.picture(gtx, c.authorAvatarURL(guildID, user), 20, colorFor(uint64(id)), user.DisplayOrUsername(), -1)
				t.Pop()
			}
			return layout.Dimensions{Size: image.Pt((n-1)*step+s, s)}
		}),
	)
}
