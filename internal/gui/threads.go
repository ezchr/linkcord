package gui

import (
	"cmp"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
)

// Threads: the conversations that hang off a text or announcement channel.
// The gateway state keeps them with the other channels (Discord sends the
// joined ones in the ready event, and the active ones of a server after the
// client subscribes to it with threads, as ningen does); here is which the
// user joined, their lines under their channel, the list of a channel's
// threads, and making, joining and leaving them, through the same calls
// Discord's own app makes.

func isThread(t discord.ChannelType) bool {
	switch t {
	case discord.GuildPublicThread, discord.GuildPrivateThread, discord.GuildAnnouncementThread:
		return true
	}
	return false
}

// hasThreads reports whether threads can be made in a channel of kind t.
func hasThreads(t discord.ChannelType) bool {
	return t == discord.GuildText || t == discord.GuildAnnouncement
}

// threadPerms are the permissions in a thread, from those in its channel:
// sending is Send Messages in Threads there, and nobody without Manage
// Threads sends in a locked thread.
func threadPerms(parent discord.Permissions, locked bool) discord.Permissions {
	p := parent &^ discord.PermissionSendMessages
	if parent.Has(discord.PermissionSendMessagesInThreads) {
		p |= discord.PermissionSendMessages
	}
	if locked && !parent.Has(discord.PermissionManageThreads) {
		p &^= discord.PermissionSendMessages
	}
	return p
}

func threadArchived(ch discord.Channel) bool {
	return ch.ThreadMetadata != nil && ch.ThreadMetadata.Archived
}

func threadLocked(ch discord.Channel) bool {
	return ch.ThreadMetadata != nil && ch.ThreadMetadata.Locked
}

// listedThreads are the threads of parent among chs shown under it in the
// channel list: those joined and not archived, as Discord lists them, and
// the one open, whatever it is. They go oldest first.
func listedThreads(chs []discord.Channel, parent discord.ChannelID, joined map[discord.ChannelID]bool, open discord.ChannelID) []discord.Channel {
	var out []discord.Channel
	for _, ch := range chs {
		if !isThread(ch.Type) || ch.ParentID != parent {
			continue
		}
		if ch.ID != open && (!joined[ch.ID] || threadArchived(ch)) {
			continue
		}
		out = append(out, ch)
	}
	slices.SortFunc(out, func(a, b discord.Channel) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// activeThreads are the threads of parent among chs not archived, the most
// recently active first.
func activeThreads(chs []discord.Channel, parent discord.ChannelID) []discord.Channel {
	var out []discord.Channel
	for _, ch := range chs {
		if isThread(ch.Type) && ch.ParentID == parent && !threadArchived(ch) {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b discord.Channel) int { return cmp.Compare(lastActivity(b), lastActivity(a)) })
	return out
}

// threadState is what Linkcord keeps of threads beside the gateway state.
type threadState struct {
	// joined are the threads the user is a member of. The gateway state
	// loses the member a thread came with when the thread changes.
	joined map[discord.ChannelID]bool

	// The list of a channel's threads: the channel, its archived threads
	// as Discord found them, and whether they are on their way.
	browseParent discord.ChannelID
	archived     []discord.Channel
	loading      bool
	loadErr      string
	// demoArchived are the demo's archived threads.
	demoArchived []discord.Channel
	// parentClick is the channel's name in a thread's header.
	parentClick widget.Clickable
}

func (t *threadState) setJoined(id discord.ChannelID, on bool) {
	if t.joined == nil {
		t.joined = make(map[discord.ChannelID]bool)
	}
	if on {
		t.joined[id] = true
	} else {
		delete(t.joined, id)
	}
}

// noteGuild takes the threads of a server as the ready event or a server's
// arrival lists them: for a user, those are the threads they joined.
func (t *threadState) noteGuild(g gateway.GuildCreateEvent) {
	for _, th := range g.Threads {
		t.setJoined(th.ID, true)
	}
}

// note updates the threads joined with a gateway event of the user me. It
// reports whether the event is about threads, which changes the channel
// list.
func (t *threadState) note(ev gateway.Event, me discord.UserID) bool {
	switch ev := ev.(type) {
	case *gateway.GuildCreateEvent:
		t.noteGuild(*ev)
		return len(ev.Threads) > 0
	case *gateway.ThreadCreateEvent:
		// Discord sends the user's member with the threads they are added
		// to, and makes whoever starts a thread a member of it.
		if ev.ThreadMember != nil || ev.OwnerID == me && me.IsValid() {
			t.setJoined(ev.ID, true)
		}
		return true
	case *gateway.ThreadUpdateEvent:
		if ev.ThreadMember != nil {
			t.setJoined(ev.ID, true)
		}
		return true
	case *gateway.ThreadDeleteEvent:
		t.setJoined(ev.ID, false)
		return true
	case *gateway.ThreadListSyncEvent:
		// Its members are the user's, in the threads they joined.
		for _, m := range ev.Members {
			if !m.UserID.IsValid() || m.UserID == me {
				t.setJoined(m.ID, true)
			}
		}
		return true
	case *gateway.ThreadMemberUpdateEvent:
		if !ev.UserID.IsValid() || ev.UserID == me {
			t.setJoined(ev.ID, true)
		}
		return true
	case *gateway.ThreadMembersUpdateEvent:
		for _, m := range ev.AddedMembers {
			if m.UserID == me {
				t.setJoined(ev.ID, true)
			}
		}
		if slices.Contains(ev.RemovedMemberIDs, me) {
			t.setJoined(ev.ID, false)
		}
		return true
	}
	return false
}

// threadJoined reports whether the user is a member of the thread.
func (c *chatView) threadJoined(id discord.ChannelID) bool {
	return c.threads.joined[id]
}

// applyThreadEvent follows a thread event in what is shown.
func (c *chatView) applyThreadEvent(ev gateway.Event) {
	if !c.threads.note(ev, c.me.ID) {
		return
	}
	switch ev := ev.(type) {
	case *gateway.ThreadUpdateEvent:
		// The thread open shows its new name, archive and lock.
		if c.selChannel != nil && c.selChannel.ID == ev.ID {
			ch := ev.Channel
			c.selChannel = &ch
		}
	case *gateway.ThreadDeleteEvent:
		if c.selChannel != nil && c.selChannel.ID == ev.ID {
			c.closeChannel()
		}
	case *gateway.ThreadListSyncEvent:
		c.forgetStaleThreads(ev)
	}
	delete(c.perms, c.openThreadID())
	c.rebuildRows()
}

// openThreadID is the thread open, or 0.
func (c *chatView) openThreadID() discord.ChannelID {
	if c.selChannel != nil && isThread(c.selChannel.Type) {
		return c.selChannel.ID
	}
	return 0
}

// forgetStaleThreads drops the threads a sync no longer lists as active from
// the gateway state: Discord sends no update for each thread that archives
// while nobody looks. The one open stays.
func (c *chatView) forgetStaleThreads(ev *gateway.ThreadListSyncEvent) {
	chs, err := c.st.Cabinet.Channels(ev.GuildID)
	if err != nil {
		return
	}
	live := make(map[discord.ChannelID]bool, len(ev.Threads))
	for _, th := range ev.Threads {
		live[th.ID] = true
	}
	for _, ch := range chs {
		if !isThread(ch.Type) || live[ch.ID] || ch.ID == c.openThreadID() || c.threads.joined[ch.ID] {
			continue
		}
		if len(ev.ChannelIDs) > 0 && !slices.Contains(ev.ChannelIDs, ch.ParentID) {
			continue
		}
		c.st.Cabinet.ChannelRemove(&ch)
	}
}

// threadRows are the lines of the threads under a channel of the list.
func (c *chatView) threadRows(chs []discord.Channel, parent discord.Channel) []channelRow {
	if !hasThreads(parent.Type) {
		return nil
	}
	var rows []channelRow
	for _, th := range listedThreads(chs, parent.ID, c.threads.joined, c.openThreadID()) {
		rows = append(rows, channelRow{kind: rowThread, ch: th, label: th.Name})
	}
	return rows
}

// layoutThreadRow is a thread under its channel, joined to it by a line, as
// Discord draws them.
func (c *chatView) layoutThreadRow(gtx layout.Context, r channelRow) layout.Dimensions {
	th := c.u.th
	selected := c.selChannel != nil && c.selChannel.ID == r.ch.ID
	unread := c.channelUnread(r.ch.ID)
	indent := gtx.Dp(28)
	rgtx := gtx
	rgtx.Constraints.Max.X = max(0, gtx.Constraints.Max.X-indent)
	rgtx.Constraints.Min.X = min(rgtx.Constraints.Min.X, rgtx.Constraints.Max.X)
	t := op.Offset(image.Pt(indent, 0)).Push(gtx.Ops)
	dims := rowItem(rgtx, c.click(discord.Snowflake(r.ch.ID)), func(gtx layout.Context, hovered bool) layout.Dimensions {
		col := colorMuted
		if selected || hovered || unread != ningen.ChannelRead {
			col = colorHeader
		}
		name := label(th, 14, col, r.label)
		if unread != ningen.ChannelRead {
			name = bold(name)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, oneLine(name).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if unread != ningen.ChannelMentioned {
					return layout.Dimensions{}
				}
				return dot(gtx, colorRed, 8)
			}),
		)
	}, selected)
	t.Pop()
	// The line comes down from the channel's sign and turns to the thread.
	x, w := gtx.Dp(18), max(gtx.Dp(2), 1)
	mid := dims.Size.Y / 2
	line := colorSelected
	paint.FillShape(gtx.Ops, line, clip.Rect{Min: image.Pt(x, -gtx.Dp(8)), Max: image.Pt(x+w, mid+w)}.Op())
	paint.FillShape(gtx.Ops, line, clip.Rect{Min: image.Pt(x, mid), Max: image.Pt(indent-gtx.Dp(2), mid+w)}.Op())
	dims.Size.X += indent
	if rightClicked(gtx, c.rightTag("ch"+r.ch.ID.String()), dims.Size) {
		c.openChannelMenu(r.ch)
	}
	return dims
}

// openThread opens a thread, in its server.
func (c *chatView) openThread(ch discord.Channel) {
	if ch.GuildID.IsValid() {
		c.selectGuildIfNot(ch.GuildID)
	}
	c.selectChannel(ch)
	c.rebuildRows()
}

// openThreadID opens the thread id of a server, asking Discord for it when
// it is not known, as for the thread of an old message.
func (c *chatView) openThreadByID(id discord.ChannelID, guildID discord.GuildID) {
	if ch, err := c.st.Cabinet.Channel(id); err == nil {
		c.openThread(*ch)
		return
	}
	if c.demo {
		return
	}
	go func() {
		ch, err := c.st.Client.Channel(id)
		c.u.post(func() {
			if err != nil {
				c.showNotice("The thread could not be opened")
				return
			}
			if !ch.GuildID.IsValid() {
				ch.GuildID = guildID
			}
			c.st.Cabinet.ChannelSet(ch, false)
			c.openThread(*ch)
		})
	}()
}

// joinThread makes the user a member of a thread, which lists it under its
// channel.
func (c *chatView) joinThread(ch discord.Channel) {
	if c.demo {
		c.threads.setJoined(ch.ID, true)
		c.rebuildRows()
		return
	}
	go func() {
		err := c.st.JoinThread(ch.ID)
		c.u.post(func() {
			if err != nil {
				c.showNotice("Discord did not let you join the thread")
				return
			}
			c.threads.setJoined(ch.ID, true)
			c.rebuildRows()
		})
	}()
}

// leaveThread stops the user's membership of a thread.
func (c *chatView) leaveThread(ch discord.Channel) {
	if c.demo {
		c.threads.setJoined(ch.ID, false)
		c.rebuildRows()
		return
	}
	go func() {
		err := c.st.LeaveThread(ch.ID)
		c.u.post(func() {
			if err != nil {
				c.showNotice("Discord did not let you leave the thread")
				return
			}
			c.threads.setJoined(ch.ID, false)
			c.rebuildRows()
		})
	}()
}

// threadMenuItems are the menu of a thread: as a channel's, with joining or
// leaving it, and closing or locking it for those who manage threads.
func (c *chatView) threadMenuItems(ch discord.Channel) []menuItem {
	unread := c.channelUnread(ch.ID) != ningen.ChannelRead
	markCol := colorMuted
	if unread {
		markCol = colorText
	}
	items := []menuItem{{key: "read", text: "Mark As Read", col: markCol, act: func() {
		if unread {
			c.markRead(ch.ID)
		}
	}}, {sep: true}}
	if c.selChannel == nil || c.selChannel.ID != ch.ID {
		items = append(items, menuItem{text: "Open Thread", act: func() { c.openThread(ch) }})
	}
	if c.threadJoined(ch.ID) {
		items = append(items, menuItem{key: "leave", text: "Leave Thread", col: colorRed, lead: menuIcon(iconMenuLeave, colorRed), act: func() { c.leaveThread(ch) }})
	} else if !threadArchived(ch) {
		items = append(items, menuItem{key: "join", text: "Join Thread", act: func() { c.joinThread(ch) }})
	}
	parentPerms := c.myPerms(ch.ParentID)
	if parentPerms.Has(discord.PermissionManageThreads) || ch.OwnerID == c.me.ID && !threadLocked(ch) {
		close := "Close Thread"
		if threadArchived(ch) {
			close = "Open Thread Again"
		}
		items = append(items, menuItem{key: "archive", text: close, act: func() {
			c.patchThread(ch, map[string]any{"archived": !threadArchived(ch)}, func(md *discord.ThreadMetadata) { md.Archived = !md.Archived })
		}})
	}
	if parentPerms.Has(discord.PermissionManageThreads) {
		lock := "Lock Thread"
		if threadLocked(ch) {
			lock = "Unlock Thread"
		}
		items = append(items, menuItem{key: "lock", text: lock, act: func() {
			c.patchThread(ch, map[string]any{"locked": !threadLocked(ch)}, func(md *discord.ThreadMetadata) { md.Locked = !md.Locked })
		}})
	}
	items = append(items, menuItem{sep: true},
		menuItem{text: "Copy Link", act: func() {
			c.copyText = fmt.Sprintf("https://discord.com/channels/%s/%s", ch.GuildID, ch.ID)
		}},
		menuItem{text: "Copy Thread ID", lead: menuIcon(iconCopy, colorText), act: func() { c.copyText = ch.ID.String() }},
	)
	return items
}

// patchThread changes a thread's archive or lock, as Discord's menu does;
// demo applies it here.
func (c *chatView) patchThread(ch discord.Channel, body map[string]any, demo func(*discord.ThreadMetadata)) {
	if c.demo {
		md := discord.ThreadMetadata{}
		if ch.ThreadMetadata != nil {
			md = *ch.ThreadMetadata
		}
		demo(&md)
		ch.ThreadMetadata = &md
		c.st.Cabinet.ChannelSet(&ch, true)
		c.applyThreadEvent(&gateway.ThreadUpdateEvent{Channel: ch})
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointChannels+ch.ID.String(), httputil.WithJSONBody(body))
		if err != nil {
			c.u.post(func() { c.showNotice("Discord did not change the thread") })
		}
	}()
}

// openThreadBrowser lists the threads of a channel at the pointer: making
// one, those active, and those archived, which Discord is asked for.
func (c *chatView) openThreadBrowser(parent discord.Channel) {
	t := &c.threads
	t.browseParent, t.archived, t.loadErr, t.loading = parent.ID, nil, "", true
	c.openMenu(c.pointerAt, 300, func() []menuItem { return c.threadBrowserItems(parent) })
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
		// As Discord's app lists a channel's older threads.
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

func (c *chatView) threadBrowserItems(parent discord.Channel) []menuItem {
	t := &c.threads
	items := []menuItem{{title: true, text: "Threads"}}
	if c.canCreateThread(parent) {
		items = append(items, menuItem{key: "create", text: "Create Thread", col: colorAccent, lead: menuIcon(iconMenuChannel, colorAccent), act: func() {
			c.openCreateThread(parent, nil)
		}})
	}
	entry := func(th discord.Channel) menuItem {
		col := colorMuted
		if c.threadJoined(th.ID) {
			col = colorText
		}
		text := th.Name
		if th.MessageCount > 0 {
			text = fmt.Sprintf("%s · %s", th.Name, messageCount(th.MessageCount))
		}
		return menuItem{key: "thread" + th.ID.String(), text: text, lead: menuIcon(iconMenuThreads, col), act: func() {
			if _, err := c.st.Cabinet.Channel(th.ID); err != nil {
				c.st.Cabinet.ChannelSet(&th, false)
			}
			c.openThread(th)
		}}
	}
	chs, _ := c.st.Cabinet.Channels(parent.GuildID)
	active := activeThreads(chs, parent.ID)
	items = append(items, menuItem{sep: true}, menuItem{title: true, text: "Active Threads"})
	if len(active) == 0 {
		items = append(items, menuItem{key: "noactive", text: "No active threads", col: colorMuted})
	}
	for _, th := range active[:min(len(active), 50)] {
		items = append(items, entry(th))
	}
	items = append(items, menuItem{sep: true}, menuItem{title: true, text: "Older Threads"})
	switch {
	case t.loading:
		items = append(items, menuItem{key: "loading", text: "Loading…", col: colorMuted})
	case t.loadErr != "":
		items = append(items, menuItem{key: "err", text: t.loadErr, col: colorRed})
	case len(t.archived) == 0:
		items = append(items, menuItem{key: "noold", text: "No older threads", col: colorMuted})
	}
	for _, th := range t.archived {
		items = append(items, entry(th))
	}
	return items
}

// messageCount is how many messages a thread has, as its card says.
func messageCount(n int) string {
	if n == 1 {
		return "1 Message"
	}
	return fmt.Sprintf("%d Messages", n)
}

// canCreateThread reports whether the user can start threads in parent.
func (c *chatView) canCreateThread(parent discord.Channel) bool {
	return hasThreads(parent.Type) && c.can(parent.ID, discord.PermissionCreatePublicThreads|discord.PermissionSendMessagesInThreads)
}

// createThread is the form that starts a thread, from a message or on its
// own, as Discord's.
type createThread struct {
	open    bool
	parent  discord.Channel
	from    *discord.Message
	name    widget.Editor
	private bool
	focused bool
	busy    bool
	err     string

	privateBtn, cancel, create, close, x widget.Clickable
}

// defaultThreadName is the name a thread from m gets when none is typed: the
// start of its text, as Discord suggests it.
func defaultThreadName(m *discord.Message) string {
	if m == nil {
		return ""
	}
	s := strings.Join(strings.Fields(m.Content), " ")
	if s == "" {
		return "New Thread"
	}
	if utf8.RuneCountInString(s) > 40 {
		s = string([]rune(s)[:40])
	}
	return s
}

// openCreateThread opens the form to start a thread in parent, from the
// message from if it is not nil.
func (c *chatView) openCreateThread(parent discord.Channel, from *discord.Message) {
	ct := &c.newThread
	*ct = createThread{open: true, parent: parent, from: from}
	ct.name.SingleLine, ct.name.Submit = true, true
	ct.name.MaxLen = 100
}

func (c *chatView) submitCreateThread() {
	ct := &c.newThread
	name := strings.TrimSpace(ct.name.Text())
	if name == "" {
		name = defaultThreadName(ct.from)
	}
	if name == "" || ct.busy {
		return
	}
	parent := ct.parent
	archive := parent.DefaultAutoArchiveDuration
	if archive == 0 {
		archive = discord.ThreeDaysArchive
	}
	kind := discord.GuildPublicThread
	if parent.Type == discord.GuildAnnouncement {
		kind = discord.GuildAnnouncementThread
	} else if ct.private && ct.from == nil {
		kind = discord.GuildPrivateThread
	}
	from := ct.from
	if c.demo {
		id := discord.ChannelID(discord.NewSnowflake(time.Now()))
		if from != nil {
			id = discord.ChannelID(from.ID)
		}
		th := discord.Channel{ID: id, GuildID: parent.GuildID, ParentID: parent.ID, Type: kind, Name: name, OwnerID: c.me.ID,
			ThreadMetadata: &discord.ThreadMetadata{AutoArchiveDuration: archive}}
		c.st.Cabinet.ChannelSet(&th, false)
		if from != nil {
			c.noteThreadStarted(*from, th)
		}
		c.threads.setJoined(th.ID, true)
		ct.open = false
		c.openThread(th)
		return
	}
	ct.busy, ct.err = true, ""
	data := api.StartThreadData{Name: name, AutoArchiveDuration: archive, Type: kind}
	go func() {
		var th *discord.Channel
		var err error
		if from != nil {
			th, err = c.st.StartThreadWithMessage(parent.ID, from.ID, data)
		} else {
			th, err = c.st.StartThreadWithoutMessage(parent.ID, data)
		}
		c.u.post(func() {
			ct.busy = false
			if err != nil {
				ct.err = "Discord did not make it: " + err.Error()
				return
			}
			ct.open = false
			if !th.GuildID.IsValid() {
				th.GuildID = parent.GuildID
			}
			if _, err := c.st.Cabinet.Channel(th.ID); err != nil {
				c.st.Cabinet.ChannelSet(th, false)
			}
			if from != nil {
				c.noteThreadStarted(*from, *th)
			}
			c.threads.setJoined(th.ID, true)
			c.openThread(*th)
		})
	}()
}

// noteThreadStarted gives message m the thread th started from it, until
// Discord's update of the message comes.
func (c *chatView) noteThreadStarted(m discord.Message, th discord.Channel) {
	m.Thread = &th
	c.st.Cabinet.MessageSet(&m, true)
	for i := range c.messages {
		if c.messages[i].ID == m.ID {
			c.messages[i].Thread = &th
		}
	}
}

// layoutCreateThread draws the form over everything.
func (c *chatView) layoutCreateThread(gtx layout.Context) {
	th := c.u.th
	ct := &c.newThread
	size := gtx.Constraints.Max
	if ct.close.Clicked(gtx) || ct.x.Clicked(gtx) || ct.cancel.Clicked(gtx) {
		ct.open = false
		return
	}
	if ct.privateBtn.Clicked(gtx) {
		ct.private = !ct.private
	}
	if ct.create.Clicked(gtx) {
		c.submitCreateThread()
	}
	for {
		e, ok := ct.name.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			c.submitCreateThread()
		}
	}
	for {
		e, ok := gtx.Event(key.Filter{Focus: &ct.name, Name: key.NameEscape})
		if !ok {
			break
		}
		if ev, ok := e.(key.Event); ok && ev.State == key.Press {
			ct.open = false
			return
		}
	}
	if !ct.open {
		return
	}
	if !ct.focused {
		ct.focused = true
		gtx.Execute(key.FocusCmd{Tag: &ct.name})
	}

	ct.close.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	hint := defaultThreadName(ct.from)
	if hint == "" {
		hint = "New Thread"
	}
	w := min(gtx.Dp(460), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(64))}
	macro := op.Record(gtx.Ops)
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuThreads, 22, colorMuted) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Flexed(1, bold(label(th, 20, colorHeader, "Create Thread")).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return ct.x.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return icon(gtx, iconClose, 24, colorMuted)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, oneLine(label(th, 13, colorMuted, "in #"+ct.parent.Name)).Layout)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
	}
	if m := ct.from; m != nil {
		// The message it starts from, as a quote.
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorInput, 6, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(oneLine(bold(label(th, 13, colorHeader, c.memberName(ct.parent.GuildID, m.Author.ID, nil)))).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := label(th, 13, colorText, c.renderContent(*m))
							l.MaxLines = 3
							return l.Layout(gtx)
						}),
					)
				})
			})
		}), layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout))
	}
	rows = append(rows,
		layout.Rigid(bold(label(th, 14, colorHeader, "Thread Name")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &ct.name, hint) }),
	)
	if ct.from == nil && ct.parent.Type == discord.GuildText && c.can(ct.parent.ID, discord.PermissionCreatePrivateThreads) {
		rows = append(rows,
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return switchRow(gtx, th, &ct.privateBtn, "🔒 Private Thread", "Only people you invite and moderators can see this thread.", ct.private)
			}),
		)
	}
	if ct.err != "" {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, ct.err).Layout)
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
			if ct.busy {
				create = withAlpha(colorAccent, 0x80)
			}
			return layout.Flex{}.Layout(gtx,
				half(&ct.cancel, colorSecondaryButton, "Cancel"),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				half(&ct.create, create, "Create Thread"),
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
	solid(gtx, ct, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// threadCardIndex is the clickable of the card of a message's thread.
const threadCardIndex = 30

// layoutThreadCard is the box under a message that started a thread: its
// name and how many messages it has, which opens it, as on Discord.
func (c *chatView) layoutThreadCard(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	thread := *m.Thread
	// The thread as the gateway keeps it is newer than the message's copy.
	if ch, err := c.st.Cabinet.Channel(thread.ID); err == nil {
		thread = *ch
	}
	cl := c.msgClick(m.ID, threadCardIndex)
	if cl.Clicked(gtx) {
		ch := thread
		if !ch.GuildID.IsValid() {
			ch.GuildID = m.GuildID
		}
		c.u.post(func() {
			if _, err := c.st.Cabinet.Channel(ch.ID); err != nil {
				c.st.Cabinet.ChannelSet(&ch, false)
			}
			c.openThread(ch)
		})
	}
	return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorSidebar
			if cl.Hovered() {
				bg = colorHover
			}
			return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(12), Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					count := "See Thread ›"
					if thread.MessageCount > 0 {
						count = messageCount(thread.MessageCount) + " ›"
					}
					status := ""
					switch {
					case threadLocked(thread):
						status = "Locked"
					case threadArchived(thread):
						status = "Archived"
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuThreads, 18, colorMuted) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(oneLine(bold(label(th, 14, colorHeader, thread.Name))).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
										layout.Rigid(label(th, 13, colorLink, count).Layout),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											if status == "" {
												return layout.Dimensions{}
											}
											return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, label(th, 12, colorMuted, status).Layout)
										}),
									)
								}),
							)
						}),
					)
				})
			})
		})
	})
}

// layoutThreadCreated is the line Discord adds to a channel when someone
// starts a thread in it; the thread's name opens it.
func (c *chatView) layoutThreadCreated(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	name := c.memberName(c.selChannel.GuildID, m.Author.ID, nil)
	thread := m.Content
	var id discord.ChannelID
	if m.Reference != nil {
		id = m.Reference.ChannelID
	}
	if ch, err := c.st.Cabinet.Channel(id); err == nil && id.IsValid() {
		thread = ch.Name
	}
	cl := c.msgClick(m.ID, threadCardIndex)
	if cl.Clicked(gtx) && id.IsValid() {
		guildID := c.selChannel.GuildID
		c.u.post(func() { c.openThreadByID(id, guildID) })
	}
	return layout.Inset{Left: unit.Dp(30), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuThreads, 18, colorMuted) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(oneLine(bold(label(th, 14, colorHeader, name))).Layout),
					layout.Rigid(label(th, 14, colorMuted, " started a thread: ").Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							col := colorHeader
							if cl.Hovered() {
								col = colorLink
							}
							return oneLine(bold(label(th, 14, col, thread))).Layout(gtx)
						})
					}),
				)
			}),
		)
	})
}

// threadStarter is what the first line of a thread started from a message
// shows: that message, which lives in the channel above, or false when it
// was deleted.
func threadStarter(m discord.Message) (discord.Message, bool) {
	if m.ReferencedMessage == nil {
		return discord.Message{}, false
	}
	ref := *m.ReferencedMessage
	// The card of its thread would lead here again.
	ref.Thread = nil
	return ref, true
}

// layoutThreadStarter draws the message a thread started from, at its top,
// without the buttons that would act on it in the wrong channel.
func (c *chatView) layoutThreadStarter(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	ref, ok := threadStarter(m)
	if !ok {
		return layout.Inset{Left: unit.Dp(72), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(4)}.Layout(gtx,
			label(th, 14, colorMuted, "Sorry, the message this thread started from was deleted.").Layout)
	}
	guildID := c.selChannel.GuildID
	name := c.memberName(guildID, ref.Author.ID, nil)
	nameColor := colorHeader
	look := c.memberRoleLook(guildID, ref.Author.ID)
	if look.colored() {
		nameColor = rgb(uint32(look.color))
	}
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.layoutMessageBody(gtx, ref, false, name, nameColor, look)
	})
}

// threadStatus is what the header says of a thread in place of a topic.
func threadStatus(ch discord.Channel) string {
	switch {
	case threadLocked(ch) && threadArchived(ch):
		return "This thread is archived and locked."
	case threadLocked(ch):
		return "This thread is locked."
	case threadArchived(ch):
		return "This thread is archived. Sending a message opens it again."
	}
	return ""
}

// cannotSendText is what the box to write says where the user may not.
func (c *chatView) cannotSendText() string {
	if ch := c.selChannel; ch != nil && isThread(ch.Type) && threadLocked(*ch) {
		return "This thread is locked. Only those who manage threads can send messages in it."
	}
	return "You do not have permission to send messages in this channel."
}

// layoutThreadTitle is the header's title in a thread: its channel, which
// goes back to it, and the thread's name.
func (c *chatView) layoutThreadTitle(gtx layout.Context, ch discord.Channel) layout.Dimensions {
	th := c.u.th
	parent := "channel"
	if p, err := c.st.Cabinet.Channel(ch.ParentID); err == nil {
		parent = p.Name
	}
	cl := &c.threads.parentClick
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				col := colorMuted
				if cl.Hovered() {
					col = colorText
				}
				return oneLine(bold(label(th, 16, col, "# "+parent))).Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icon(gtx, iconFold, 16, colorMuted)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuThreads, 20, colorChannel) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(oneLine(bold(label(th, 16, colorHeader, ch.Name))).Layout),
	)
}

// layoutThreadButtons are the header's buttons for threads: a channel's
// threads, or, in a thread, joining it and its menu.
func (c *chatView) layoutThreadButtons(gtx layout.Context) layout.Dimensions {
	ch := c.selChannel
	if ch == nil || !ch.GuildID.IsValid() {
		return layout.Dimensions{}
	}
	th := c.u.th
	switch {
	case hasThreads(ch.Type):
		return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, &c.threadsBtn, iconMenuThreads, colorMuted, colorHeader)
		})
	case isThread(ch.Type):
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.threadJoined(ch.ID) || threadArchived(*ch) {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return smallButton(gtx, th, &c.joinThreadBtn, colorAccent, "Join Thread")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, &c.threadMoreBtn, iconMore, colorMuted, colorHeader)
				})
			}),
		)
	}
	return layout.Dimensions{}
}

// smallButton is a filled button as small as its text.
func smallButton(gtx layout.Context, th *material.Theme, click *widget.Clickable, bg colorT, txt string) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if click.Hovered() {
			bg = withAlpha(bg, 0xd0)
		}
		return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx,
				bold(label(th, 13, colorHeader, txt)).Layout)
		})
	})
}

// updateThreadButtons handles the clicks of the header's thread buttons.
func (c *chatView) updateThreadButtons(gtx layout.Context) {
	ch := c.selChannel
	if c.threadsBtn.Clicked(gtx) && ch != nil && hasThreads(ch.Type) {
		c.openThreadBrowser(*ch)
	}
	if c.joinThreadBtn.Clicked(gtx) && ch != nil && isThread(ch.Type) {
		c.joinThread(*ch)
	}
	if c.threadMoreBtn.Clicked(gtx) && ch != nil && isThread(ch.Type) {
		c.openChannelMenu(*ch)
	}
	if c.threads.parentClick.Clicked(gtx) && ch != nil && isThread(ch.Type) {
		if p, err := c.st.Cabinet.Channel(ch.ParentID); err == nil {
			c.selectChannel(*p)
			c.rebuildRows()
		}
	}
}
