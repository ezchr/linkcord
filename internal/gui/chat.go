package gui

import (
	"cmp"
	"context"
	"image"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/ningen/v3/states/read"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconVoice  = mustIcon(icons.AVVolumeUp)
	iconLogout = mustIcon(icons.ActionExitToApp)
	iconHome   = mustIcon(icons.CommunicationChatBubble)
	iconAttach = mustIcon(icons.EditorAttachFile)
	iconReply  = mustIcon(icons.ContentReply)
	iconExpand = mustIcon(icons.NavigationExpandMore)
	iconFold   = mustIcon(icons.NavigationChevronRight)
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

// icon lays out ic as a square of size in color c.
func icon(gtx layout.Context, ic *widget.Icon, size unit.Dp, c colorT) layout.Dimensions {
	px := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	return ic.Layout(gtx, c)
}

type rowKind int

const (
	rowText rowKind = iota
	rowVoice
	rowCategory
	rowVoiceMember
	rowDM
	// rowHome is the friends or the message requests, at the top of the
	// direct messages.
	rowHome
)

// channelRow is a line of the channel column.
type channelRow struct {
	kind  rowKind
	ch    discord.Channel
	label string
	// user is who a voice member row stands for.
	user discord.UserID
	// muted reports whether a voice member is muted or deafened, and deaf
	// whether deafened.
	muted, deaf bool
	// avatar is the picture of a voice member, "" when it isn't known.
	avatar string
}

type chatView struct {
	// friends is the friends page and the message requests of the direct
	// messages.
	friends friendsState

	// detached is set while older messages show in place of the newest,
	// after a jump; flashID is lit until flashUntil.
	detached    bool
	jumpPresent widget.Clickable
	flashID     discord.MessageID
	flashUntil  time.Time
	// downloads are the files saved to the Downloads folder, by address.
	downloads map[string]*download
	// voice is the voice message playing, and voiceCtl the widgets of each.
	voice    *voicePlayer
	voiceCtl map[string]*voiceControls
	// deleted keeps deleted messages, shown in their place with a sign.
	deleted *deletedLog
	// The video volume chosen last, kept for the next video.
	lastVolume float32
	volumeSet  bool
	u          *ui
	st         *ningen.State

	events     chan gateway.Event
	done       chan struct{}
	stopEvents func()
	closed     bool

	me discord.User

	clicks map[discord.Snowflake]*widget.Clickable

	guildOrder []discord.GuildID
	homeClick  widget.Clickable
	// selGuild is the server shown, or NullGuildID for direct messages.
	selGuild    discord.GuildID
	lastChannel map[discord.GuildID]discord.ChannelID
	collapsed   map[discord.ChannelID]bool

	rows       []channelRow
	selChannel *discord.Channel

	messages              []discord.Message
	loading, loadingOlder bool
	noOlder               bool
	loadErr, sendErr      string
	historyEpoch          uint64
	refreshing            bool
	refreshPending        bool
	refreshFailures       int
	refreshCancel         context.CancelFunc
	lastRefresh           time.Time
	refreshLive           map[discord.MessageID]*discord.Message
	selectables           map[discord.MessageID]*widget.Selectable
	// richStates hold the clickable spans of each message, and revealed the
	// messages whose spoilers were clicked.
	richStates map[discord.MessageID]*richtextState
	revealed   map[discord.MessageID]bool
	linkClicks map[string]*widget.Clickable

	guildList, channelList, messageList widget.List
	panes                               paneSettings
	panesLoaded                         bool
	channelResize, memberResize         paneResize

	composer    widget.Editor
	typingUntil time.Time
	typers      map[discord.UserID]time.Time

	// comp is the box to write, and focusComposer asks to give it the
	// keyboard focus on the next frame.
	comp          composerState
	focusComposer bool

	// viewer shows a picture over everything, if open.
	viewer *imageViewer
	video  *videoViewer

	// The profile card, if open, and the profiles already loaded.
	profile   *profileView
	profiles  map[profileKey]*profileData
	selfClick widget.Clickable
	// userClicks open the profile of the member clicked in the member list,
	// and msgClicks of the author clicked in the messages.
	userClicks map[discord.UserID]*widget.Clickable
	msgClicks  map[msgClickKey]*widget.Clickable
	titleClick widget.Clickable

	// The member list at the right of the messages.
	showMembers   bool
	membersToggle widget.Clickable
	memberRows    []memberRow
	memberList    widget.List
	memberChunk   int
	// demo reports whether the chat shows made-up data, and must not
	// contact Discord.
	demo bool

	// presencePending reports whether a redraw for presence updates is
	// already queued, so that busy servers do not flood the window.
	presencePending atomic.Bool

	// act is what can be done to messages: see actions.go.
	act actionState
}

// msgClickKey is the avatar (part 0) or the name (part 1) of a message.
type msgClickKey struct {
	id   discord.MessageID
	part int
}

func (c *chatView) userClick(id discord.UserID) *widget.Clickable {
	cl, ok := c.userClicks[id]
	if !ok {
		cl = new(widget.Clickable)
		c.userClicks[id] = cl
	}
	return cl
}

func (c *chatView) msgClick(id discord.MessageID, part int) *widget.Clickable {
	k := msgClickKey{id, part}
	cl, ok := c.msgClicks[k]
	if !ok {
		cl = new(widget.Clickable)
		c.msgClicks[k] = cl
	}
	return cl
}

func newChatView(u *ui, st *ningen.State) *chatView {
	c := &chatView{
		u:           u,
		st:          st,
		events:      make(chan gateway.Event, 64),
		done:        make(chan struct{}),
		clicks:      make(map[discord.Snowflake]*widget.Clickable),
		lastChannel: make(map[discord.GuildID]discord.ChannelID),
		collapsed:   make(map[discord.ChannelID]bool),
		selectables: make(map[discord.MessageID]*widget.Selectable),
		linkClicks:  make(map[string]*widget.Clickable),
		typers:      make(map[discord.UserID]time.Time),
		profiles:    make(map[profileKey]*profileData),
		userClicks:  make(map[discord.UserID]*widget.Clickable),
		msgClicks:   make(map[msgClickKey]*widget.Clickable),
		richStates:  make(map[discord.MessageID]*richtextState),
		revealed:    make(map[discord.MessageID]bool),
		showMembers: true,
		act:         newActionState(),
	}
	if os.Getenv(demoEnvVar) != "" {
		c.deleted = newDeletedLog("")
	} else {
		c.deleted = newDeletedLog(deletedLogPath())
	}
	// Deleted messages are copied before the state forgets them.
	st.PreHandler = handler.New()
	st.PreHandler.AddSyncHandler(func(ev *gateway.MessageDeleteEvent) {
		if m, err := st.Cabinet.Message(ev.ChannelID, ev.ID); err == nil {
			c.deleted.record(*m)
		}
	})
	st.PreHandler.AddSyncHandler(func(ev *gateway.MessageDeleteBulkEvent) {
		for _, id := range ev.IDs {
			if m, err := st.Cabinet.Message(ev.ChannelID, id); err == nil {
				c.deleted.record(*m)
			}
		}
	})
	c.friends.init()
	c.memberList.Axis = layout.Vertical
	c.guildList.Axis = layout.Vertical
	c.channelList.Axis = layout.Vertical
	c.messageList.Axis = layout.Vertical
	c.messageList.ScrollToEnd = true
	c.composer.Submit = true

	c.stopEvents = st.AddChanHandler(c.events)
	go c.pump()
	return c
}

// pump forwards the gateway events the interface shows to the window's
// goroutine.
func (c *chatView) pump() {
	for {
		select {
		case <-c.done:
			return
		case ev := <-c.events:
			switch ev := ev.(type) {
			case *gateway.ReadyEvent:
				c.u.post(func() { c.u.onReady(c.st, ev) })
			case *gateway.ResumedEvent:
				c.u.post(func() {
					if !c.closed && c.u.chat == c {
						c.refreshCurrentChat()
					}
				})
			case *authTokenRefreshEvent:
				c.u.post(func() { c.u.tokenRefreshed(c.st, ev.Token) })
			case *gateway.GuildMemberListUpdateEvent:
				c.u.post(func() {
					if ch := c.selChannel; ch != nil && ch.GuildID == ev.GuildID && c.st.MemberState.ListID(ch) == ev.ID {
						c.rebuildMembers()
					}
				})
			case *gateway.PresenceUpdateEvent:
				// Presences are read while laying out; one redraw at a time
				// is enough for any number of updates.
				if c.presencePending.CompareAndSwap(false, true) {
					c.u.post(func() {
						c.presencePending.Store(false)
						if ch := c.selChannel; ch != nil && ch.Type == discord.GroupDM {
							c.rebuildMembers()
						}
					})
				}
			case *gateway.RelationshipAddEvent, *gateway.RelationshipRemoveEvent:
				// The gateway state keeps the friends; the lists read it
				// while laying out.
				c.u.win.Invalidate()
			case *gateway.MessageCreateEvent, *gateway.MessageUpdateEvent, *gateway.MessageDeleteEvent, *gateway.MessageDeleteBulkEvent,
				*gateway.TypingStartEvent, *read.UpdateEvent,
				*gateway.GuildCreateEvent, *gateway.GuildDeleteEvent, *gateway.GuildUpdateEvent,
				*gateway.ChannelCreateEvent, *gateway.ChannelUpdateEvent, *gateway.ChannelDeleteEvent,
				*gateway.VoiceStateUpdateEvent, *gateway.GuildMembersChunkEvent,
				*gateway.MessageReactionAddEvent, *gateway.MessageReactionRemoveEvent,
				*gateway.MessageReactionRemoveAllEvent, *gateway.MessageReactionRemoveEmojiEvent:
				c.u.post(func() { c.apply(ev) })
			}
		}
	}
}

func (c *chatView) close() {
	if c.closed {
		return
	}
	c.closed = true
	if c.refreshCancel != nil {
		c.refreshCancel()
	}
	c.closeVideo()
	c.stopVoice()
	// A call does not outlive the session it belongs to.
	c.u.leaveVoice()
	c.u.voice.demoChannel = 0
	c.stopEvents()
	close(c.done)
	st := c.st
	go func() {
		if err := st.Close(); err != nil {
			slog.Error("failed to close session", "err", err)
		}
	}()
}

// load fills the chat from the ready event.
func (c *chatView) load(ready *gateway.ReadyEvent) {
	c.me = ready.User

	seen := make(map[discord.GuildID]bool)
	var order []discord.GuildID
	// The settings can be missing, in which case the servers keep the order
	// of the ready event.
	if settings := ready.UserSettings; settings != nil {
		for _, folder := range settings.GuildFolders {
			for _, id := range folder.GuildIDs {
				if !seen[id] {
					seen[id] = true
					order = append(order, id)
				}
			}
		}
		for _, id := range settings.GuildPositions {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	}
	// Servers missing from the settings were joined last and go first, as
	// in Discord.
	var missing []discord.GuildID
	for _, g := range ready.Guilds {
		if !seen[g.ID] {
			missing = append(missing, g.ID)
		}
	}
	c.guildOrder = append(missing, order...)
	if c.selChannel != nil {
		// A fresh READY after reconnect must keep the conversation open.
		c.rebuildRows()
		c.rebuildMembers()
		c.requestMembers(0)
		c.refreshCurrentChat()
	} else {
		c.selectGuild(discord.NullGuildID)
		// The message requests are known only from the list of direct
		// messages, which the gateway does not send with them marked.
		c.loadMessageRequests()
	}
}

func (c *chatView) click(id discord.Snowflake) *widget.Clickable {
	cl, ok := c.clicks[id]
	if !ok {
		cl = new(widget.Clickable)
		c.clicks[id] = cl
	}
	return cl
}

func (c *chatView) selectGuild(id discord.GuildID) {
	c.selGuild = id
	c.channelList.Position = layout.Position{}
	if id.IsValid() && !c.demo {
		go c.st.MemberState.Subscribe(id)
	}
	c.rebuildRows()
	if chID, ok := c.lastChannel[id]; ok {
		for _, r := range c.rows {
			if r.ch.ID == chID && r.kind != rowVoiceMember {
				c.selectChannel(r.ch)
				return
			}
		}
	}
	// Open the first text channel of a server, as Discord does.
	if id.IsValid() {
		for _, r := range c.rows {
			if r.kind == rowText {
				c.selectChannel(r.ch)
				return
			}
		}
	}
	// The direct messages open on the friends, as in Discord.
	if !id.IsValid() {
		c.openHome(homeFriends)
		return
	}
	c.closeChannel()
	c.setTitle(appName)
}

// closeChannel leaves the open channel, if any, so that nothing is shown in
// the middle until something else is opened.
func (c *chatView) closeChannel() {
	if c.refreshCancel != nil {
		c.refreshCancel()
		c.refreshCancel = nil
	}
	// Loads on their way for the channel are dropped when they arrive.
	c.historyEpoch++
	c.friends.page = homeNone
	c.selChannel, c.messages = nil, nil
	c.comp.files, c.comp.mentions, c.comp.mentionOn, c.comp.emoji = nil, nil, false, nil
	c.act.reset()
	c.memberRows, c.memberChunk = nil, 0
}

func isVoice(t discord.ChannelType) bool {
	return t == discord.GuildVoice || t == discord.GuildStageVoice
}

func isTextual(t discord.ChannelType) bool {
	switch t {
	case discord.GuildText, discord.GuildAnnouncement, discord.DirectMessage, discord.GroupDM,
		discord.GuildPublicThread, discord.GuildPrivateThread, discord.GuildAnnouncementThread:
		return true
	}
	return false
}

// rebuildRows recomputes the lines of the channel column.
func (c *chatView) rebuildRows() {
	c.rows = c.rows[:0]
	if !c.selGuild.IsValid() {
		c.rows = append(c.rows, channelRow{kind: rowHome, ch: discord.Channel{ID: friendsRowID}, label: "Friends"})
		if len(c.friends.requests) > 0 {
			c.rows = append(c.rows, channelRow{kind: rowHome, ch: discord.Channel{ID: requestsRowID}, label: "Message Requests"})
		}
		channels, err := c.st.PrivateChannels()
		if err != nil {
			slog.Error("failed to get private channels", "err", err)
		}
		slices.SortFunc(channels, func(a, b discord.Channel) int {
			return cmp.Compare(lastActivity(b), lastActivity(a))
		})
		for _, ch := range channels {
			// Message requests wait in their own page until accepted.
			if c.friends.requestIDs[ch.ID] {
				continue
			}
			c.rows = append(c.rows, channelRow{kind: rowDM, ch: ch, label: c.dmName(ch)})
		}
		return
	}

	channels, err := c.st.Cabinet.Channels(c.selGuild)
	if err != nil {
		slog.Error("failed to get channels", "err", err, "guild_id", c.selGuild)
		return
	}
	var top, categories []discord.Channel
	children := make(map[discord.ChannelID][]discord.Channel)
	for _, ch := range channels {
		switch {
		case ch.Type == discord.GuildCategory:
			categories = append(categories, ch)
		case !isTextual(ch.Type) && !isVoice(ch.Type):
			// Forums and other kinds are not supported yet.
		case ch.Type == discord.GuildPublicThread || ch.Type == discord.GuildPrivateThread || ch.Type == discord.GuildAnnouncementThread:
		case !c.st.HasPermissions(ch.ID, discord.PermissionViewChannel):
		case ch.ParentID.IsValid():
			children[ch.ParentID] = append(children[ch.ParentID], ch)
		default:
			top = append(top, ch)
		}
	}
	// Text channels go before voice channels, then by position.
	sortChannels := func(chs []discord.Channel) {
		slices.SortFunc(chs, func(a, b discord.Channel) int {
			va, vb := isVoice(a.Type), isVoice(b.Type)
			if va != vb {
				if va {
					return 1
				}
				return -1
			}
			return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
		})
	}
	slices.SortFunc(categories, func(a, b discord.Channel) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})

	voiceStates, _ := c.st.Cabinet.VoiceStates(c.selGuild)
	add := func(ch discord.Channel) {
		if isVoice(ch.Type) {
			c.rows = append(c.rows, channelRow{kind: rowVoice, ch: ch, label: ch.Name})
			var members []channelRow
			for _, vs := range voiceStates {
				if vs.ChannelID == ch.ID {
					members = append(members, channelRow{
						kind:   rowVoiceMember,
						ch:     ch,
						user:   vs.UserID,
						label:  c.memberName(c.selGuild, vs.UserID, vs.Member),
						muted:  vs.Mute || vs.SelfMute || vs.Deaf || vs.SelfDeaf,
						deaf:   vs.Deaf || vs.SelfDeaf,
						avatar: c.voiceAvatarURL(c.selGuild, vs),
					})
				}
			}
			// By name, so that people keep their place instead of moving
			// whenever their voice state changes.
			slices.SortFunc(members, func(a, b channelRow) int {
				return cmp.Or(
					strings.Compare(strings.ToLower(a.label), strings.ToLower(b.label)),
					cmp.Compare(a.user, b.user),
				)
			})
			c.rows = append(c.rows, members...)
			return
		}
		c.rows = append(c.rows, channelRow{kind: rowText, ch: ch, label: ch.Name})
	}

	sortChannels(top)
	for _, ch := range top {
		add(ch)
	}
	for _, cat := range categories {
		chs := children[cat.ID]
		if len(chs) == 0 {
			continue
		}
		c.rows = append(c.rows, channelRow{kind: rowCategory, ch: cat, label: strings.ToUpper(cat.Name)})
		if c.collapsed[cat.ID] {
			continue
		}
		sortChannels(chs)
		for _, ch := range chs {
			add(ch)
		}
	}
}

func lastActivity(ch discord.Channel) discord.Snowflake {
	if ch.LastMessageID.IsValid() {
		return discord.Snowflake(ch.LastMessageID)
	}
	return discord.Snowflake(ch.ID)
}

// dmName is how a direct message is called: its name, or its recipients.
func (c *chatView) dmName(ch discord.Channel) string {
	if ch.Name != "" {
		return ch.Name
	}
	names := make([]string, 0, len(ch.DMRecipients))
	for _, r := range ch.DMRecipients {
		name := r.DisplayOrUsername()
		if rel, ok := c.st.RelationshipState.FullRelationship(r.ID); ok && rel.Nickname != nil && *rel.Nickname != "" {
			name = *rel.Nickname
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "Direct message"
	}
	return strings.Join(names, ", ")
}

// voiceAvatarURL is the picture of the user of vs: their server avatar, if
// any, or their own. It is "" when the user isn't known yet, which shows
// their initials instead.
func (c *chatView) voiceAvatarURL(guildID discord.GuildID, vs discord.VoiceState) string {
	if vs.Member != nil {
		if vs.Member.Avatar != "" {
			return cdnSize(vs.Member.AvatarURLWithType(discord.PNGImage, guildID))
		}
		if vs.Member.User.ID.IsValid() {
			return c.authorAvatarURL(guildID, vs.Member.User)
		}
	}
	if m, err := c.st.Cabinet.Member(guildID, vs.UserID); err == nil {
		return c.authorAvatarURL(guildID, m.User)
	}
	if u, err := c.st.Cabinet.User(vs.UserID); err == nil {
		return c.authorAvatarURL(guildID, *u)
	}
	return ""
}

// memberName is how a user is called in a server: their nickname, display
// name, or username. member may be nil.
func (c *chatView) memberName(guildID discord.GuildID, userID discord.UserID, member *discord.Member) string {
	if member == nil && guildID.IsValid() {
		member, _ = c.st.Cabinet.Member(guildID, userID)
	}
	if member != nil {
		if member.Nick != "" {
			return member.Nick
		}
		return member.User.DisplayOrUsername()
	}
	if u, err := c.st.Cabinet.User(userID); err == nil {
		return u.DisplayOrUsername()
	}
	return "User"
}

func (c *chatView) selectChannel(ch discord.Channel) {
	if c.selChannel != nil && c.selChannel.ID == ch.ID {
		return
	}
	if c.refreshCancel != nil {
		c.refreshCancel()
		c.refreshCancel = nil
	}
	c.historyEpoch++
	c.friends.page = homeNone
	c.refreshing, c.refreshPending, c.refreshFailures = false, false, 0
	c.lastRefresh, c.refreshLive = time.Time{}, nil
	c.selChannel = &ch
	c.lastChannel[c.selGuild] = ch.ID
	c.messages, c.loadErr, c.sendErr = nil, "", ""
	c.loading, c.loadingOlder, c.noOlder = true, false, false
	c.detached = false
	c.messageList.Position = layout.Position{}
	clear(c.typers)
	clear(c.selectables)
	clear(c.msgClicks)
	clear(c.richStates)
	clear(c.revealed)
	c.comp.files, c.comp.mentions, c.comp.mentionOn, c.comp.emoji = nil, nil, false, nil
	c.act.reset()
	c.memberRows, c.memberChunk = nil, 0
	c.memberList.Position = layout.Position{}
	c.rebuildMembers()
	c.requestMembers(0)

	title := "#" + ch.Name
	if !ch.GuildID.IsValid() {
		title = "@" + c.dmName(ch)
	}
	c.setTitle(title + " - " + appName)

	id, epoch := ch.ID, c.historyEpoch
	demo := c.demo
	go func() {
		// The history comes from Discord, which may hold newer messages than
		// the cache; the demo has no connection and keeps its own.
		var msgs []discord.Message
		var err error
		if demo {
			msgs, err = c.st.Cabinet.Messages(id)
		} else {
			msgs, err = c.st.Client.Messages(id, 50)
		}
		c.u.post(func() {
			if c.closed || c.historyEpoch != epoch || c.selChannel == nil || c.selChannel.ID != id {
				return
			}
			c.loading = false
			defer c.runPendingRefresh()
			if err != nil {
				c.loadErr = "Could not load messages: " + err.Error()
				return
			}
			c.noOlder = len(msgs) < 50
			c.messages = c.deleted.merge(id, sortedMessages(msgs), historyStart(msgs, !c.noOlder), 0)
			c.markRead(id)
			c.requestAuthors(c.messages)
		})
	}()
}

func sortedMessages(msgs []discord.Message) []discord.Message {
	msgs = slices.Clone(msgs)
	slices.SortFunc(msgs, func(a, b discord.Message) int { return cmp.Compare(a.ID, b.ID) })
	return msgs
}

// loadOlder fetches the messages before the oldest one shown.
func (c *chatView) loadOlder() {
	if c.demo {
		c.noOlder = true
		return
	}
	if c.selChannel == nil || c.loading || c.loadingOlder || c.refreshing || c.noOlder || len(c.messages) == 0 {
		return
	}
	c.loadingOlder = true
	id, before, epoch := c.selChannel.ID, c.messages[0].ID, c.historyEpoch
	go func() {
		msgs, err := c.st.MessagesBefore(id, before, 50)
		c.u.post(func() {
			if c.closed || c.historyEpoch != epoch || c.selChannel == nil || c.selChannel.ID != id {
				return
			}
			c.loadingOlder = false
			defer c.runPendingRefresh()
			if err != nil {
				slog.Error("failed to load older messages", "err", err)
				// Stop trying until the channel is opened again, instead of
				// asking again on every frame.
				c.noOlder = true
				return
			}
			if len(msgs) < 50 {
				c.noOlder = true
			}
			older := c.deleted.merge(id, sortedMessages(msgs), historyStart(msgs, !c.noOlder), before)
			c.messages = append(older, c.messages...)
			// Keep showing the same messages instead of jumping to the top.
			c.messageList.Position.First += len(older)
			c.requestAuthors(older)
		})
	}()
}

// requestAuthors asks the gateway for the members who wrote msgs, so that
// their nicknames and colors show.
func (c *chatView) requestAuthors(msgs []discord.Message) {
	if c.demo || c.selChannel == nil || !c.selChannel.GuildID.IsValid() {
		return
	}
	guildID := c.selChannel.GuildID
	seen := make(map[discord.UserID]bool)
	for _, m := range msgs {
		if seen[m.Author.ID] {
			continue
		}
		seen[m.Author.ID] = true
		if _, err := c.st.Cabinet.Member(guildID, m.Author.ID); err != nil {
			c.st.MemberState.RequestMember(guildID, m.Author.ID)
		}
	}
}

func (c *chatView) markRead(chID discord.ChannelID) {
	if c.demo {
		return
	}
	if last := c.st.LastMessage(chID); last.IsValid() {
		go c.st.ReadState.MarkRead(chID, last)
	}
}

func (c *chatView) windowFocused() {
	c.refreshCurrentChat()
	if c.u.focused && c.selChannel != nil {
		c.markRead(c.selChannel.ID)
	}
}

func (c *chatView) setTitle(title string) {
	c.u.win.Option(app.Title(title))
}

// apply updates the chat with a gateway event.
func (c *chatView) apply(ev gateway.Event) {
	if c.closed {
		return
	}
	sel := c.selChannel
	defer c.recordRefreshEvent(ev)
	switch ev := ev.(type) {
	case *gateway.MessageCreateEvent:
		if sel != nil && ev.ChannelID == sel.ID {
			// Over older messages, new ones wait for the jump to the present.
			if !c.detached && !slices.ContainsFunc(c.messages, func(m discord.Message) bool { return m.ID == ev.ID }) {
				c.messages = append(c.messages, ev.Message)
			}
			delete(c.typers, ev.Author.ID)
			if c.u.focused {
				c.markRead(sel.ID)
			}
		}
		if ev.Author.ID != c.me.ID && (sel == nil || ev.ChannelID != sel.ID || !c.u.focused) {
			if !ev.GuildID.IsValid() || c.st.MessageMentions(&ev.Message) != 0 {
				c.u.notify()
			}
		}
		if !c.selGuild.IsValid() && !ev.GuildID.IsValid() {
			// A new message moves the conversation to the top.
			c.rebuildRows()
		}
	case *gateway.MessageUpdateEvent:
		for i := range c.messages {
			if c.messages[i].ID == ev.ID {
				c.messages[i] = ev.Message
				delete(c.selectables, ev.ID)
				delete(c.richStates, ev.ID)
			}
		}
	case *gateway.MessageDeleteEvent:
		c.messageDeleted(ev.ID)
	case *gateway.MessageDeleteBulkEvent:
		for _, id := range ev.IDs {
			c.messageDeleted(id)
		}
	case *gateway.MessageReactionAddEvent:
		c.reactionEvent(ev.ChannelID, ev.MessageID, ev.Emoji, ev.UserID, true)
	case *gateway.MessageReactionRemoveEvent:
		c.reactionEvent(ev.ChannelID, ev.MessageID, ev.Emoji, ev.UserID, false)
	case *gateway.MessageReactionRemoveAllEvent:
		if sel != nil && ev.ChannelID == sel.ID {
			if i := c.messageIndex(ev.MessageID); i >= 0 {
				c.messages[i].Reactions = nil
			}
		}
	case *gateway.MessageReactionRemoveEmojiEvent:
		if sel != nil && ev.ChannelID == sel.ID {
			if i := c.messageIndex(ev.MessageID); i >= 0 {
				c.messages[i].Reactions = slices.DeleteFunc(c.messages[i].Reactions, func(r discord.Reaction) bool {
					return emojiKey(r.Emoji) == emojiKey(ev.Emoji)
				})
			}
		}
	case *gateway.TypingStartEvent:
		if sel != nil && ev.ChannelID == sel.ID && ev.UserID != c.me.ID {
			c.typers[ev.UserID] = time.Now().Add(10 * time.Second)
			time.AfterFunc(10*time.Second+100*time.Millisecond, c.u.win.Invalidate)
		}
	case *gateway.GuildCreateEvent:
		if !slices.Contains(c.guildOrder, ev.ID) {
			c.guildOrder = append([]discord.GuildID{ev.ID}, c.guildOrder...)
		}
	case *gateway.GuildDeleteEvent:
		c.guildOrder = slices.DeleteFunc(c.guildOrder, func(id discord.GuildID) bool { return id == ev.ID })
		if c.selGuild == ev.ID {
			c.selectGuild(discord.NullGuildID)
		}
	case *gateway.GuildMembersChunkEvent:
		c.rebuildRows()
		// The members found while typing @ arrive this way.
		c.rebuildMentions()
	case *gateway.ChannelCreateEvent, *gateway.ChannelUpdateEvent, *gateway.ChannelDeleteEvent,
		*gateway.VoiceStateUpdateEvent:
		c.rebuildRows()
	}
	// Read states, member names and the rest are read while laying out, so
	// the next frame shows them.
}

func (c *chatView) update(gtx layout.Context) {
	if c.homeClick.Clicked(gtx) && c.selGuild.IsValid() {
		c.selectGuild(discord.NullGuildID)
	}
	for _, id := range c.guildOrder {
		if c.click(discord.Snowflake(id)).Clicked(gtx) && id != c.selGuild {
			c.selectGuild(id)
		}
	}
	for _, r := range c.rows {
		if r.kind == rowVoiceMember {
			continue
		}
		if !c.click(discord.Snowflake(r.ch.ID)).Clicked(gtx) {
			continue
		}
		switch r.kind {
		case rowCategory:
			c.collapsed[r.ch.ID] = !c.collapsed[r.ch.ID]
			c.rebuildRows()
		case rowText, rowDM:
			c.selectChannel(r.ch)
		case rowHome:
			if r.ch.ID == requestsRowID {
				c.openHome(homeRequests)
			} else {
				c.openHome(homeFriends)
			}
		case rowVoice:
			c.joinVoice(r.ch)
		}
		break
	}
	if c.selfClick.Clicked(gtx) {
		if c.profile != nil && c.profile.self {
			c.profile = nil
		} else {
			c.openProfile(c.me, discord.NullGuildID)
		}
	}
	if c.membersToggle.Clicked(gtx) {
		c.showMembers = !c.showMembers
	}
	if ch := c.selChannel; ch != nil {
		for _, r := range c.memberRows {
			if r.member != nil && c.userClick(r.member.User.ID).Clicked(gtx) {
				c.openProfile(r.member.User, ch.GuildID)
			}
		}
		for _, m := range c.messages {
			if c.msgClick(m.ID, 0).Clicked(gtx) || c.msgClick(m.ID, 1).Clicked(gtx) {
				c.openProfile(m.Author, ch.GuildID)
			}
		}
		if c.titleClick.Clicked(gtx) && ch.Type == discord.DirectMessage && len(ch.DMRecipients) > 0 {
			c.openProfile(ch.DMRecipients[0], discord.NullGuildID)
		}
	}
	c.updateComposer(gtx)
}

func (c *chatView) layout(gtx layout.Context) layout.Dimensions {
	c.update(gtx)
	c.updateUnreadKeys(gtx)
	if c.u.chat != c {
		// Logged out while handling input.
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	channels, members, showMembers := c.paneWidths(gtx)
	serverWidth := min(gtx.Dp(72), gtx.Constraints.Max.X)
	messageX := serverWidth + channels
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pane(gtx, colorServers, serverWidth, c.layoutGuilds)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pane(gtx, colorSidebar, channels, c.layoutChannels)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			c.comp.paneX, c.comp.paneW = messageX, gtx.Constraints.Max.X
			return pane(gtx, colorChat, gtx.Constraints.Max.X, c.layoutMessages)
		}),
	}
	if showMembers {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pane(gtx, colorSidebar, members, c.layoutMembers)
		}))
	}
	dims := layout.Flex{}.Layout(gtx, children...)
	c.channelResize.layout(gtx, messageX)
	if showMembers {
		c.memberResize.layout(gtx, gtx.Constraints.Max.X-members)
	}
	c.layoutComposerPopups(gtx)
	if c.profile != nil {
		c.layoutProfile(gtx)
	}
	if c.viewer != nil {
		c.layoutViewer(gtx)
	}
	if c.video != nil && c.video.visible() {
		c.layoutVideoViewer(gtx)
	}
	if c.act.deleting != nil {
		c.layoutDeleteConfirm(gtx)
	}
	return dims
}

func (c *chatView) layoutGuilds(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	// The first element is the button for direct messages.
	return listLayout(gtx, th, &c.guildList, len(c.guildOrder)+1, func(gtx layout.Context, i int) layout.Dimensions {
		if i == 0 {
			unread := c.dmsUnread()
			dims := c.guildButton(gtx, &c.homeClick, !c.selGuild.IsValid(), unread, func(gtx layout.Context, active bool) layout.Dimensions {
				bg := colorSidebar
				if active {
					bg = colorAccent
				}
				return circleIcon(gtx, bg, iconHome, active)
			})
			// A line separates the servers from the direct messages.
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dims }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					w, h := gtx.Dp(32), gtx.Dp(2)
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: image.Pt(w, h)}.Op())
						return layout.Dimensions{Size: image.Pt(w, h)}
					})
				}),
			)
		}
		id := c.guildOrder[i-1]
		guild, err := c.st.Cabinet.Guild(id)
		if err != nil {
			return layout.Dimensions{}
		}
		unread := c.st.GuildIsUnread(id, ningen.GuildUnreadOpts{})
		return c.guildButton(gtx, c.click(discord.Snowflake(id)), id == c.selGuild, unread, func(gtx layout.Context, active bool) layout.Dimensions {
			bg := colorSidebar
			radius := -1
			if active {
				bg, radius = colorAccent, gtx.Dp(16)
			}
			return c.u.picture(gtx, cdnSize(guild.IconURLWithType(discord.PNGImage)), 48, bg, guild.Name, radius)
		})
	})
}

// guildButton lays out one entry of the server column, with the pill on its
// left that marks the selected and unread servers.
func (c *chatView) guildButton(gtx layout.Context, click *widget.Clickable, selected bool, unread ningen.UnreadIndication, w func(gtx layout.Context, active bool) layout.Dimensions) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Stack{Alignment: layout.W}.Layout(gtx,
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						dims := w(gtx, selected || click.Hovered())
						if unread == ningen.ChannelMentioned {
							// A red dot in the corner marks mentions, cut out of
							// the column by a ring of its color.
							s, ring := gtx.Dp(14), gtx.Dp(3)
							x, y := dims.Size.X-s+ring/2, dims.Size.Y-s+ring/2
							paint.FillShape(gtx.Ops, colorServers, clip.Ellipse(image.Rect(x-ring, y-ring, x+s+ring, y+s+ring)).Op(gtx.Ops))
							paint.FillShape(gtx.Ops, colorRed, clip.Ellipse(image.Rect(x, y, x+s, y+s)).Op(gtx.Ops))
						}
						return dims
					})
				})
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				var h int
				switch {
				case selected:
					h = gtx.Dp(40)
				case click.Hovered():
					h = gtx.Dp(20)
				case unread != ningen.ChannelRead:
					h = gtx.Dp(8)
				default:
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}
				y := (gtx.Constraints.Min.Y - h) / 2
				r := image.Rect(-gtx.Dp(4), y, gtx.Dp(4), y+h)
				paint.FillShape(gtx.Ops, colorHeader, clip.UniformRRect(r, gtx.Dp(4)).Op(gtx.Ops))
				// The pill fills the stack: with no size, the stack would
				// center it and move it down by half the button.
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
		)
	})
}

func (c *chatView) dmsUnread() ningen.UnreadIndication {
	// Friend requests waiting for an answer count as mentions, as in Discord.
	if c.incomingRequests() > 0 {
		return ningen.ChannelMentioned
	}
	channels, _ := c.st.PrivateChannels()
	for _, ch := range channels {
		if c.friends.requestIDs[ch.ID] {
			continue
		}
		if c.st.ChannelIsUnread(ch.ID, ningen.UnreadOpts{}) != ningen.ChannelRead {
			return ningen.ChannelMentioned
		}
	}
	return ningen.ChannelRead
}

func (c *chatView) layoutChannels(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	title := "Direct messages"
	if c.selGuild.IsValid() {
		if g, err := c.st.Cabinet.Guild(c.selGuild); err == nil {
			title = g.Name
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return header(gtx, th, title) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return listLayout(gtx, th, &c.channelList, len(c.rows), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.layoutRow(gtx, c.rows[i], i == 0)
				})
			})
		}),
		layout.Rigid(c.layoutUserPanel),
	)
}

func (c *chatView) layoutRow(gtx layout.Context, r channelRow, first bool) layout.Dimensions {
	th := c.u.th
	switch r.kind {
	case rowHome:
		return c.layoutHomeRow(gtx, r)
	case rowCategory:
		top := unit.Dp(14)
		if first {
			top = 6
		}
		return layout.Inset{Top: top}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			cl := c.click(discord.Snowflake(r.ch.ID))
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				col := colorMuted
				if cl.Hovered() {
					col = colorText
				}
				ic := iconExpand
				if c.collapsed[r.ch.ID] {
					ic = iconFold
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 12, col) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(2)}.Layout),
					layout.Flexed(1, oneLine(bold(label(th, 12, col, r.label))).Layout),
				)
			})
		})
	case rowVoiceMember:
		return layout.Inset{Left: unit.Dp(28), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			col := colorMuted
			if c.u.voiceSpeaking(r.user) {
				col = colorGreen
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, r.avatar, 22, colorFor(uint64(r.user)), r.label, -1)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Flexed(1, oneLine(label(th, 14, col, r.label)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					switch {
					case r.deaf:
						return icon(gtx, iconDeafened, 14, colorRed)
					case r.muted:
						return icon(gtx, iconMicOff, 14, colorRed)
					}
					return layout.Dimensions{}
				}),
			)
		})
	}

	selected := c.selChannel != nil && c.selChannel.ID == r.ch.ID
	unread := ningen.ChannelRead
	if r.kind != rowVoice {
		unread = c.st.ChannelIsUnread(r.ch.ID, ningen.UnreadOpts{})
	}
	inVoice := r.kind == rowVoice && c.u.voiceChannel() == r.ch.ID
	return rowItem(gtx, c.click(discord.Snowflake(r.ch.ID)), func(gtx layout.Context, hovered bool) layout.Dimensions {
		col := colorMuted
		if selected || hovered || unread != ningen.ChannelRead || inVoice {
			col = colorHeader
		}
		name := label(th, 15, col, r.label)
		if unread != ningen.ChannelRead {
			name = bold(name)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				switch r.kind {
				case rowDM:
					return c.dmAvatar(gtx, r, selected, hovered)
				case rowVoice:
					return layout.Inset{Left: unit.Dp(1), Right: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, iconVoice, 18, colorChannel)
					})
				default:
					gtx.Constraints.Min.X = gtx.Dp(20)
					l := label(th, 20, colorChannel, "#")
					l.Alignment = text.Middle
					return l.Layout(gtx)
				}
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, oneLine(name).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if unread != ningen.ChannelMentioned {
					return layout.Dimensions{}
				}
				return dot(gtx, colorRed, 8)
			}),
		)
	}, selected)
}

// dmAvatar is the picture of a conversation: the other person's avatar with
// their status, or the icon of a group.
func (c *chatView) dmAvatar(gtx layout.Context, r channelRow, selected, hovered bool) layout.Dimensions {
	ring := colorSidebar
	switch {
	case selected:
		ring = colorSelected
	case hovered:
		ring = colorHover
	}
	if r.ch.Type == discord.DirectMessage && len(r.ch.DMRecipients) > 0 {
		return c.avatarStatus(gtx, r.ch.DMRecipients[0], discord.NullGuildID, 32, ring)
	}
	url := ""
	if r.ch.Icon != "" {
		url = cdnSize(r.ch.IconURLWithType(discord.PNGImage))
	}
	return c.u.picture(gtx, url, 32, colorFor(uint64(r.ch.ID)), r.label, -1)
}

// layoutUserPanel shows who is logged in at the bottom of the channels, with
// the voice controls. Clicking the user opens their profile, whose menu has
// the logout.
func (c *chatView) layoutUserPanel(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(c.u.layoutVoicePanel),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			h := gtx.Dp(52)
			size := image.Pt(gtx.Constraints.Max.X, h)
			paint.FillShape(gtx.Ops, colorPanel, clip.Rect{Max: size}.Op())
			gtx.Constraints = layout.Exact(size)
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					// The avatar and name open the user's own profile.
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return c.selfClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							hovered := c.selfClick.Hovered()
							bg := colorPanel
							if hovered {
								bg = colorHover
							}
							return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(2), Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return c.avatarStatus(gtx, c.me, discord.NullGuildID, 32, bg)
										}),
										layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
										layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
											sub := c.me.Username
											if hovered {
												sub = statusName(c.status(discord.NullGuildID, c.me.ID))
											}
											return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
												layout.Rigid(oneLine(bold(label(th, 14, colorHeader, c.me.DisplayOrUsername()))).Layout),
												layout.Rigid(oneLine(label(th, 12, colorMuted, sub)).Layout),
											)
										}),
									)
								})
							})
						})
					}),
					// Logging out is in the menu of the user's own profile,
					// away from the voice buttons, where it is easy to hit.
					layout.Rigid(c.u.layoutVoiceButtons),
				)
			})
		}),
	)
}

// iconButton is a square button showing ic, colored hover when hovered.
func iconButton(gtx layout.Context, click *widget.Clickable, ic *widget.Icon, c, hover colorT) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := gtx.Dp(32)
		r := image.Rectangle{Max: image.Pt(size, size)}
		if click.Hovered() {
			paint.FillShape(gtx.Ops, colorHover, clip.UniformRRect(r, gtx.Dp(4)).Op(gtx.Ops))
			c = hover
		}
		gtx.Constraints = layout.Exact(r.Max)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, ic, 20, c)
		})
	})
}

// circleIcon is ic centered in a 48dp circle of color bg, squarer when active.
func circleIcon(gtx layout.Context, bg colorT, ic *widget.Icon, active bool) layout.Dimensions {
	size := gtx.Dp(48)
	radius := size / 2
	if active {
		radius = gtx.Dp(16)
	}
	r := image.Rectangle{Max: image.Pt(size, size)}
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(r, radius).Op(gtx.Ops))
	gtx.Constraints = layout.Exact(r.Max)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return icon(gtx, ic, 24, colorHeader)
	})
	return layout.Dimensions{Size: r.Max}
}
