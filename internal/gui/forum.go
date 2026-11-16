package gui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/ningen/v3"
)

// Forums: channels that hold only posts, which are threads. The channel
// list shows a forum with the posts the user joined under it, as it does a
// text channel and its threads; opening the forum lists its posts in place
// of messages: the active ones the gateway state has, then the older ones
// Discord is asked for.

// guildMedia is a media channel, a forum whose posts are pictures and videos.
const guildMedia discord.ChannelType = 16

func isForum(t discord.ChannelType) bool {
	return t == discord.GuildForum || t == guildMedia
}

// holdsThreads reports whether threads are listed under a channel of kind t.
func holdsThreads(t discord.ChannelType) bool {
	return hasThreads(t) || isForum(t)
}

// forumState is the list of posts of the forum open.
type forumState struct {
	list    widget.List
	clicks  map[discord.ChannelID]*widget.Clickable
	newPost widget.Clickable
}

func (f *forumState) click(id discord.ChannelID) *widget.Clickable {
	if f.clicks == nil {
		f.clicks = make(map[discord.ChannelID]*widget.Clickable)
	}
	cl, ok := f.clicks[id]
	if !ok {
		cl = new(widget.Clickable)
		f.clicks[id] = cl
	}
	return cl
}

// loadOlderThreads asks Discord for the archived threads of parent, the most
// recently active first, as its app lists a channel's older threads. They
// land in the thread state while parent is still the one browsed.
func (c *chatView) loadOlderThreads(parent discord.Channel) {
	t := &c.threads
	t.browseParent, t.archived, t.loadErr, t.loading = parent.ID, nil, "", true
	if c.demo {
		for _, th := range t.demoArchived {
			if th.ParentID == parent.ID {
				t.archived = append(t.archived, th)
			}
		}
		t.loading = false
		return
	}
	go func() {
		res, err := c.st.SearchThreads(parent.ID, api.SearchThreadsData{
			Archived: true, SortBy: api.LastMessageTime, SortOrder: api.SortDescending, Limit: 25,
		})
		c.u.post(func() {
			if t.browseParent != parent.ID {
				return
			}
			t.loading = false
			if err != nil {
				t.loadErr = "Older threads could not be loaded"
				return
			}
			for i := range res.Threads {
				if !res.Threads[i].GuildID.IsValid() {
					res.Threads[i].GuildID = parent.GuildID
				}
			}
			for _, m := range res.Members {
				if m.UserID == c.me.ID || !m.UserID.IsValid() {
					t.setJoined(m.ID, true)
				}
			}
			t.archived = res.Threads
		})
	}()
}

// openForum shows the posts of a forum in place of messages.
func (c *chatView) openForum(ch discord.Channel) {
	c.forum.list.Axis = layout.Vertical
	c.forum.list.Position = layout.Position{}
	c.loadOlderThreads(ch)
}

// canPost reports whether the user can start a post in the forum.
func (c *chatView) canPost(forum discord.Channel) bool {
	return isForum(forum.Type) && c.can(forum.ID, discord.PermissionSendMessages)
}

// forumPosts are the posts of the forum to list: the active ones, the most
// recently active first, then the older ones.
func (c *chatView) forumPosts(forum discord.Channel) (active, older []discord.Channel) {
	chs, _ := c.st.Cabinet.Channels(forum.GuildID)
	active = activeThreads(chs, forum.ID)
	if c.threads.browseParent == forum.ID {
		older = c.threads.archived
	}
	return active, older
}

// postSummary is the line under a post's name: how much was said and when.
func postSummary(th discord.Channel) string {
	var parts []string
	if th.MessageCount > 0 {
		parts = append(parts, messageCount(th.MessageCount))
	}
	if last := lastActivity(th); last.IsValid() {
		parts = append(parts, formatTime(last.Time()))
	}
	if threadLocked(th) {
		parts = append(parts, "Locked")
	} else if threadArchived(th) {
		parts = append(parts, "Closed")
	}
	return strings.Join(parts, " · ")
}

// updateForum handles the clicks on the forum's list.
func (c *chatView) updateForum(gtx layout.Context, forum discord.Channel) {
	if c.forum.newPost.Clicked(gtx) && c.canPost(forum) {
		c.openCreateThread(forum, nil)
	}
	active, older := c.forumPosts(forum)
	for _, th := range append(active[:len(active):len(active)], older...) {
		if c.forum.click(th.ID).Clicked(gtx) {
			if _, err := c.st.Cabinet.Channel(th.ID); err != nil {
				c.st.Cabinet.ChannelSet(&th, false)
			}
			c.openThread(th)
			return
		}
	}
}

// forumLine is a line of the forum's list: a heading, a note, or a post.
type forumLine struct {
	title string
	post  *discord.Channel
	note  string
	noteC colorT
}

// layoutForum is the forum open: a button to post, and its posts.
func (c *chatView) layoutForum(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	forum := *c.selChannel
	c.updateForum(gtx, forum)
	if c.selChannel == nil || c.selChannel.ID != forum.ID {
		// A post was opened.
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	active, older := c.forumPosts(forum)
	t := &c.threads

	var lines []forumLine
	if len(active) > 0 {
		lines = append(lines, forumLine{title: "Active Posts"})
		for i := range active {
			lines = append(lines, forumLine{post: &active[i]})
		}
	}
	lines = append(lines, forumLine{title: "Older Posts"})
	switch {
	case t.browseParent == forum.ID && t.loading:
		lines = append(lines, forumLine{note: "Loading…", noteC: colorMuted})
	case t.browseParent == forum.ID && t.loadErr != "":
		lines = append(lines, forumLine{note: t.loadErr, noteC: colorRed})
	case len(older) == 0:
		lines = append(lines, forumLine{note: "No older posts", noteC: colorMuted})
	}
	for i := range older {
		lines = append(lines, forumLine{post: &older[i]})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(12), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						topic := forum.Topic
						if topic == "" {
							topic = fmt.Sprintf("%d active posts", len(active))
						}
						l := label(th, 14, colorMuted, topic)
						l.MaxLines = 2
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !c.canPost(forum) {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return smallButton(gtx, th, &c.forum.newPost, colorAccent, "New Post")
						})
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return listLayout(gtx, th, &c.forum.list, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
				ln := lines[i]
				switch {
				case ln.title != "":
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(14), Bottom: unit.Dp(4)}.Layout(gtx,
						bold(label(th, 12, colorMuted, strings.ToUpper(ln.title))).Layout)
				case ln.post == nil:
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx,
						label(th, 14, ln.noteC, ln.note).Layout)
				}
				return c.layoutForumPost(gtx, *ln.post)
			})
		}),
	)
}

// layoutForumPost is one post of the forum's list: its name, and under it
// how many messages it has and when the last one came.
func (c *chatView) layoutForumPost(gtx layout.Context, post discord.Channel) layout.Dimensions {
	th := c.u.th
	cl := c.forum.click(post.ID)
	unread := c.channelUnread(post.ID) != ningen.ChannelRead && c.threadJoined(post.ID)
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorSidebar
			if cl.Hovered() {
				bg = colorHover
			}
			return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							nameCol := colorText
							if c.threadJoined(post.ID) {
								nameCol = colorHeader
							}
							name := bold(label(th, 15, nameCol, post.Name))
							name.MaxLines = 2
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(name.Layout),
								layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
								layout.Rigid(oneLine(label(th, 12, colorMuted, postSummary(post))).Layout),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !unread {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return dot(gtx, colorHeader, 8)
							})
						}),
					)
				})
			})
		})
	})
}

// forumSign is the sign of a forum in the channel list, in place of the #.
func forumSign(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min = image.Pt(gtx.Dp(20), 0)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return icon(gtx, iconMenuThreads, 18, colorChannel)
	})
}
