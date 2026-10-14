package gui

import (
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// profileBadge is a badge of a profile, as Discord sends it: what it means
// and where it leads.
type profileBadge struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Link        string `json:"link"`
}

// animatedAvatarURL is the avatar of user as the profile card shows it:
// their server profile's in a server, moving if it moves.
func (c *chatView) animatedAvatarURL(guildID discord.GuildID, user discord.User) string {
	hash, url := user.Avatar, user.AvatarURLWithType(discord.PNGImage)
	if guildID.IsValid() {
		if m, err := c.st.Cabinet.Member(guildID, user.ID); err == nil && m.Avatar != "" {
			hash, url = m.Avatar, m.AvatarURLWithType(discord.PNGImage, guildID)
		}
	}
	if strings.HasPrefix(hash, "a_") {
		url = strings.Replace(url, ".png", ".gif", 1)
	}
	return cdnSize(url)
}

// layoutCardAvatar is the profile card's avatar: moving, in its
// decoration, with the status dot, as Discord's card shows it.
func (c *chatView) layoutCardAvatar(gtx layout.Context, p *profileView, user discord.User, size unit.Dp, ring colorT) layout.Dimensions {
	dims := c.u.picture(gtx, c.animatedAvatarURL(p.guildID, user), size, colorFor(uint64(user.ID)), user.DisplayOrUsername(), -1)
	deco := user.AvatarDecoration
	if p.data != nil && p.data.User.AvatarDecoration != nil {
		deco = p.data.User.AvatarDecoration
	}
	if deco != nil && deco.Asset != "" {
		c.u.decoration(gtx, deco.URL(true), dims.Size.X)
	}
	statusDot(gtx, c.status(p.guildID, user.ID), dims.Size.X, ring)
	return dims
}

// layoutBadge is a badge of the profile, which a click opens, with what it
// means and where it leads.
func (c *chatView) layoutBadge(gtx layout.Context, p *profileView, b profileBadge) layout.Dimensions {
	if p.badgeClicks == nil {
		p.badgeClicks = make(map[string]*widget.Clickable)
	}
	cl := clickOf(p.badgeClicks, b.ID)
	if cl.Clicked(gtx) {
		if p.openBadge == b.ID {
			p.openBadge = ""
		} else {
			p.openBadge = b.ID
		}
	}
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return c.u.picture(gtx, "https://cdn.discordapp.com/badge-icons/"+b.Icon+".png", 20, colorPopup, "", 0)
	})
}

// layoutOpenBadge is the badge clicked, under the badges: big, what it
// means, and a button to where it leads, if anywhere.
func (c *chatView) layoutOpenBadge(gtx layout.Context, p *profileView, badges []profileBadge) layout.Dimensions {
	var b *profileBadge
	for i := range badges {
		if badges[i].ID == p.openBadge {
			b = &badges[i]
		}
	}
	if b == nil {
		return layout.Dimensions{}
	}
	th := c.u.th
	if p.badgeLink.Clicked(gtx) && b.Link != "" {
		openLink(b.Link)
	}
	return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, "https://cdn.discordapp.com/badge-icons/"+b.Icon+".png", 36, colorMenu, "", 0)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						rows := []layout.FlexChild{layout.Rigid(label(th, 14, colorHeader, b.Description).Layout)}
						if b.Link != "" {
							rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return p.badgeLink.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										pointer.CursorPointer.Add(gtx.Ops)
										return label(th, 13, colorLink, "Learn more").Layout(gtx)
									})
								})
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					}))
			})
		})
	})
}

// Profile effects are the animations over a whole profile card, such as
// falling petals. A profile tells only the effect's ID; what it is made of
// is in Discord's list of effects, fetched once and kept on disk.

// profileEffect is an effect: its layers, each a moving picture laid over
// the card at a place, from a moment, for a time, looping or not.
type profileEffect struct {
	ID      string `json:"id"`
	Effects []struct {
		Src       string `json:"src"`
		Loop      bool   `json:"loop"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  int    `json:"duration"`
		Start     int    `json:"start"`
		LoopDelay int    `json:"loopDelay"`
		Position  struct {
			X int `json:"x"`
			Y int `json:"y"`
		} `json:"position"`
		ZIndex int `json:"zIndex"`
	} `json:"effects"`
}

// effectWidth is the width of the card Discord's effects are drawn for.
const effectWidth = 450

var profileEffects struct {
	sync.Mutex
	byID     map[string]*profileEffect
	fetching bool
	fetched  time.Time
}

func profileEffectsPath() string {
	return filepath.Join(filepath.Dir(logPath()), "profile-effects.json")
}

// profileEffect is the effect id, or nil while Discord's list is fetched
// or if it is not in it.
func (c *chatView) profileEffect(id string) *profileEffect {
	pe := &profileEffects
	pe.Lock()
	defer pe.Unlock()
	if pe.byID == nil {
		pe.byID = make(map[string]*profileEffect)
		if data, err := os.ReadFile(profileEffectsPath()); err == nil {
			loadProfileEffects(data, pe.byID)
		}
	}
	e := pe.byID[id]
	// An effect not known yet is asked for, at most once an hour.
	if e == nil && !pe.fetching && time.Since(pe.fetched) > time.Hour && !c.demo {
		pe.fetching = true
		go func() {
			var raw json.RawMessage
			err := c.st.Client.RequestJSONInto(&raw, "GET", api.Endpoint+"user-profile-effects")
			pe.Lock()
			pe.fetching, pe.fetched = false, time.Now()
			if err == nil {
				loadProfileEffects(raw, pe.byID)
				writeFile(profileEffectsPath(), raw)
			} else {
				slog.Warn("could not get the profile effects", "err", err)
			}
			pe.Unlock()
			c.u.win.Invalidate()
		}()
	}
	return e
}

func loadProfileEffects(data []byte, into map[string]*profileEffect) {
	var list struct {
		Configs []*profileEffect `json:"profile_effect_configs"`
	}
	if json.Unmarshal(data, &list) != nil {
		return
	}
	for _, e := range list.Configs {
		into[e.ID] = e
	}
}

// layoutProfileEffect draws the effect over a card of size, playing since
// the card opened.
func (c *chatView) layoutProfileEffect(gtx layout.Context, e *profileEffect, size image.Point, since time.Time) {
	if e == nil || c.u.images.lowPower {
		return
	}
	k := float32(size.X) / effectWidth
	elapsed := int(gtx.Now.Sub(since).Milliseconds())
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
	pending := false
	for _, l := range e.Effects {
		if l.Src == "" || elapsed < l.Start {
			pending = true
			continue
		}
		// A layer that does not loop shows once, for its time.
		if !l.Loop && l.Duration > 0 && elapsed > l.Start+l.Duration {
			continue
		}
		w, h := int(float32(l.Width)*k), int(float32(l.Height)*k)
		if w <= 0 || h <= 0 {
			continue
		}
		img := c.u.images.frame(gtx, l.Src, max(w, h))
		if img == nil {
			pending = true
			continue
		}
		t := op.Offset(image.Pt(int(float32(l.Position.X)*k), int(float32(l.Position.Y)*k))).Push(gtx.Ops)
		igtx := gtx
		igtx.Constraints = layout.Exact(image.Pt(w, h))
		widget.Image{Src: *img, Fit: widget.Fill}.Layout(igtx)
		t.Pop()
	}
	if pending {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(50 * time.Millisecond)})
	}
}
