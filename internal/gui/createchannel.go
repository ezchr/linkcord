package gui

import (
	"image"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// createChannel is the form that makes a channel or a category, opened from
// the server's menu, as Discord's.
type createChannel struct {
	open    bool
	guildID discord.GuildID
	// parent is the category it goes in, if any.
	parent   discord.ChannelID
	category bool
	kind     discord.ChannelType
	name     widget.Editor
	private  bool
	focused  bool
	busy     bool
	err      string

	kinds                                [3]widget.Clickable
	privateBtn, cancel, create, close, x widget.Clickable
}

// channelKinds are the kinds of channel the form makes.
var channelKinds = [3]struct {
	kind        discord.ChannelType
	name, about string
}{
	{discord.GuildText, "Text", "Send messages, images, GIFs, emoji, opinions, and puns"},
	{discord.GuildVoice, "Voice", "Hang out together with voice, video, and screen share"},
	{discord.GuildForum, "Forum", "Create a space for organized discussions"},
}

// openCreateChannel opens the form to make a channel, or a category.
func (c *chatView) openCreateChannel(guildID discord.GuildID, category bool) {
	cc := &c.newChannel
	*cc = createChannel{open: true, guildID: guildID, category: category, kind: discord.GuildText}
	if category {
		cc.kind = discord.GuildCategory
	}
	cc.name.SingleLine, cc.name.Submit = true, true
	cc.name.MaxLen = 100
}

func (c *chatView) submitCreateChannel() {
	cc := &c.newChannel
	name := strings.TrimSpace(cc.name.Text())
	if name == "" || cc.busy {
		return
	}
	if cc.kind == discord.GuildText || cc.kind == discord.GuildForum {
		// As Discord names them as they are typed.
		name = strings.ToLower(strings.Join(strings.Fields(name), "-"))
	}
	body := map[string]any{"name": name, "type": cc.kind}
	if cc.parent.IsValid() {
		body["parent_id"] = cc.parent.String()
	}
	if cc.private {
		// Only the user sees it, until others are let in.
		view := strconv.FormatUint(uint64(discord.PermissionViewChannel), 10)
		body["permission_overwrites"] = []map[string]any{
			{"id": cc.guildID.String(), "type": 0, "deny": view, "allow": "0"},
			{"id": c.me.ID.String(), "type": 1, "allow": view, "deny": "0"},
		}
	}
	guildID := cc.guildID
	if c.demo {
		ch := discord.Channel{ID: discord.ChannelID(discord.NewSnowflake(time.Now())), GuildID: guildID, Name: name, Type: cc.kind, Position: 999, ParentID: cc.parent}
		c.st.Cabinet.ChannelSet(&ch, false)
		cc.open = false
		c.rebuildRows()
		return
	}
	cc.busy, cc.err = true, ""
	go func() {
		ch, err := c.st.Client.RequestJSON[discord.Channel]("POST", api.EndpointGuilds+guildID.String()+"/channels", httputil.WithJSONBody(body))
		c.u.post(func() {
			cc.busy = false
			if err != nil {
				cc.err = "Discord did not make it: " + err.Error()
				return
			}
			cc.open = false
			if isTextual(ch.Type) && c.selGuild == guildID {
				c.selectChannel(ch)
			}
		})
	}()
}

// layoutCreateChannel draws the form over everything.
func (c *chatView) layoutCreateChannel(gtx layout.Context) {
	th := c.u.th
	cc := &c.newChannel
	size := gtx.Constraints.Max
	if cc.close.Clicked(gtx) || cc.x.Clicked(gtx) || cc.cancel.Clicked(gtx) {
		cc.open = false
		return
	}
	for i := range cc.kinds {
		if cc.kinds[i].Clicked(gtx) {
			cc.kind = channelKinds[i].kind
		}
	}
	if cc.privateBtn.Clicked(gtx) {
		cc.private = !cc.private
	}
	if cc.create.Clicked(gtx) {
		c.submitCreateChannel()
	}
	for {
		e, ok := cc.name.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			c.submitCreateChannel()
		}
	}
	for {
		e, ok := gtx.Event(key.Filter{Focus: &cc.name, Name: key.NameEscape})
		if !ok {
			break
		}
		if ev, ok := e.(key.Event); ok && ev.State == key.Press {
			cc.open = false
			return
		}
	}
	if !cc.open {
		return
	}
	if !cc.focused {
		cc.focused = true
		gtx.Execute(key.FocusCmd{Tag: &cc.name})
	}

	cc.close.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	title, nameTitle, hint, privTitle, privAbout, submit := "Create Channel", "Channel Name", "new-channel", "Private Channel",
		"Only selected members and roles will be able to view this channel.", "Create Channel"
	if cc.category {
		title, nameTitle, hint, privTitle, privAbout, submit = "Create Category", "Category Name", "New Category", "Private Category",
			"By making a category private, only selected members and roles will be able to view it. Channels in it will match its settings.", "Create Category"
	}
	w := min(gtx.Dp(460), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(64))}
	macro := op.Record(gtx.Ops)
	var rows []layout.FlexChild
	rows = append(rows,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, bold(label(th, 20, colorHeader, title)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return cc.x.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return icon(gtx, iconClose, 24, colorMuted)
					})
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
	)
	if !cc.category {
		rows = append(rows, layout.Rigid(bold(label(th, 14, colorHeader, "Channel Type")).Layout), layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout))
		for i, k := range channelKinds {
			i, k := i, k
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return kindRow(gtx, th, &cc.kinds[i], k.kind, k.name, k.about, cc.kind == k.kind)
			}))
		}
		rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout))
	}
	rows = append(rows,
		layout.Rigid(bold(label(th, 14, colorHeader, nameTitle)).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &cc.name, hint) }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &cc.privateBtn, "🔒 "+privTitle, privAbout, cc.private)
		}),
	)
	if cc.err != "" {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, cc.err).Layout)
		}))
	}
	rows = append(rows,
		layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			half := func(click *widget.Clickable, bg colorT, txt string) layout.FlexChild {
				return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return button(gtx, th, click, bg, txt)
				})
			}
			create := colorAccent
			if strings.TrimSpace(cc.name.Text()) == "" || cc.busy {
				create = withAlpha(colorAccent, 0x80)
			}
			return layout.Flex{}.Layout(gtx,
				half(&cc.cancel, colorSecondaryButton, "Cancel"),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				half(&cc.create, create, submit),
			)
		}),
	)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, cc, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// kindRow is a kind of channel to choose: a round box, its icon, name and
// what it is for.
func kindRow(gtx layout.Context, th *material.Theme, click *widget.Clickable, kind discord.ChannelType, name, about string, on bool) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorSidebar
			switch {
			case on:
				bg = colorSelected
			case click.Hovered():
				bg = colorHover
			}
			return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							d := gtx.Dp(20)
							col := colorMuted
							if on {
								col = colorAccent
							}
							sw := float32(max(gtx.Dp(2), 1))
							paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Ellipse(image.Rect(1, 1, d-1, d-1)).Path(gtx.Ops), Width: sw}.Op())
							if on {
								in := gtx.Dp(5)
								paint.FillShape(gtx.Ops, colorAccent, clip.Ellipse(image.Rect(in, in, d-in, d-in)).Op(gtx.Ops))
							}
							return layout.Dimensions{Size: image.Pt(d, d)}
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Flexed(1, stacked(
							func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										switch kind {
										case discord.GuildVoice:
											return icon(gtx, iconVoice, 18, colorText)
										case discord.GuildForum:
											return icon(gtx, iconMenuThreads, 18, colorText)
										}
										return label(th, 18, colorText, "#").Layout(gtx)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
									layout.Rigid(label(th, 16, colorHeader, name).Layout),
								)
							},
							label(th, 13, colorMuted, about).Layout,
						)),
					)
				})
			})
		})
	})
}
