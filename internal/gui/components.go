package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log/slog"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconCollapse = mustIcon(icons.NavigationExpandLess)

// Interaction types that a user sends.
const (
	interactionComponent   = 3
	interactionModalSubmit = 5
)

// interactionTimeout is how long an interaction waits for Discord to say it
// went through before it shows as failed.
const interactionTimeout = 15 * time.Second

// pendingInteraction is an interaction sent and not answered yet.
type pendingInteraction struct {
	msg discord.MessageID
	// key is the component that sent it, which shows a spinner meanwhile.
	key string
}

// interactions is the state of the buttons, menus and forms of bots.
type interactions struct {
	// sessionID is the gateway session, which Discord wants with each
	// interaction.
	sessionID string
	// pending maps the nonce of each interaction on its way to its source.
	pending map[string]pendingInteraction
	// failed holds the messages whose last interaction failed.
	failed map[discord.MessageID]bool
	clicks map[string]*widget.Clickable
	// open is the key of the menu showing its options, if any.
	open  string
	modal *modalView
}

func (ip *interactions) init() {
	ip.pending = make(map[string]pendingInteraction)
	ip.failed = make(map[discord.MessageID]bool)
	ip.clicks = make(map[string]*widget.Clickable)
}

// busy reports whether the component with key waits for an answer.
func (ip *interactions) busy(key string) bool {
	for _, p := range ip.pending {
		if p.key == key {
			return true
		}
	}
	return false
}

func newNonce() string {
	return strconv.FormatUint(uint64(discord.NewSnowflake(time.Now())), 10)
}

// appOf returns the application a bot message comes from, which interactions
// go to. Most bots are their own application.
func appOf(m discord.Message) discord.AppID {
	if m.ApplicationID.IsValid() {
		return m.ApplicationID
	}
	return discord.AppID(m.Author.ID)
}

// postInteraction sends an interaction as the web client does: a form with
// its JSON in payload_json.
func (c *chatView) postInteraction(payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("payload_json", string(data)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.st.Client.FastRequest("POST", api.Endpoint+"interactions",
		httputil.WithContentType(w.FormDataContentType()),
		httputil.WithBody(io.NopCloser(&buf)))
}

// interact sends what the user did with a component of message m, from the
// component with key, and marks it failed if Discord does not confirm it.
func (c *chatView) interact(m discord.Message, key string, data map[string]any) {
	ip := &c.inter
	nonce := newNonce()
	ip.pending[nonce] = pendingInteraction{msg: m.ID, key: key}
	delete(ip.failed, m.ID)
	if c.demo {
		// The demo has no bot to answer: it shows the spinner a moment, and
		// one button answers with a form.
		time.AfterFunc(700*time.Millisecond, func() {
			c.u.post(func() {
				delete(ip.pending, nonce)
				if data["custom_id"] == discord.ComponentID(demoFormID) {
					c.openModal(demoForm(m))
				}
			})
		})
		return
	}
	payload := map[string]any{
		"type":           interactionComponent,
		"nonce":          nonce,
		"channel_id":     m.ChannelID,
		"message_flags":  m.Flags,
		"message_id":     m.ID,
		"application_id": appOf(m),
		"session_id":     ip.sessionID,
		"data":           data,
	}
	if ch := c.selChannel; ch != nil && ch.GuildID.IsValid() {
		payload["guild_id"] = ch.GuildID
	}
	go func() {
		if err := c.postInteraction(payload); err != nil {
			slog.Error("failed to send interaction", "err", err)
			c.u.post(func() { c.interactionDone(nonce, false) })
		}
	}()
	time.AfterFunc(interactionTimeout, func() {
		c.u.post(func() {
			if _, ok := ip.pending[nonce]; ok {
				c.interactionDone(nonce, false)
			}
		})
	})
}

// interactionDone ends the interaction with nonce, which went through or
// failed.
func (c *chatView) interactionDone(nonce string, ok bool) {
	ip := &c.inter
	if m := ip.modal; m != nil && m.nonce == nonce {
		m.sending = false
		if ok {
			ip.modal = nil
		} else {
			m.err = "This interaction failed. Try again."
		}
		return
	}
	p, found := ip.pending[nonce]
	if !found {
		return
	}
	delete(ip.pending, nonce)
	if !ok {
		ip.failed[p.msg] = true
	}
}

func (ip *interactions) click(key string) *widget.Clickable {
	return clickOf(ip.clicks, key)
}

// buttonStyle returns the style number of a button and its URL if it is a
// link, which arikawa keeps unexported but writes in its JSON.
func buttonStyle(b *discord.ButtonComponent) (style int, url string) {
	data, err := json.Marshal(b)
	if err != nil {
		return 2, ""
	}
	var v struct {
		Style int    `json:"style"`
		URL   string `json:"url"`
	}
	json.Unmarshal(data, &v)
	return v.Style, v.URL
}

const (
	buttonPrimary   = 1
	buttonSecondary = 2
	buttonSuccess   = 3
	buttonDanger    = 4
	buttonLink      = 5
)

// updateComponents handles the clicks on the components of the messages
// shown. Components are laid out inside messages, so their clicks are read
// there, after this frame's layout; this runs before the next one.
func (c *chatView) updateComponents(gtx layout.Context) {
	for _, m := range c.messages {
		if len(m.Components) == 0 {
			continue
		}
		for i, comp := range m.Components {
			c.updateComponent(gtx, m, fmt.Sprintf("%d/%d", m.ID, i), comp)
		}
	}
}

func (c *chatView) updateComponent(gtx layout.Context, m discord.Message, key string, comp discord.Component) {
	ip := &c.inter
	switch v := comp.(type) {
	case *discord.ActionRowComponent:
		for i, child := range *v {
			c.updateComponent(gtx, m, fmt.Sprintf("%s/%d", key, i), child)
		}
	case *discord.ContainerComponent:
		for i, child := range v.Components {
			c.updateComponent(gtx, m, fmt.Sprintf("%s/%d", key, i), child)
		}
	case *discord.SectionComponent:
		if v.Accessory != nil {
			c.updateComponent(gtx, m, key+"/a", v.Accessory)
		}
	case *discord.ButtonComponent:
		if !ip.click(key).Clicked(gtx) || v.Disabled || ip.busy(key) {
			return
		}
		style, url := buttonStyle(v)
		if style == buttonLink {
			openLink(url)
			return
		}
		c.interact(m, key, map[string]any{"component_type": int(discord.ButtonComponentType), "custom_id": v.CustomID})
	default:
		sel, ok := selectOf(c, comp)
		if !ok {
			return
		}
		if ip.click(key).Clicked(gtx) && !sel.disabled {
			if ip.open == key {
				ip.open = ""
			} else {
				ip.open = key
			}
		}
		for i, opt := range sel.options {
			if !ip.click(fmt.Sprintf("%s/o%d", key, i)).Clicked(gtx) {
				continue
			}
			ip.open = ""
			t := int(comp.Type())
			c.interact(m, key, map[string]any{"component_type": t, "type": t, "custom_id": sel.customID, "values": []string{opt.Value}})
		}
	}
}

// selectMenu is what the menus of every kind have in common.
type selectMenu struct {
	customID    discord.ComponentID
	placeholder string
	disabled    bool
	options     []discord.SelectOption
}

// selectOf returns the menu of comp if it is one. The menus of users, roles
// and channels list those that the client knows.
func selectOf(c *chatView, comp discord.Component) (selectMenu, bool) {
	guildID := discord.NullGuildID
	if c.selChannel != nil {
		guildID = c.selChannel.GuildID
	}
	users := func() []discord.SelectOption {
		var out []discord.SelectOption
		if guildID.IsValid() {
			members, _ := c.st.Cabinet.Members(guildID)
			for _, mb := range members {
				out = append(out, discord.SelectOption{Label: c.memberName(guildID, mb.User.ID, &mb), Value: mb.User.ID.String(), Description: mb.User.Username})
			}
		}
		return out
	}
	roles := func() []discord.SelectOption {
		var out []discord.SelectOption
		rs, _ := c.st.Cabinet.Roles(guildID)
		for _, r := range rs {
			if discord.Snowflake(r.ID) != discord.Snowflake(guildID) {
				out = append(out, discord.SelectOption{Label: "@" + r.Name, Value: r.ID.String()})
			}
		}
		return out
	}
	switch v := comp.(type) {
	case *discord.StringSelectComponent:
		return selectMenu{v.CustomID, v.Placeholder, v.Disabled, v.Options}, true
	case *discord.UserSelectComponent:
		return selectMenu{v.CustomID, v.Placeholder, v.Disabled, users()}, true
	case *discord.RoleSelectComponent:
		return selectMenu{v.CustomID, v.Placeholder, v.Disabled, roles()}, true
	case *discord.MentionableSelectComponent:
		return selectMenu{v.CustomID, v.Placeholder, v.Disabled, append(users(), roles()...)}, true
	case *discord.ChannelSelectComponent:
		var out []discord.SelectOption
		chs, _ := c.st.Cabinet.Channels(guildID)
		for _, ch := range chs {
			if ch.Type != discord.GuildCategory && c.st.HasPermissions(ch.ID, discord.PermissionViewChannel) {
				out = append(out, discord.SelectOption{Label: "#" + ch.Name, Value: ch.ID.String()})
			}
		}
		slices.SortFunc(out, func(a, b discord.SelectOption) int { return strings.Compare(a.Label, b.Label) })
		return selectMenu{v.CustomID, v.Placeholder, v.Disabled, out}, true
	}
	return selectMenu{}, false
}

// layoutComponents lays out the buttons, menus and blocks of a bot message,
// with the failure of its last interaction if it failed.
func (c *chatView) layoutComponents(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	var children []layout.FlexChild
	for i, comp := range m.Components {
		key := fmt.Sprintf("%d/%d", m.ID, i)
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutComponent(gtx, m, key, comp)
			})
		}))
	}
	if c.inter.failed[m.ID] {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, label(th, 13, colorRed, "This interaction failed").Layout)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (c *chatView) layoutComponent(gtx layout.Context, m discord.Message, key string, comp discord.Component) layout.Dimensions {
	switch v := comp.(type) {
	case *discord.ActionRowComponent:
		var items []layout.FlexChild
		for i, child := range *v {
			ck := fmt.Sprintf("%s/%d", key, i)
			items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.layoutComponent(gtx, m, ck, child)
				})
			}))
		}
		return wrapLayout(gtx, items)
	case *discord.ButtonComponent:
		return c.layoutButton(gtx, key, v)
	case *discord.TextDisplayComponent:
		return c.layoutText(gtx, m, key, v.Content, 15)
	case *discord.SectionComponent:
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				var texts []layout.FlexChild
				for i, child := range v.Components {
					ck := fmt.Sprintf("%s/%d", key, i)
					texts = append(texts, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutComponent(gtx, m, ck, child)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, texts...)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if v.Accessory == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.layoutComponent(gtx, m, key+"/a", v.Accessory)
				})
			}),
		)
	case *discord.ThumbnailComponent:
		px := gtx.Dp(85)
		return c.layoutMediaItem(gtx, v.Media, px, px)
	case *discord.MediaGalleryComponent:
		return c.layoutGallery(gtx, v)
	case *discord.FileComponent:
		return c.layoutFileComponent(gtx, m, v)
	case *discord.SeparatorComponent:
		space := unit.Dp(4)
		if v.Spacing == discord.SeparatorComponentSpacingLargePadding {
			space = 12
		}
		return layout.Inset{Top: space, Bottom: space}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
			if v.Divider {
				paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: size}.Op())
			}
			return layout.Dimensions{Size: size}
		})
	case *discord.ContainerComponent:
		return c.layoutContainer(gtx, m, key, v)
	}
	if sel, ok := selectOf(c, comp); ok {
		return c.layoutSelect(gtx, key, sel)
	}
	return layout.Dimensions{}
}

// layoutButton is a button of a bot, in the colors of Discord.
func (c *chatView) layoutButton(gtx layout.Context, key string, b *discord.ButtonComponent) layout.Dimensions {
	th := c.u.th
	style, _ := buttonStyle(b)
	bg := map[int]colorT{
		buttonPrimary: colorAccent, buttonSecondary: colorSecondaryButton,
		buttonSuccess: colorGreenButton, buttonDanger: colorRed, buttonLink: colorSecondaryButton,
	}[style]
	if bg == (colorT{}) {
		bg = colorSecondaryButton
	}
	cl := c.inter.click(key)
	busy := c.inter.busy(key)
	if b.Disabled {
		bg = withAlpha(bg, 0x80)
	} else if cl.Hovered() {
		bg = lighten(bg)
	}
	fg := colorHeader
	if b.Disabled {
		fg = withAlpha(fg, 0x80)
	}
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !b.Disabled {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Dp(32)
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
					if busy {
						s := gtx.Dp(16)
						gtx.Constraints = layout.Exact(image.Pt(max(s, gtx.Dp(24)), s))
						l := loader(th)
						l.Color = colorHeader
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints = layout.Exact(image.Pt(s, s))
							return l.Layout(gtx)
						})
					}
					var parts []layout.FlexChild
					if b.Emoji != nil {
						parts = append(parts, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutComponentEmoji(gtx, *b.Emoji, 18)
						}))
						if b.Label != "" {
							parts = append(parts, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
						}
					}
					if b.Label != "" {
						parts = append(parts, layout.Rigid(oneLine(label(th, 14, fg, b.Label)).Layout))
					}
					if style == buttonLink {
						parts = append(parts,
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconOpen, 14, fg) }),
						)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, parts...)
				})
			})
		})
	})
}

// layoutComponentEmoji is the emoji of a button or option: a picture for
// those of servers, a character otherwise.
func (c *chatView) layoutComponentEmoji(gtx layout.Context, e discord.ComponentEmoji, size unit.Dp) layout.Dimensions {
	if e.ID.IsValid() {
		return c.u.picture(gtx, emojiURL(e.ID.String(), e.Animated, gtx.Dp(size)), size, colorT{}, "", 0)
	}
	l := label(c.u.th, unit.Sp(float32(size)*0.85), colorHeader, e.Name)
	l.MaxLines = 1
	return l.Layout(gtx)
}

// lighten returns c a little lighter, for buttons under the pointer.
func lighten(c colorT) colorT {
	up := func(v uint8) uint8 { return uint8(min(int(v)+18, 255)) }
	return colorT{R: up(c.R), G: up(c.G), B: up(c.B), A: c.A}
}

// layoutSelect is a menu of a bot: a box with the choice or placeholder that
// shows its options below when clicked.
func (c *chatView) layoutSelect(gtx layout.Context, key string, sel selectMenu) layout.Dimensions {
	th := c.u.th
	ip := &c.inter
	open := ip.open == key
	busy := ip.busy(key)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	chosen := ""
	for _, o := range sel.options {
		if o.Default {
			chosen = o.Label
		}
	}
	box := func(gtx layout.Context) layout.Dimensions {
		cl := ip.click(key)
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if !sel.disabled {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			bg := colorServers
			if cl.Hovered() && !sel.disabled {
				bg = colorPanel
			}
			return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = gtx.Dp(40)
				return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
						txt, col := sel.placeholder, colorMuted
						if txt == "" {
							txt = "Make a selection"
						}
						if chosen != "" {
							txt, col = chosen, colorText
						}
						if sel.disabled {
							col = withAlpha(col, 0x80)
						}
						ic := iconExpand
						if open {
							ic = iconCollapse
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, oneLine(label(th, 14, col, txt)).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if busy {
									s := gtx.Dp(16)
									gtx.Constraints = layout.Exact(image.Pt(s, s))
									return loader(th).Layout(gtx)
								}
								return icon(gtx, ic, 20, colorMuted)
							}),
						)
					})
				})
			})
		})
	}
	if !open {
		return box(gtx)
	}
	var rows []layout.FlexChild
	for i, o := range sel.options {
		ok := fmt.Sprintf("%s/o%d", key, i)
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, ip.click(ok), func(gtx layout.Context, hovered bool) layout.Dimensions {
				var parts []layout.FlexChild
				if o.Emoji != nil {
					parts = append(parts,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutComponentEmoji(gtx, *o.Emoji, 20) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					)
				}
				parts = append(parts, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lines := []layout.FlexChild{layout.Rigid(oneLine(label(th, 14, colorHeader, o.Label)).Layout)}
					if o.Description != "" {
						lines = append(lines, layout.Rigid(oneLine(label(th, 12, colorMuted, o.Description)).Layout))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
				}))
				if o.Default {
					parts = append(parts, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconCheck, 18, colorAccent) }))
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, parts...)
			}, false)
		}))
	}
	if len(rows) == 0 {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, label(th, 13, colorMuted, "No options").Layout)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(box),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorPanel, 4, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					// Long menus show their first options; Discord has 25 at most.
					return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows[:min(len(rows), 25)]...)
					})
				})
			})
		}),
	)
}

// layoutContainer is a box of a bot message, with its accent color on the
// left as embeds have.
func (c *chatView) layoutContainer(gtx layout.Context, m discord.Message, key string, v *discord.ContainerComponent) layout.Dimensions {
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
	var children []layout.FlexChild
	for i, child := range v.Components {
		ck := fmt.Sprintf("%s/%d", key, i)
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutComponent(gtx, m, ck, child)
			})
		}))
	}
	return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(14), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
		if v.AccentColor != 0 && v.AccentColor != discord.NullColor {
			paint.FillShape(gtx.Ops, rgb(uint32(v.AccentColor)), clip.UniformRRect(image.Rect(0, 0, gtx.Dp(4), dims.Size.Y), gtx.Dp(2)).Op(gtx.Ops))
		}
		return dims
	})
}

// layoutMediaItem shows a picture of a component in w×h pixels at most.
func (c *chatView) layoutMediaItem(gtx layout.Context, it discord.UnfurledMediaitem, w, h int) layout.Dimensions {
	url := it.URL
	if strings.HasPrefix(url, "attachment://") {
		return layout.Dimensions{}
	}
	iw, ih := it.Width, it.Height
	if iw <= 0 || ih <= 0 {
		iw, ih = w, h
	}
	// Fit the picture in w×h, keeping its shape.
	scale := min(float32(w)/float32(iw), float32(h)/float32(ih))
	dw, dh := max(int(float32(iw)*scale), 1), max(int(float32(ih)*scale), 1)
	size := image.Pt(dw, dh)
	cl := c.linkClick("media:" + url)
	if cl.Clicked(gtx) {
		c.openViewer(it.ProxyURL, url, iw, ih, url, mediaURL(it.ProxyURL, url, dw, dh), max(dw, dh))
	}
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		r := image.Rectangle{Max: size}
		defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
		img := c.u.images.get(mediaURL(it.ProxyURL, url, dw, dh), max(dw, dh))
		if img == nil {
			paint.FillShape(gtx.Ops, colorPanel, clip.Rect(r).Op())
			return layout.Dimensions{Size: size}
		}
		gtx.Constraints = layout.Exact(size)
		return widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
	})
}

// layoutGallery shows the pictures of a gallery: one large, or several in
// a grid.
func (c *chatView) layoutGallery(gtx layout.Context, g *discord.MediaGalleryComponent) layout.Dimensions {
	maxW := min(gtx.Constraints.Max.X, gtx.Dp(520))
	if len(g.Items) == 1 {
		return c.layoutMediaItem(gtx, g.Items[0].Media, maxW, gtx.Dp(350))
	}
	side := (maxW - gtx.Dp(4)) / 2
	if len(g.Items) > 4 {
		side = (maxW - gtx.Dp(8)) / 3
	}
	var cells []layout.FlexChild
	for _, it := range g.Items {
		cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutMediaItem(gtx, it.Media, side, side)
			})
		}))
	}
	gtx.Constraints.Max.X = maxW + gtx.Dp(8)
	return wrapLayout(gtx, cells)
}

// layoutFileComponent shows a file of a component as an attachment.
func (c *chatView) layoutFileComponent(gtx layout.Context, m discord.Message, f *discord.FileComponent) layout.Dimensions {
	name := strings.TrimPrefix(f.File.URL, "attachment://")
	for _, a := range m.Attachments {
		if a.Filename == name {
			return c.layoutAttachment(gtx, a)
		}
	}
	return label(c.u.th, 14, colorMuted, f.Name).Layout(gtx)
}
