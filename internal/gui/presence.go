package gui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

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
	dims := c.u.picture(gtx, cdnSize(user.AvatarURLWithType(discord.PNGImage)), size, colorFor(uint64(user.ID)), user.DisplayOrUsername(), -1)
	statusDot(gtx, c.status(guildID, user.ID), dims.Size.X, ring)
	return dims
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
