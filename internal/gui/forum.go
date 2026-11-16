package gui

import (
	"cmp"
	"fmt"
	"image"
	"slices"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
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
	// first are the posts' first messages, by post, and firstOf the forum
	// they were asked for.
	first   map[discord.ChannelID]discord.Message
	firstOf discord.ChannelID
	// The buttons that show the posts as a list or as squares, and that
	// take or add a column of them.
	asList, asGrid, fewer, more widget.Clickable
}

// The fewest and most columns of squares, and those first chosen.
const (
	minForumColumns     = 2
	maxForumColumns     = 8
	defaultForumColumns = 3
)

// forumColumns is how many squares across the forums' posts show in, or 0
// if they are listed.
func (c *chatView) forumColumns() int {
	switch n := c.u.appearance.ForumColumns; {
	case n < 0:
		return 0
	case n == 0:
		return defaultForumColumns
	default:
		return min(max(n, minForumColumns), maxForumColumns)
	}
}

// setForumColumns shows the forums' posts in n squares across, or lists
// them if n is 0, and keeps the choice.
func (c *chatView) setForumColumns(n int) {
	if n == 0 {
		n = -1
	} else {
		n = min(max(n, minForumColumns), maxForumColumns)
	}
	c.u.appearance.ForumColumns = n
	c.forum.list.Position = layout.Position{}
	if !c.demo {
		saveAppearance(c.u.appearance)
	}
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
	c.loadFirstMessages(ch)
}

// noteFirstMessages keeps the first messages of posts of the forum.
func (c *chatView) noteFirstMessages(forum discord.ChannelID, msgs []discord.Message) {
	f := &c.forum
	if f.firstOf != forum {
		return
	}
	if f.first == nil {
		f.first = make(map[discord.ChannelID]discord.Message)
	}
	for _, m := range msgs {
		f.first[m.ChannelID] = m
	}
}

// loadFirstMessages asks Discord for the first messages of the forum's
// posts, active and older, which its list shows: the search of a channel's
// threads gives them with the threads, as Discord's app gets them.
func (c *chatView) loadFirstMessages(forum discord.Channel) {
	c.forum.first, c.forum.firstOf = nil, forum.ID
	if c.demo {
		return
	}
	for _, archived := range []bool{false, true} {
		go func() {
			res, err := c.st.SearchThreads(forum.ID, api.SearchThreadsData{
				Archived: archived, SortBy: api.LastMessageTime, SortOrder: api.SortDescending, Limit: 25,
			})
			if err != nil {
				return
			}
			c.u.post(func() { c.noteFirstMessages(forum.ID, res.FirstMessages) })
		}()
	}
}

// postPicture is the picture a post shows in the list: the first picture
// attached to its first message, or the picture of its first embed.
func postPicture(m discord.Message) (proxy, url string, ok bool) {
	for _, a := range m.Attachments {
		if isImage(a) {
			return a.Proxy, a.URL, true
		}
	}
	for _, e := range m.Embeds {
		if e.Thumbnail != nil && e.Thumbnail.URL != "" {
			return e.Thumbnail.Proxy, e.Thumbnail.URL, true
		}
		if e.Image != nil && e.Image.URL != "" {
			return e.Image.Proxy, e.Image.URL, true
		}
	}
	return "", "", false
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
	cols := c.forumColumns()
	if c.forum.asList.Clicked(gtx) {
		c.setForumColumns(0)
	}
	if c.forum.asGrid.Clicked(gtx) && cols == 0 {
		c.setForumColumns(defaultForumColumns)
	}
	if c.forum.fewer.Clicked(gtx) && cols > 0 {
		c.setForumColumns(cols - 1)
	}
	if c.forum.more.Clicked(gtx) && cols > 0 {
		c.setForumColumns(cols + 1)
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

// forumLine is a line of the forum's list: a heading, a note, a post, or a
// row of posts as squares.
type forumLine struct {
	title string
	post  *discord.Channel
	row   []discord.Channel
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
	cols := c.forumColumns()
	if cols > 0 {
		lines = gridLines(lines, cols)
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
						return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return c.layoutForumView(gtx, cols)
						})
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
				case ln.row != nil:
					return c.layoutForumRow(gtx, ln.row, cols)
				case ln.post == nil:
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx,
						label(th, 14, ln.noteC, ln.note).Layout)
				}
				return c.layoutForumPost(gtx, *ln.post)
			})
		}),
	)
}

// gridLines puts the posts that follow each other in lines into rows of
// cols squares; headings and notes keep their own lines.
func gridLines(lines []forumLine, cols int) []forumLine {
	var out []forumLine
	for _, ln := range lines {
		if ln.post == nil {
			out = append(out, ln)
			continue
		}
		if n := len(out); n > 0 && out[n-1].row != nil && len(out[n-1].row) < cols {
			out[n-1].row = append(out[n-1].row, *ln.post)
			continue
		}
		out = append(out, forumLine{row: []discord.Channel{*ln.post}})
	}
	return out
}

// layoutForumView are the buttons that choose how the posts show: listed,
// or as squares, with how many across.
func (c *chatView) layoutForumView(gtx layout.Context, cols int) layout.Dimensions {
	th := c.u.th
	f := &c.forum
	chip := func(cl *widget.Clickable, txt string, on bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			bg := colorInput
			switch {
			case on:
				bg = colorAccent
			case cl.Hovered():
				bg = colorHover
			}
			return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(9), Right: unit.Dp(9)}.Layout(gtx,
							label(th, 13, colorHeader, txt).Layout)
					})
				})
			})
		}
	}
	children := []layout.FlexChild{
		layout.Rigid(chip(&f.asList, "List", cols == 0)),
		layout.Rigid(chip(&f.asGrid, "Grid", cols > 0)),
	}
	if cols > 0 {
		children = append(children,
			layout.Rigid(chip(&f.fewer, "−", false)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(2), Right: unit.Dp(6)}.Layout(gtx, label(th, 13, colorText, fmt.Sprint(cols)).Layout)
			}),
			layout.Rigid(chip(&f.more, "+", false)),
		)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// layoutForumRow is a row of posts as squares, cols across.
func (c *chatView) layoutForumRow(gtx layout.Context, posts []discord.Channel, cols int) layout.Dimensions {
	gap := gtx.Dp(8)
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		side := max((gtx.Constraints.Max.X-gap*(cols-1))/cols, gtx.Dp(60))
		for i, post := range posts {
			t := op.Offset(image.Pt(i*(side+gap), 0)).Push(gtx.Ops)
			cgtx := gtx
			cgtx.Constraints = layout.Exact(image.Pt(side, side))
			c.layoutForumSquare(cgtx, post)
			t.Pop()
		}
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, side)}
	})
}

// layoutForumSquare is a post as a square: its picture, or its sign when
// it has none, over its name and how much was said.
func (c *chatView) layoutForumSquare(gtx layout.Context, post discord.Channel) layout.Dimensions {
	th := c.u.th
	size := gtx.Constraints.Max
	cl := c.forum.click(post.ID)
	first, hasFirst := c.forum.first[post.ID]
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		bg := colorSidebar
		if cl.Hovered() {
			bg = colorHover
		}
		r := image.Rectangle{Max: size}
		defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
		paint.FillShape(gtx.Ops, bg, clip.Rect(r).Op())
		// The text takes what it needs at the bottom, the picture the rest.
		textH := gtx.Dp(64)
		if hasFirst && len(first.Reactions) > 0 {
			textH = gtx.Dp(86)
		}
		textH = min(textH, size.Y*2/3)
		picH := size.Y - textH
		pic := image.Rectangle{Max: image.Pt(size.X, picH)}
		proxy, url, ok := postPicture(first)
		var img *paint.ImageOp
		if hasFirst && ok {
			img = c.u.images.get(mediaURL(proxy, url, size.X, picH), max(size.X, picH))
		}
		if img != nil {
			st := clip.Rect(pic).Push(gtx.Ops)
			igtx := gtx
			igtx.Constraints = layout.Exact(pic.Max)
			widget.Image{Src: *img, Fit: widget.Cover}.Layout(igtx)
			st.Pop()
		} else {
			paint.FillShape(gtx.Ops, colorPanel, clip.Rect(pic).Op())
			s := min(gtx.Dp(32), picH/2)
			t := op.Offset(image.Pt((size.X-s)/2, (picH-s)/2)).Push(gtx.Ops)
			igtx := gtx
			igtx.Constraints = layout.Exact(image.Pt(s, s))
			iconMenuThreads.Layout(igtx, colorChannel)
			t.Pop()
		}
		t := op.Offset(image.Pt(0, picH)).Push(gtx.Ops)
		tgtx := gtx
		tgtx.Constraints = layout.Exact(image.Pt(size.X, textH))
		layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(tgtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			nameCol := colorText
			if c.threadJoined(post.ID) {
				nameCol = colorHeader
			}
			name := bold(label(th, 13, nameCol, post.Name))
			name.MaxLines = 2
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(name.Layout),
				layout.Rigid(oneLine(label(th, 11, colorMuted, postSummary(post))).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutPostReactions(gtx, first, 4) }),
			)
		})
		t.Pop()
		return layout.Dimensions{Size: size}
	})
}

// layoutPostReactions are the reactions to a post's first message, the most
// given first, at most n of them: each its emoji and how many.
func (c *chatView) layoutPostReactions(gtx layout.Context, first discord.Message, n int) layout.Dimensions {
	if len(first.Reactions) == 0 {
		return layout.Dimensions{}
	}
	th := c.u.th
	rs := slices.Clone(first.Reactions)
	slices.SortStableFunc(rs, func(a, b discord.Reaction) int { return cmp.Compare(b.Count, a.Count) })
	var children []layout.FlexChild
	for _, r := range rs[:min(len(rs), n)] {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8), Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutEmoji(gtx, r.Emoji, 15) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(3)}.Layout),
					layout.Rigid(label(th, 12, colorMuted, fmt.Sprint(r.Count)).Layout),
				)
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// layoutForumPost is one post of the forum's list: its name, and under it
// how many messages it has and when the last one came.
func (c *chatView) layoutForumPost(gtx layout.Context, post discord.Channel) layout.Dimensions {
	th := c.u.th
	cl := c.forum.click(post.ID)
	unread := c.channelUnread(post.ID) != ningen.ChannelRead && c.threadJoined(post.ID)
	first, hasFirst := c.forum.first[post.ID]
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
							rows := []layout.FlexChild{layout.Rigid(name.Layout)}
							if hasFirst {
								// Who started it and how it starts.
								text := strings.Join(strings.Fields(c.renderContent(first)), " ")
								who := c.memberName(post.GuildID, first.Author.ID, nil)
								if text != "" {
									who += ": "
								}
								rows = append(rows,
									layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layout.Flex{}.Layout(gtx,
											layout.Rigid(oneLine(bold(label(th, 13, colorText, who))).Layout),
											layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
												l := label(th, 13, colorMuted, text)
												l.MaxLines = 2
												return l.Layout(gtx)
											}),
										)
									}),
								)
							}
							rows = append(rows,
								layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
										layout.Rigid(oneLine(label(th, 12, colorMuted, postSummary(post))).Layout),
										layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutPostReactions(gtx, first, 6) }),
									)
								}),
							)
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							proxy, url, ok := postPicture(first)
							if !hasFirst || !ok {
								return layout.Dimensions{}
							}
							// The post's picture, small, at its right.
							px := gtx.Dp(72)
							size := image.Pt(px, px)
							return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								r := image.Rectangle{Max: size}
								defer clip.UniformRRect(r, gtx.Dp(6)).Push(gtx.Ops).Pop()
								img := c.u.images.get(mediaURL(proxy, url, px, px), px)
								if img == nil {
									paint.FillShape(gtx.Ops, colorPanel, clip.Rect(r).Op())
									return layout.Dimensions{Size: size}
								}
								gtx.Constraints = layout.Exact(size)
								return widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
							})
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
