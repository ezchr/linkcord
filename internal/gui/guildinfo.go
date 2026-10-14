package gui

import (
	"fmt"
	"image"
	"log/slog"
	"slices"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconEvents = mustIcon(icons.ActionEvent)
	iconBoost  = mustIcon(icons.ActionLoyalty)
)

// The rows of the boost goal, the events and the boosts at the top of a
// server's channels stand in for channels with IDs that no channel has.
const (
	boostGoalRowID = discord.ChannelID(3)
	eventsRowID    = discord.ChannelID(4)
	boostsRowID    = discord.ChannelID(5)
)

// boostLevels are the boosts each server level takes.
var boostLevels = [...]uint64{2, 7, 14}

// boostPerks are what each level unlocks, as Discord lists them.
var boostPerks = [...][]string{
	{"100 emoji and 15 sticker slots", "128 Kbps audio quality", "Animated server icon", "Custom invite background", "720p 60 FPS streams"},
	{"150 emoji and 30 sticker slots", "256 Kbps audio quality", "Server banner", "50 MB uploads", "1080p 60 FPS streams", "Custom role icons"},
	{"250 emoji and 60 sticker slots", "384 Kbps audio quality", "Animated server banner", "100 MB uploads", "Vanity URL"},
}

// boostGoal returns the level a server has with n boosts and the boosts the
// next level takes, or 0 at the top level.
func boostGoal(n uint64) (level int, next uint64) {
	for i, need := range boostLevels {
		if n < need {
			return i, need
		}
	}
	return len(boostLevels), 0
}

type infoPopup int

const (
	popupNone infoPopup = iota
	popupEvents
	popupBoosts
)

// guildInfo is the boost goal and the events of the selected server.
type guildInfo struct {
	events  map[discord.GuildID][]discord.GuildScheduledEvent
	loading map[discord.GuildID]bool
	popup   infoPopup
	close   widget.Clickable
	scroll  widget.List
}

func (g *guildInfo) init() {
	g.events = make(map[discord.GuildID][]discord.GuildScheduledEvent)
	g.loading = make(map[discord.GuildID]bool)
	g.scroll.Axis = layout.Vertical
}

// loadEvents fetches the scheduled events of a server, once.
func (c *chatView) loadEvents(guildID discord.GuildID) {
	g := &c.guild
	if c.demo || !guildID.IsValid() || g.loading[guildID] {
		return
	}
	if _, ok := g.events[guildID]; ok {
		return
	}
	g.loading[guildID] = true
	go func() {
		evs, err := c.st.Client.ListScheduledEvents(guildID, true)
		c.u.post(func() {
			delete(g.loading, guildID)
			if err != nil {
				slog.Warn("failed to load scheduled events", "err", err, "guild_id", guildID)
				return
			}
			g.events[guildID] = upcomingEvents(evs)
		})
	}()
}

// upcomingEvents keeps the events that have not ended, live ones first,
// then by when they start.
func upcomingEvents(evs []discord.GuildScheduledEvent) []discord.GuildScheduledEvent {
	evs = slices.DeleteFunc(slices.Clone(evs), func(e discord.GuildScheduledEvent) bool {
		return e.Status == discord.CompletedEvent || e.Status == discord.CancelledEvent
	})
	slices.SortFunc(evs, func(a, b discord.GuildScheduledEvent) int {
		if (a.Status == discord.ActiveEvent) != (b.Status == discord.ActiveEvent) {
			if a.Status == discord.ActiveEvent {
				return -1
			}
			return 1
		}
		return a.StartTime.Time().Compare(b.StartTime.Time())
	})
	return evs
}

// guildInfoRows returns the rows at the top of a server's channels.
func (c *chatView) guildInfoRows() []channelRow {
	return []channelRow{
		{kind: rowGuildInfo, ch: discord.Channel{ID: boostGoalRowID}},
		{kind: rowGuildInfo, ch: discord.Channel{ID: eventsRowID}, label: "Events"},
		{kind: rowGuildInfo, ch: discord.Channel{ID: boostsRowID}, label: "Server Boosts"},
	}
}

func (c *chatView) openGuildInfo(id discord.ChannelID) {
	c.guild.scroll.Position = layout.Position{}
	if id == eventsRowID {
		c.guild.popup = popupEvents
		c.loadEvents(c.selGuild)
	} else {
		c.guild.popup = popupBoosts
	}
}

// layoutGuildInfoRow is the boost goal, or the row of the events or of the
// boosts, at the top of a server's channels.
func (c *chatView) layoutGuildInfoRow(gtx layout.Context, r channelRow) layout.Dimensions {
	th := c.u.th
	guild, err := c.st.Cabinet.Guild(c.selGuild)
	if err != nil {
		return layout.Dimensions{}
	}
	cl := c.click(discord.Snowflake(r.ch.ID))
	switch r.ch.ID {
	case boostGoalRowID:
		level, next := boostGoal(guild.NitroBoosters)
		count := fmt.Sprintf("%d/%d Boosts", guild.NitroBoosters, next)
		progress := float32(1)
		if next > 0 {
			prev := uint64(0)
			if level > 0 {
				prev = boostLevels[level-1]
			}
			progress = float32(guild.NitroBoosters-prev) / float32(next-prev)
		} else {
			count = fmt.Sprintf("Level 3 · %d Boosts", guild.NitroBoosters)
		}
		return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				bg := colorSidebar
				if cl.Hovered() {
					bg = colorHover
				}
				return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return rounded(gtx, colorBoostChip, 10, func(gtx layout.Context) layout.Dimensions {
											return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx,
												bold(label(th, 12, colorHeader, "Boost Goal")).Layout)
										})
									}),
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										gtx.Constraints.Min.X = gtx.Constraints.Max.X
										return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
												layout.Rigid(bold(label(th, 12, colorMuted, count)).Layout),
												layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconChevron, 16, colorMuted) }),
											)
										})
									}),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return progressBar(gtx, progress, colorBoost)
								})
							}),
						)
					})
				})
			})
		})
	}

	ic := iconEvents
	count := 0
	if r.ch.ID == eventsRowID {
		count = len(c.guild.events[c.selGuild])
	} else {
		ic = iconBoost
	}
	children := []layout.FlexChild{}
	dims := rowItem(gtx, cl, func(gtx layout.Context, hovered bool) layout.Dimensions {
		col := colorChannel
		if hovered {
			col = colorHeader
		}
		children = children[:0]
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if ic == iconBoost {
					return boostGem(gtx, 20, colorBoost)
				}
				return icon(gtx, ic, 20, col)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, oneLine(label(th, 15, col, r.label)).Layout),
		)
		if count > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSelected, 8, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx,
						bold(label(th, 12, colorHeader, fmt.Sprint(count))).Layout)
				})
			}))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}, false)
	if r.ch.ID != boostsRowID {
		return dims
	}
	// A line separates these rows from the channels, as in Discord.
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dims }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
				paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: size}.Op())
				return layout.Dimensions{Size: size}
			})
		}),
	)
}

// progressBar is a thin bar filled to p, from 0 to 1, in col.
func progressBar(gtx layout.Context, p float32, col colorT) layout.Dimensions {
	p = min(max(p, 0), 1)
	h := gtx.Dp(6)
	w := gtx.Constraints.Max.X
	r := image.Rectangle{Max: image.Pt(w, h)}
	paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(r, h/2).Op(gtx.Ops))
	if fill := int(float32(w) * p); fill > 0 {
		paint.FillShape(gtx.Ops, col, clip.UniformRRect(image.Rect(0, 0, max(fill, h), h), h/2).Op(gtx.Ops))
	}
	return layout.Dimensions{Size: r.Max}
}

// layoutGuildPopup is the card of the events or the boosts of the server,
// over the chat.
func (c *chatView) layoutGuildPopup(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	g := &c.guild
	size := gtx.Constraints.Max
	if g.close.Clicked(gtx) {
		g.popup = popupNone
		return layout.Dimensions{Size: size}
	}
	guild, err := c.st.Cabinet.Guild(c.selGuild)
	if err != nil {
		g.popup = popupNone
		return layout.Dimensions{Size: size}
	}
	// Clicking outside the card closes it.
	g.close.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(460), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(64))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if g.popup == popupEvents {
				return c.layoutEventsCard(gtx, guild)
			}
			return c.layoutBoostsCard(gtx, guild)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, g, dims)
	call.Add(gtx.Ops)
	t.Pop()
	_ = th
	return layout.Dimensions{Size: size}
}

func (c *chatView) layoutBoostsCard(gtx layout.Context, guild *discord.Guild) layout.Dimensions {
	th := c.u.th
	level, next := boostGoal(guild.NitroBoosters)
	status := fmt.Sprintf("%d Boosts · Level %d", guild.NitroBoosters, level)
	goal := "This server has every level unlocked."
	progress := float32(1)
	if next > 0 {
		goal = fmt.Sprintf("%d more Boosts reach Level %d.", next-guild.NitroBoosters, level+1)
		progress = float32(guild.NitroBoosters) / float32(boostLevels[len(boostLevels)-1])
	}
	rows := []layout.FlexChild{
		layout.Rigid(bold(label(th, 20, colorHeader, "Server Boosts")).Layout),
		layout.Rigid(label(th, 14, colorMuted, guild.Name).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(bold(label(th, 15, colorBoost, status)).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return progressBar(gtx, progress, colorBoost) }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(label(th, 13, colorText, goal).Layout),
	}
	for i, perks := range boostPerks {
		unlocked := level > i
		head := fmt.Sprintf("LEVEL %d — %d BOOSTS", i+1, boostLevels[i])
		rows = append(rows,
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				col := colorMuted
				if unlocked {
					col = colorBoost
				}
				return bold(label(th, 12, col, head)).Layout(gtx)
			}),
		)
		for _, perk := range perks {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					col, ic := colorMuted, iconClose
					if unlocked {
						col, ic = colorText, iconCheck
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 16, col) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, label(th, 14, col, perk).Layout),
					)
				})
			}))
		}
	}
	return listLayout(gtx, th, &c.guild.scroll, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func (c *chatView) layoutEventsCard(gtx layout.Context, guild *discord.Guild) layout.Dimensions {
	th := c.u.th
	evs := c.guild.events[guild.ID]
	rows := []layout.FlexChild{
		layout.Rigid(bold(label(th, 20, colorHeader, fmt.Sprintf("Events — %d", len(evs)))).Layout),
		layout.Rigid(label(th, 14, colorMuted, guild.Name).Layout),
	}
	switch {
	case c.guild.loading[guild.ID]:
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, label(th, 14, colorMuted, "Loading events…").Layout)
		}))
	case len(evs) == 0:
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, label(th, 14, colorMuted, "There are no upcoming events.").Layout)
		}))
	}
	for _, e := range evs {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						when, whenCol := formatTime(e.StartTime.Time()), colorMuted
						if e.Status == discord.ActiveEvent {
							when, whenCol = "Live now", colorGreen
						}
						where := ""
						switch {
						case e.EntityMetadata != nil && e.EntityMetadata.Location != "":
							where = e.EntityMetadata.Location
						case e.ChannelID.IsValid():
							if ch, err := c.st.Cabinet.Channel(e.ChannelID); err == nil {
								where = "🔊 " + ch.Name
							}
						}
						lines := []layout.FlexChild{
							layout.Rigid(bold(label(th, 12, whenCol, when)).Layout),
							layout.Rigid(bold(label(th, 16, colorHeader, e.Name)).Layout),
						}
						if e.Description != "" {
							lines = append(lines, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, label(th, 14, colorText, e.Description).Layout)
							}))
						}
						foot := where
						if e.UserCount > 0 {
							if foot != "" {
								foot += " · "
							}
							foot += fmt.Sprintf("%d interested", e.UserCount)
						}
						if foot != "" {
							lines = append(lines, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, label(th, 13, colorMuted, foot).Layout)
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
					})
				})
			})
		}))
	}
	return listLayout(gtx, th, &c.guild.scroll, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}
