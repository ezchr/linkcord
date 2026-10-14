package gui

import (
	"cmp"
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// iconMusic marks music being listened to, as Discord's note.
var iconMusic = mustIcon(icons.ImageMusicNote)

// presence returns what the user is doing, looked up in the server first and
// then among friends, or nil if it is not known.
func (c *chatView) presence(guildID discord.GuildID, userID discord.UserID) *discord.Presence {
	if guildID.IsValid() {
		if p, err := c.st.Cabinet.Presence(guildID, userID); err == nil {
			return p
		}
	}
	if p, err := c.st.Cabinet.Presence(discord.NullGuildID, userID); err == nil {
		return p
	}
	return nil
}

// status returns whether the user is online, idle, busy or offline.
func (c *chatView) status(guildID discord.GuildID, userID discord.UserID) discord.Status {
	if userID == c.me.ID {
		if s := c.st.Status(); s != "" {
			return s
		}
	}
	if p := c.presence(guildID, userID); p != nil && p.Status != "" {
		return p.Status
	}
	return discord.OfflineStatus
}

func statusColor(s discord.Status) colorT {
	switch s {
	case discord.OnlineStatus:
		return colorGreen
	case discord.IdleStatus:
		return colorYellow
	case discord.DoNotDisturbStatus:
		return colorRed
	}
	return colorOffline
}

func statusName(s discord.Status) string {
	switch s {
	case discord.OnlineStatus:
		return "Online"
	case discord.IdleStatus:
		return "Idle"
	case discord.DoNotDisturbStatus:
		return "Do not disturb"
	case discord.InvisibleStatus:
		return "Invisible"
	}
	return "Offline"
}

// statusRank orders members as Discord does: online, idle, busy, offline.
func statusRank(s discord.Status) int {
	switch s {
	case discord.OnlineStatus:
		return 0
	case discord.IdleStatus:
		return 1
	case discord.DoNotDisturbStatus:
		return 2
	}
	return 3
}

// customStatus returns the text of the custom status, with its emoji if it
// is a plain one.
func customStatus(p *discord.Presence) string {
	if p == nil {
		return ""
	}
	for _, a := range p.Activities {
		if a.Type != discord.CustomActivity {
			continue
		}
		txt := a.State
		if a.Emoji != nil && !a.Emoji.ID.IsValid() && a.Emoji.Name != "" {
			txt = strings.TrimSpace(a.Emoji.Name + " " + txt)
		}
		return txt
	}
	return ""
}

// activityLine returns one line saying what the user does, as the member
// list of Discord does: the custom status, or the game, music or stream.
func activityLine(p *discord.Presence) string {
	if s := customStatus(p); s != "" {
		return s
	}
	if p == nil {
		return ""
	}
	for _, a := range p.Activities {
		if a.Type != discord.CustomActivity {
			return activityVerb(a)
		}
	}
	return ""
}

// layoutActivityLine is the line under a name of what someone does: music
// shows as a note and what plays, as on Discord, the rest as line says.
func layoutActivityLine(gtx layout.Context, th *material.Theme, p *discord.Presence, line string) layout.Dimensions {
	if customStatus(p) == "" && p != nil {
		for _, a := range p.Activities {
			if a.Type == discord.CustomActivity {
				continue
			}
			if a.Type == discord.ListeningActivity {
				what := cmp.Or(a.State, a.Details, a.Name)
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMusic, 13, colorGreen) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
					layout.Flexed(1, oneLine(label(th, 12, colorMuted, what)).Layout),
				)
			}
			break
		}
	}
	return oneLine(label(th, 12, colorMuted, line)).Layout(gtx)
}

// activityVerb is the header of an activity, such as "Playing Minecraft".
func activityVerb(a discord.Activity) string {
	switch a.Type {
	case discord.GameActivity:
		return "Playing " + a.Name
	case discord.StreamingActivity:
		return "Streaming " + a.Name
	case discord.ListeningActivity:
		return "Listening to " + a.Name
	case discord.WatchingActivity:
		return "Watching " + a.Name
	case discord.CompetingActivity:
		return "Competing in " + a.Name
	}
	return a.Name
}

// activityImageURL returns the URL of an activity image, which can be an
// asset of the application, a Spotify cover or an external image proxied by
// Discord.
func activityImageURL(a discord.Activity, img string) string {
	switch {
	case img == "":
		return ""
	case strings.HasPrefix(img, "mp:"):
		return "https://media.discordapp.net/" + strings.TrimPrefix(img, "mp:")
	case strings.HasPrefix(img, "spotify:"):
		return "https://i.scdn.co/image/" + strings.TrimPrefix(img, "spotify:")
	case a.AppID.IsValid():
		return "https://cdn.discordapp.com/app-assets/" + a.AppID.String() + "/" + img + ".png"
	}
	return ""
}

// elapsed formats how long ago t was, as m:ss or h:mm:ss.
func elapsed(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < 0 {
		d = 0
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// avatarStatus lays out the picture of a user with the dot of their status in
// the bottom right corner, cut out of ring, the color behind it.
func (c *chatView) avatarStatus(gtx layout.Context, user discord.User, guildID discord.GuildID, size unit.Dp, ring colorT) layout.Dimensions {
	return c.avatarStatusSpeaking(gtx, user, guildID, size, ring, false)
}

// avatarStatusSpeaking is avatarStatus with the green ring of who is
// speaking around the picture if speaking, as Discord shows it.
func (c *chatView) avatarStatusSpeaking(gtx layout.Context, user discord.User, guildID discord.GuildID, size unit.Dp, ring colorT, speaking bool) layout.Dimensions {
	// The picture of their server profile in a server, as the member list
	// and the chat show it.
	dims := c.u.picture(gtx, c.authorAvatarURL(guildID, user), size, colorFor(uint64(user.ID)), user.DisplayOrUsername(), -1)
	if speaking {
		speakingRing(gtx, dims.Size.X, ring)
	}
	statusDot(gtx, c.status(guildID, user.ID), dims.Size.X, ring)
	return dims
}

// speakingRing draws the green ring of who is speaking over the edge of a
// round picture of px pixels, with a thin gap of the color bg inside it.
func speakingRing(gtx layout.Context, px int, bg colorT) {
	w := max(gtx.Dp(2), 2)
	gap := max(gtx.Dp(1), 1)
	r := image.Rect(0, 0, px, px)
	stroke := func(c colorT, inset, width int) {
		e := clip.Ellipse(r.Inset(inset + width/2))
		paint.FillShape(gtx.Ops, c, clip.Stroke{Path: e.Path(gtx.Ops), Width: float32(width)}.Op())
	}
	stroke(bg, w, gap)
	stroke(colorGreen, 0, w)
}

// statusDot draws the status dot over the bottom right corner of an avatar
// of px pixels.
func statusDot(gtx layout.Context, s discord.Status, px int, ring colorT) {
	d := px * 3 / 10
	border := max(px/16, 2)
	r := image.Rect(px-d, px-d, px, px)
	paint.FillShape(gtx.Ops, ring, clip.Ellipse(r.Inset(-border)).Op(gtx.Ops))
	drawStatus(gtx, s, r, ring)
}

// statusIcon is the status shape on its own, px wide, over the color bg.
func statusIcon(gtx layout.Context, s discord.Status, px int, bg colorT) layout.Dimensions {
	drawStatus(gtx, s, image.Rect(0, 0, px, px), bg)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// drawStatus draws the shape of a status in r, cutting its holes in bg: a
// dot, a moon, a dash or a ring.
func drawStatus(gtx layout.Context, s discord.Status, r image.Rectangle, ring colorT) {
	d := r.Dx()
	paint.FillShape(gtx.Ops, statusColor(s), clip.Ellipse(r).Op(gtx.Ops))
	// The holes stay inside the dot.
	defer clip.Ellipse(r).Push(gtx.Ops).Pop()
	switch s {
	case discord.IdleStatus:
		// A moon: the dot with a bite taken out of its top left.
		bite := image.Rect(r.Min.X-d/8, r.Min.Y-d/8, r.Min.X+d*5/8, r.Min.Y+d*5/8)
		paint.FillShape(gtx.Ops, ring, clip.Ellipse(bite).Op(gtx.Ops))
	case discord.DoNotDisturbStatus:
		bar := image.Rect(r.Min.X+d/5, r.Min.Y+d*2/5, r.Max.X-d/5, r.Max.Y-d*2/5)
		paint.FillShape(gtx.Ops, ring, clip.UniformRRect(bar, max(bar.Dy()/2, 1)).Op(gtx.Ops))
	case discord.OfflineStatus, discord.InvisibleStatus:
		hole := r.Inset(d / 4)
		paint.FillShape(gtx.Ops, ring, clip.Ellipse(hole).Op(gtx.Ops))
	}
}
