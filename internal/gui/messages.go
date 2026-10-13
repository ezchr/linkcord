package gui

import (
	"fmt"
	"image"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/skratchdot/open-golang/open"
)

// groupWindow is how close in time messages of the same author must be to be
// shown under one name.
const groupWindow = 7 * time.Minute

func (c *chatView) layoutMessages(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	if c.selChannel == nil {
		return layout.Center.Layout(gtx, label(th, 16, colorMuted, "Choose a conversation or channel").Layout)
	}
	title := "# " + c.selChannel.Name
	if !c.selChannel.GuildID.IsValid() {
		title = "@ " + c.dmName(*c.selChannel)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.chatHeader(gtx, title) }),
		layout.Flexed(1, c.layoutHistory),
		layout.Rigid(c.layoutComposer),
	)
}

// chatHeader is the bar above the messages: the channel or person, the
// topic, and the button that shows or hides the member list.
func (c *chatView) chatHeader(gtx layout.Context, title string) layout.Dimensions {
	th := c.u.th
	height := gtx.Dp(48)
	size := image.Pt(gtx.Constraints.Max.X, height)
	gtx.Constraints = layout.Exact(size)
	paint.FillShape(gtx.Ops, colorServers, clip.Rect{Min: image.Pt(0, height-1), Max: size}.Op())
	ch := c.selChannel
	layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
					if ch.Type != discord.DirectMessage {
						return oneLine(bold(label(th, 16, colorHeader, title))).Layout(gtx)
					}
					// Clicking the person opens their profile.
					return c.titleClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return oneLine(bold(label(th, 16, colorHeader, title))).Layout(gtx)
					})
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if ch.Topic == "" {
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
				}
				return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = gtx.Constraints.Max
					// A short line, centered, separates the title from the topic.
					h := gtx.Dp(24)
					y := (gtx.Constraints.Max.Y - h) / 2
					paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(gtx.Dp(1), y+h)}.Op())
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return vcenter(gtx, oneLine(label(th, 14, colorMuted, strings.ReplaceAll(ch.Topic, "\n", " "))).Layout)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if ch.Type != discord.GroupDM && !ch.GuildID.IsValid() {
					return layout.Dimensions{}
				}
				col := colorMuted
				if c.showMembers {
					col = colorHeader
				}
				return iconButton(gtx, &c.membersToggle, iconMembers, col, colorHeader)
			}),
		)
	})
	return layout.Dimensions{Size: size}
}

func (c *chatView) layoutHistory(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	switch {
	case c.loading:
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			s := gtx.Dp(32)
			gtx.Constraints = layout.Exact(image.Pt(s, s))
			return loader(th).Layout(gtx)
		})
	case c.loadErr != "":
		return layout.Center.Layout(gtx, label(th, 15, colorRed, c.loadErr).Layout)
	case len(c.messages) == 0:
		return layout.Center.Layout(gtx, label(th, 15, colorMuted, "No messages here yet. Say hello!").Layout)
	}
	// The first element is the top of the history: a spinner while older
	// messages load, or the start of the conversation.
	dims := listLayout(gtx, th, &c.messageList, len(c.messages)+1, func(gtx layout.Context, i int) layout.Dimensions {
		if i == 0 {
			return c.layoutHistoryTop(gtx)
		}
		if i == len(c.messages) {
			// Room between the newest message and the box to write.
			return layout.Inset{Bottom: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutMessage(gtx, i-1)
			})
		}
		return c.layoutMessage(gtx, i-1)
	})
	if pos := c.messageList.Position; pos.First == 0 {
		c.loadOlder()
	}
	return dims
}

func (c *chatView) layoutHistoryTop(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !c.noOlder {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := gtx.Dp(24)
				gtx.Constraints = layout.Exact(image.Pt(s, s))
				return loader(th).Layout(gtx)
			})
		}
		txt := "This is the beginning of #" + c.selChannel.Name + "."
		if !c.selChannel.GuildID.IsValid() {
			txt = "This is the beginning of your conversation with " + c.dmName(*c.selChannel) + "."
		}
		return label(th, 14, colorMuted, txt).Layout(gtx)
	})
}

// grouped reports whether message i continues the previous one, so it is
// shown without the author's name and avatar.
func (c *chatView) grouped(i int) bool {
	if i == 0 {
		return false
	}
	m, prev := c.messages[i], c.messages[i-1]
	return m.Author.ID == prev.Author.ID &&
		isRegular(m) && isRegular(prev) && m.ReferencedMessage == nil &&
		m.Timestamp.Time().Sub(prev.Timestamp.Time()) < groupWindow
}

func isRegular(m discord.Message) bool {
	return m.Type == discord.DefaultMessage || m.Type == discord.InlinedReplyMessage
}

func (c *chatView) layoutMessage(gtx layout.Context, i int) layout.Dimensions {
	th := c.u.th
	m := c.messages[i]
	if !isRegular(m) {
		return layout.Inset{Left: unit.Dp(72), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx,
			label(th, 14, colorMuted, c.systemText(m)).Layout)
	}
	grouped := c.grouped(i)
	top := unit.Dp(16)
	if grouped {
		top = 2
	}
	guildID := c.selChannel.GuildID
	name := c.memberName(guildID, m.Author.ID, nil)
	nameColor := colorHeader
	if guildID.IsValid() {
		if col, ok := c.st.MemberColor(guildID, m.Author.ID); ok && col != 0 && col != discord.NullColor {
			nameColor = rgb(uint32(col))
		}
	}

	// The gap above stays outside, so that the highlight under the pointer
	// covers only the message.
	return layout.Inset{Top: top - 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.layoutWithActions(gtx, m, func(gtx layout.Context) layout.Dimensions {
			return c.layoutMessageBody(gtx, m, grouped, name, nameColor)
		})
	})
}

func (c *chatView) layoutMessageBody(gtx layout.Context, m discord.Message, grouped bool, name string, nameColor colorT) layout.Dimensions {
	th := c.u.th
	guildID := c.selChannel.GuildID
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: 2, Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if grouped {
					return layout.Spacer{Width: unit.Dp(40)}.Layout(gtx)
				}
				pic := func(gtx layout.Context) layout.Dimensions {
					return c.msgClick(m.ID, 0).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return c.u.picture(gtx, c.authorAvatarURL(guildID, m.Author), 40, colorFor(uint64(m.Author.ID)), name, -1)
					})
				}
				// Replies show the replied message above the avatar's row.
				if m.ReferencedMessage != nil {
					return layout.Inset{Top: unit.Dp(22)}.Layout(gtx, pic)
				}
				return pic(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				var children []layout.FlexChild
				if ref := m.ReferencedMessage; ref != nil && !grouped {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutReply(gtx, *ref)
					}))
				}
				if !grouped {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								cl := c.msgClick(m.ID, 1)
								return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									pointer.CursorPointer.Add(gtx.Ops)
									dims := oneLine(bold(label(th, 15, nameColor, name))).Layout(gtx)
									if cl.Hovered() {
										// Underline the name like a link.
										paint.FillShape(gtx.Ops, nameColor, clip.Rect{Min: image.Pt(0, dims.Size.Y-gtx.Dp(2)), Max: image.Pt(dims.Size.X, dims.Size.Y-gtx.Dp(1))}.Op())
									}
									return dims
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if !m.Author.Bot {
									return layout.Dimensions{}
								}
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, botTag(th))
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(label(th, 12, colorMuted, formatTime(m.Timestamp.Time())).Layout),
						)
					}))
				}
				if c.act.editing == m.ID {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutEditor(gtx, m)
					}))
				} else if m.Content != "" {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if m.EditedTimestamp.IsValid() {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutContent(gtx, m) }),
								layout.Rigid(label(th, 11, colorMuted, "(edited)").Layout),
							)
						}
						return c.layoutContent(gtx, m)
					}))
				}
				for _, a := range m.Attachments {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutAttachment(gtx, a)
					}))
				}
				for _, s := range m.Stickers {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutSticker(gtx, s)
					}))
				}
				for _, e := range m.Embeds {
					if p, ok := embedPreview(e); ok {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if p.video {
								return c.layoutVideo(gtx, p)
							}
							return c.layoutMedia(gtx, p.proxy, p.original, p.width, p.height, p.openURL)
						}))
						continue
					}
					if e.Title == "" && e.Description == "" && e.Image == nil {
						continue
					}
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutEmbed(gtx, e)
					}))
				}
				if len(m.Reactions) > 0 {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutReactions(gtx, m)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			}),
		)
	})
}

// authorAvatarURL is the avatar of a message author: the one they use in the
// server, if any, or their own.
func (c *chatView) authorAvatarURL(guildID discord.GuildID, u discord.User) string {
	if guildID.IsValid() {
		if m, err := c.st.Cabinet.Member(guildID, u.ID); err == nil && m.Avatar != "" {
			return cdnSize(m.AvatarURLWithType(discord.PNGImage, guildID))
		}
	}
	return cdnSize(u.AvatarURLWithType(discord.PNGImage))
}

func (c *chatView) selectable(id discord.MessageID) *widget.Selectable {
	s, ok := c.selectables[id]
	if !ok {
		s = new(widget.Selectable)
		c.selectables[id] = s
	}
	return s
}

func (c *chatView) layoutReply(gtx layout.Context, ref discord.Message) layout.Dimensions {
	th := c.u.th
	name := c.memberName(c.selChannel.GuildID, ref.Author.ID, nil)
	snippet := strings.ReplaceAll(c.renderContent(ref), "\n", " ")
	if snippet == "" && len(ref.Attachments) > 0 {
		snippet = "Attachment"
	}
	return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconReply, 14, colorMuted) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(oneLine(bold(label(th, 13, colorMuted, "@"+name))).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Flexed(1, oneLine(label(th, 13, colorMuted, snippet)).Layout),
		)
	})
}

func (c *chatView) linkClick(url string) *widget.Clickable {
	cl, ok := c.linkClicks[url]
	if !ok {
		cl = new(widget.Clickable)
		c.linkClicks[url] = cl
	}
	return cl
}

// openLink opens url in the default browser.
func openLink(url string) {
	go open.Run(url)
}

// isImage reports whether an attachment is a picture that can be shown.
func isImage(a discord.Attachment) bool {
	if strings.HasPrefix(a.ContentType, "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(a.Filename)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// mediaURL asks the media proxy of Discord for a picture resized to w×h
// pixels, which downloads much less than the original.
func mediaURL(proxy, original string, w, h int) string {
	u := proxy
	if parsed, err := url.Parse(original); err == nil && (strings.EqualFold(path.Ext(parsed.Path), ".gif") || parsed.Fragment == gifVideoFragment) {
		// Static proxy renditions must not replace the animated original.
		u = original
	}
	if u == "" {
		u = original
	}
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || !discordMediaHost(parsed.Hostname()) {
		return u
	}
	q := parsed.Query()
	if strings.EqualFold(path.Ext(parsed.Path), ".gif") {
		q.Del("format")
		q.Set("animated", "true")
	}
	q.Set("width", strconv.Itoa(max(w, 1)))
	q.Set("height", strconv.Itoa(max(h, 1)))
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// layoutMedia shows a picture of w×h pixels at most 400×300, which opens
// openURL in the browser when clicked.
func (c *chatView) layoutMedia(gtx layout.Context, proxy, original string, w, h int, openURL string) layout.Dimensions {
	maxW, maxH := min(gtx.Constraints.Max.X, gtx.Dp(400)), gtx.Dp(300)
	if w <= 0 || h <= 0 {
		w, h = 400, 300
	}
	dw, dh := gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h))
	if dw > maxW {
		dw, dh = maxW, dh*maxW/dw
	}
	if dh > maxH {
		dw, dh = dw*maxH/dh, maxH
	}
	dw, dh = max(dw, 1), max(dh, 1)
	size := image.Pt(dw, dh)
	source := mediaURL(proxy, original, dw, dh)
	if c.u.images.failed(source, max(dw, dh)) {
		return c.layoutMediaFallback(gtx, "Image", "Could not load · click to open", openURL)
	}
	cl := c.linkClick("media:" + openURL)
	if cl.Clicked(gtx) {
		c.openViewer(proxy, original, w, h, openURL, mediaURL(proxy, original, dw, dh), max(dw, dh))
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			r := image.Rectangle{Max: size}
			defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
			img := c.u.images.frame(gtx, mediaURL(proxy, original, dw, dh), max(dw, dh))
			if img == nil {
				paint.FillShape(gtx.Ops, colorSidebar, clip.Rect(r).Op())
				return layout.Dimensions{Size: size}
			}
			gtx.Constraints = layout.Exact(size)
			return widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
		})
	})
}

func (c *chatView) layoutAttachment(gtx layout.Context, a discord.Attachment) layout.Dimensions {
	if isImage(a) {
		return c.layoutMedia(gtx, string(a.Proxy), string(a.URL), int(a.Width), int(a.Height), string(a.URL))
	}
	if isVideo(a) {
		return c.layoutVideo(gtx, mediaPreview{
			proxy: videoPosterURL(string(a.Proxy)), openURL: string(a.URL), playURL: string(a.URL),
			width: int(a.Width), height: int(a.Height), video: true,
			title: a.Filename + " · " + formatSize(a.Size),
		})
	}
	th := c.u.th
	cl := c.linkClick(string(a.URL))
	if cl.Clicked(gtx) {
		openLink(string(a.URL))
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorSidebar
			if cl.Hovered() {
				bg = colorSelected
			}
			return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconAttach, 20, colorMuted) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(oneLine(label(th, 14, colorLink, a.Filename)).Layout),
								layout.Rigid(label(th, 12, colorMuted, formatSize(a.Size)+" · click to open").Layout),
							)
						}),
					)
				})
			})
		})
	})
}

func (c *chatView) layoutEmbed(gtx layout.Context, e discord.Embed) layout.Dimensions {
	th := c.u.th
	bar := colorSelected
	if e.Color != 0 && e.Color != discord.NullColor {
		bar = rgb(uint32(e.Color))
	}
	cl := c.linkClick("embed:" + string(e.URL) + e.Title)
	if e.URL != "" && cl.Clicked(gtx) {
		openLink(string(e.URL))
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
		return rounded(gtx, colorSidebar, 4, func(gtx layout.Context) layout.Dimensions {
			dims := layout.Inset{Left: unit.Dp(14), Right: unit.Dp(12), Top: unit.Dp(10), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				var children []layout.FlexChild
				if e.Title != "" {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						col := colorHeader
						if e.URL != "" {
							col = colorLink
						}
						return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if e.URL != "" {
								pointer.CursorPointer.Add(gtx.Ops)
							}
							return bold(label(th, 15, col, e.Title)).Layout(gtx)
						})
					}))
				}
				if e.Description != "" {
					desc := e.Description
					if utf8.RuneCountInString(desc) > 400 {
						desc = string([]rune(desc)[:400]) + "…"
					}
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, label(th, 14, colorText, desc).Layout)
					}))
				}
				if e.Image != nil {
					img := e.Image
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.layoutMedia(gtx, string(img.Proxy), string(img.URL), int(img.Width), int(img.Height), string(img.URL))
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
			paint.FillShape(gtx.Ops, bar, clip.UniformRRect(image.Rect(0, 0, gtx.Dp(4), dims.Size.Y), gtx.Dp(2)).Op(gtx.Ops))
			return dims
		})
	})
}

var (
	reUserMention    = regexp.MustCompile(`<@!?(\d+)>`)
	reRoleMention    = regexp.MustCompile(`<@&(\d+)>`)
	reChannelMention = regexp.MustCompile(`<#(\d+)>`)
	reCustomEmoji    = regexp.MustCompile(`<a?:(\w+):\d+>`)
	reTimestamp      = regexp.MustCompile(`<t:(-?\d+)(?::[tTdDfFR])?>`)
)

// renderContent turns the markup of Discord in the content of m into text.
func (c *chatView) renderContent(m discord.Message) string {
	s := m.Content
	if s == "" {
		return ""
	}
	guildID := m.GuildID
	if !guildID.IsValid() && c.selChannel != nil {
		guildID = c.selChannel.GuildID
	}
	s = reUserMention.ReplaceAllStringFunc(s, func(match string) string {
		id, _ := discord.ParseSnowflake(reUserMention.FindStringSubmatch(match)[1])
		for _, u := range m.Mentions {
			if discord.Snowflake(u.ID) == id {
				return "@" + c.memberName(guildID, u.ID, u.Member)
			}
		}
		return "@" + c.memberName(guildID, discord.UserID(id), nil)
	})
	s = reRoleMention.ReplaceAllStringFunc(s, func(match string) string {
		id, _ := discord.ParseSnowflake(reRoleMention.FindStringSubmatch(match)[1])
		if r, err := c.st.Cabinet.Role(guildID, discord.RoleID(id)); err == nil {
			return "@" + r.Name
		}
		return "@role"
	})
	s = reChannelMention.ReplaceAllStringFunc(s, func(match string) string {
		id, _ := discord.ParseSnowflake(reChannelMention.FindStringSubmatch(match)[1])
		if ch, err := c.st.Cabinet.Channel(discord.ChannelID(id)); err == nil {
			return "#" + ch.Name
		}
		return "#channel"
	})
	s = reCustomEmoji.ReplaceAllString(s, ":$1:")
	s = reTimestamp.ReplaceAllStringFunc(s, func(match string) string {
		sec, err := strconv.ParseInt(reTimestamp.FindStringSubmatch(match)[1], 10, 64)
		if err != nil {
			return match
		}
		return time.Unix(sec, 0).Local().Format("02/01/2006 15:04")
	})
	return s
}

// systemText describes messages that Discord sends on its own.
func (c *chatView) systemText(m discord.Message) string {
	name := c.memberName(c.selChannel.GuildID, m.Author.ID, nil)
	switch m.Type {
	case discord.GuildMemberJoinMessage:
		return "→ " + name + " joined the server."
	case discord.ChannelPinnedMessage:
		return "📌 " + name + " pinned a message in this channel."
	case discord.CallMessage:
		return "📞 " + name + " started a call."
	case discord.RecipientAddMessage:
		return "→ " + name + " added someone to the group."
	case discord.RecipientRemoveMessage:
		return "← " + name + " left the group."
	case discord.ChannelNameChangeMessage:
		return name + " changed the channel name: " + m.Content
	}
	if m.Content != "" {
		return name + ": " + c.renderContent(m)
	}
	return name + " performed an action Linkcord cannot display yet."
}

// formatTime shows when a message was sent, as Discord does in Spanish.
func formatTime(t time.Time) string {
	t = t.Local()
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today at " + t.Format("15:04")
	case y1 == y2 && m1 == m2 && d1 == d2-1:
		return "yesterday at " + t.Format("15:04")
	}
	return t.Format("02/01/2006 15:04")
}

func formatSize(n uint64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// botTag is the chip that marks apps and bots next to their name.
func botTag(th *material.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, colorAccent, 3, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx,
				bold(label(th, 10, colorHeader, "BOT")).Layout)
		})
	}
}

// offset moves what is drawn next by (x, y) until popped.
func offset(gtx layout.Context, x, y int) op.TransformStack {
	return op.Offset(image.Pt(x, y)).Push(gtx.Ops)
}

// listLayout lays out a list with a thin scrollbar that only shows over it.
func listLayout(gtx layout.Context, th *material.Theme, l *widget.List, n int, w layout.ListElement) layout.Dimensions {
	ls := material.List(th, l)
	ls.AnchorStrategy = material.Overlay
	ls.Indicator.Color = withAlpha(colorServers, 0xc0)
	ls.Indicator.HoverColor = colorServers
	ls.Indicator.MinorWidth = unit.Dp(6)
	return ls.Layout(gtx, n, w)
}
