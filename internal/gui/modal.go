package gui

import (
	"fmt"
	"image"
	"log/slog"
	"strings"
	"unicode/utf8"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// modalCreateEvent is sent when a bot answers an interaction with a form,
// which Discord shows over the chat. Arikawa knows it only for bots.
type modalCreateEvent struct {
	ID          string                     `json:"id"`
	Nonce       string                     `json:"nonce"`
	ChannelID   discord.ChannelID          `json:"channel_id"`
	Title       string                     `json:"title"`
	CustomID    string                     `json:"custom_id"`
	Components  discord.TopLevelComponents `json:"components"`
	Application struct {
		ID   discord.AppID `json:"id"`
		Name string        `json:"name"`
		Icon string        `json:"icon"`
	} `json:"application"`
}

// modalField is a field of a form: a text box or a menu.
type modalField struct {
	label, description string
	required           bool
	customID           discord.ComponentID
	// inLabel reports whether the field came in a label, the newer form
	// layout, rather than in an action row; the answer goes the same way.
	inLabel bool

	text        bool
	paragraph   bool
	ed          widget.Editor
	maxLength   int
	placeholder string

	options  []discord.SelectOption
	chosen   int
	open     bool
	box      widget.Clickable
	optClick []widget.Clickable
}

// modalView is the form of a bot, over the chat.
type modalView struct {
	ev      modalCreateEvent
	guildID discord.GuildID
	fields  []*modalField
	// texts are the paragraphs of a form that only explain.
	texts []string

	// local, if set, is given the answers in place of a bot, for the forms
	// of the app itself; submitText names its button, red if danger.
	local      func(values []string)
	submitText string
	danger     bool

	submit, cancel widget.Clickable
	list           widget.List
	nonce          string
	sending        bool
	err            string
	focused        bool
}

// openModal shows the form a bot answered with.
func (c *chatView) openModal(ev modalCreateEvent) {
	mv := &modalView{ev: ev}
	mv.list.Axis = layout.Vertical
	if c.selChannel != nil {
		mv.guildID = c.selChannel.GuildID
	}
	add := func(comp discord.Component, lbl, desc string, inLabel bool) {
		switch v := comp.(type) {
		case *discord.TextInputComponent:
			f := &modalField{label: lbl, description: desc, required: v.Required, customID: v.CustomID, inLabel: inLabel, text: true}
			if f.label == "" {
				f.label = v.Label
			}
			f.paragraph = v.Style == discord.TextInputParagraphStyle
			f.ed.SingleLine = !f.paragraph
			f.ed.SetText(v.Value)
			f.maxLength = v.LengthLimits[1]
			f.placeholder = v.Placeholder
			mv.fields = append(mv.fields, f)
		case *discord.StringSelectComponent:
			f := &modalField{label: lbl, description: desc, required: v.Required, customID: v.CustomID, inLabel: inLabel, options: v.Options, chosen: -1}
			for i, o := range v.Options {
				if o.Default {
					f.chosen = i
				}
			}
			f.optClick = make([]widget.Clickable, len(v.Options))
			mv.fields = append(mv.fields, f)
		}
	}
	for _, comp := range ev.Components {
		switch v := comp.(type) {
		case *discord.ActionRowComponent:
			for _, child := range *v {
				add(child, "", "", false)
			}
		case *discord.LabelComponent:
			add(v.Component, v.Label, v.Description, true)
		case *discord.TextDisplayComponent:
			mv.texts = append(mv.texts, v.Content)
		}
	}
	c.inter.modal = mv
}

// submitModal sends the answers of the form, once the required fields are
// filled.
func (c *chatView) submitModal() {
	ip := &c.inter
	mv := ip.modal
	if mv == nil || mv.sending {
		return
	}
	var comps []map[string]any
	var values []string
	for _, f := range mv.fields {
		var inner map[string]any
		if f.text {
			value := f.ed.Text()
			values = append(values, value)
			if f.required && strings.TrimSpace(value) == "" {
				mv.err = fmt.Sprintf("“%s” is required.", f.label)
				return
			}
			inner = map[string]any{"type": int(discord.TextInputComponentType), "custom_id": f.customID, "value": value}
		} else {
			chosen := []string{}
			if f.chosen >= 0 {
				chosen = append(chosen, f.options[f.chosen].Value)
				values = append(values, f.options[f.chosen].Value)
			} else if f.required {
				mv.err = fmt.Sprintf("Choose an option for “%s”.", f.label)
				return
			} else {
				values = append(values, "")
			}
			inner = map[string]any{"type": int(discord.StringSelectComponentType), "custom_id": f.customID, "values": chosen}
		}
		if f.inLabel {
			comps = append(comps, map[string]any{"type": int(discord.LabelComponentType), "component": inner})
		} else {
			comps = append(comps, map[string]any{"type": int(discord.ActionRowComponentType), "components": []any{inner}})
		}
	}
	if mv.local != nil {
		ip.modal = nil
		mv.local(values)
		return
	}
	mv.err, mv.sending = "", true
	mv.nonce = newNonce()
	if c.demo {
		ip.modal = nil
		return
	}
	payload := map[string]any{
		"type":           interactionModalSubmit,
		"nonce":          mv.nonce,
		"application_id": mv.ev.Application.ID,
		"channel_id":     mv.ev.ChannelID,
		"session_id":     ip.sessionID,
		"data": map[string]any{
			"id":         mv.ev.ID,
			"custom_id":  mv.ev.CustomID,
			"components": comps,
		},
	}
	if mv.guildID.IsValid() {
		payload["guild_id"] = mv.guildID
	}
	nonce := mv.nonce
	go func() {
		if err := c.postInteraction(payload); err != nil {
			slog.Error("failed to submit form", "err", err)
			c.u.post(func() { c.interactionDone(nonce, false) })
		}
	}()
}

// layoutModal draws the form of a bot in the middle of the window.
func (c *chatView) layoutModal(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	ip := &c.inter
	mv := ip.modal
	size := gtx.Constraints.Max
	if mv.cancel.Clicked(gtx) {
		ip.modal = nil
		return layout.Dimensions{Size: size}
	}
	if mv.submit.Clicked(gtx) {
		c.submitModal()
	}
	for _, f := range mv.fields {
		if f.text {
			for {
				if _, ok := f.ed.Update(gtx); !ok {
					break
				}
			}
			// Discord stops typing at the length limit.
			if f.maxLength > 0 && utf8.RuneCountInString(f.ed.Text()) > f.maxLength {
				f.ed.SetText(string([]rune(f.ed.Text())[:f.maxLength]))
				f.ed.SetCaret(f.maxLength, f.maxLength)
			}
			continue
		}
		if f.box.Clicked(gtx) {
			f.open = !f.open
		}
		for i := range f.optClick {
			if f.optClick[i].Clicked(gtx) {
				f.chosen, f.open = i, false
			}
		}
	}
	if !mv.focused {
		for _, f := range mv.fields {
			if f.text {
				gtx.Execute(key.FocusCmd{Tag: &f.ed})
				break
			}
		}
		mv.focused = true
	}

	paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
	w := min(gtx.Dp(440), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(48))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// The bot and the title.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(16), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					app := mv.ev.Application
					if !app.ID.IsValid() {
						return oneLine(bold(label(th, 18, colorHeader, mv.ev.Title))).Layout(gtx)
					}
					icon := ""
					if app.Icon != "" {
						icon = "https://cdn.discordapp.com/app-icons/" + app.ID.String() + "/" + app.Icon + ".png?size=64"
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.u.picture(gtx, icon, 24, colorAccent, app.Name, -1)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, oneLine(bold(label(th, 18, colorHeader, mv.ev.Title))).Layout),
					)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// The fields scroll when they do not fit, leaving room for the
				// title and the buttons.
				gtx.Constraints.Max.Y -= gtx.Dp(140)
				var rows []layout.Widget
				for _, t := range mv.texts {
					rows = append(rows, label(th, 14, colorText, t).Layout)
				}
				for _, f := range mv.fields {
					rows = append(rows, func(gtx layout.Context) layout.Dimensions { return c.layoutModalField(gtx, f) })
				}
				return listLayout(gtx, th, &mv.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, rows[i])
				})
			}),
			// The buttons, on a darker strip as in Discord.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								if mv.err == "" {
									return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
								}
								return label(th, 13, colorRed, mv.err).Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, &mv.cancel, colorSidebar, "Cancel")
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								txt := "Submit"
								if mv.submitText != "" {
									txt = mv.submitText
								}
								if mv.sending {
									txt = "Sending…"
								}
								bg := colorAccent
								if mv.danger {
									bg = colorRed
								}
								return button(gtx, th, &mv.submit, bg, txt)
							}),
						)
					})
				})
			}),
		)
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, mv, dims)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// layoutModalField is a field of a form with its label.
func (c *chatView) layoutModalField(gtx layout.Context, f *modalField) layout.Dimensions {
	th := c.u.th
	title := strings.ToUpper(f.label)
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(bold(label(th, 12, colorMuted, title)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !f.required {
						return layout.Dimensions{}
					}
					return bold(label(th, 12, colorRed, " *")).Layout(gtx)
				}),
			)
		}),
	}
	if f.description != "" {
		children = append(children, layout.Rigid(label(th, 13, colorMuted, f.description).Layout))
	}
	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout))
	if f.text {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				if f.paragraph {
					gtx.Constraints.Min.Y = gtx.Dp(90)
				}
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(200))
					return materialEditor(th, &f.ed, f.placeholder).Layout(gtx)
				})
			})
		}))
		if f.maxLength > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, label(th, 11, colorMuted, fmt.Sprintf("%d/%d", utf8.RuneCountInString(f.ed.Text()), f.maxLength)).Layout)
				})
			}))
		}
	} else {
		sel := selectMenu{customID: f.customID, placeholder: "Make a selection", options: f.options}
		for i := range sel.options {
			sel.options[i].Default = i == f.chosen
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.layoutModalSelect(gtx, f, sel)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutModalSelect is a menu of a form, which keeps its choice until the
// form is sent.
func (c *chatView) layoutModalSelect(gtx layout.Context, f *modalField, sel selectMenu) layout.Dimensions {
	th := c.u.th
	chosen := sel.placeholder
	col := colorMuted
	if f.chosen >= 0 {
		chosen, col = f.options[f.chosen].Label, colorText
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, &f.box, func(gtx layout.Context, hovered bool) layout.Dimensions {
				ic := iconExpand
				if f.open {
					ic = iconCollapse
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(label(th, 14, col, chosen)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 20, colorMuted) }),
				)
			}, true)
		}),
	}
	if f.open {
		for i, o := range f.options {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return rowItem(gtx, &f.optClick[i], func(gtx layout.Context, hovered bool) layout.Dimensions {
					return oneLine(label(th, 14, colorHeader, o.Label)).Layout(gtx)
				}, i == f.chosen)
			}))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
