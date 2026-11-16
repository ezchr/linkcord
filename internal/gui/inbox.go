package gui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconInbox = mustIcon(icons.ContentInbox)

// inboxKind is why a message is in the inbox.
type inboxKind uint8

const (
	inboxNone inboxKind = iota
	// inboxMention mentions the user, a role of theirs, or @everyone or
	// @here.
	inboxMention
	// inboxReply answers a message of the user's, with or without a ping.
	inboxReply
)

// inboxMax is how many entries the inbox keeps.
const inboxMax = 100

// inboxEntry is a message in the inbox.
type inboxEntry struct {
	Message discord.Message `json:"message"`
	Kind    inboxKind       `json:"kind"`
	// Read is set once the entry was opened, or all were marked read.
	Read bool `json:"read,omitempty"`
}

// inboxList is the entries, the newest first, inboxMax at most.
type inboxList []inboxEntry

// add puts e in its place by ID. An entry already there takes the newer
// message and keeps whether it was read. It reports whether the list
// changed.
func (l *inboxList) add(e inboxEntry) bool {
	if e.Kind == inboxNone || !e.Message.ID.IsValid() {
		return false
	}
	// A reply quotes its message, which needs not quote another.
	if ref := e.Message.ReferencedMessage; ref != nil {
		r := *ref
		r.ReferencedMessage = nil
		e.Message.ReferencedMessage = &r
	}
	i, found := slices.BinarySearchFunc(*l, e.Message.ID, func(a inboxEntry, id discord.MessageID) int {
		return cmp.Compare(id, a.Message.ID)
	})
	if found {
		e.Read = e.Read || (*l)[i].Read
		(*l)[i] = e
		return true
	}
	if i >= inboxMax {
		return false
	}
	*l = slices.Insert(*l, i, e)
	if len(*l) > inboxMax {
		*l = (*l)[:inboxMax]
	}
	return true
}

// remove takes the message id out, and reports whether it was there.
func (l *inboxList) remove(id discord.MessageID) bool {
	n := len(*l)
	*l = slices.DeleteFunc(*l, func(e inboxEntry) bool { return e.Message.ID == id })
	return len(*l) != n
}

// inboxRules are what decides whether a message is for the user: who they
// are, their roles in its server, and its notification settings.
type inboxRules struct {
	me    discord.UserID
	roles []discord.RoleID
	// anyRole takes any role mentioned as one of the user's, for the
	// messages Discord already found to mention them.
	anyRole bool
	// suppressEveryone and suppressRoles are the server's settings that
	// silence @everyone and @here, and role mentions.
	suppressEveryone, suppressRoles bool
	// silent is set when the channel, or its server, notifies of nothing.
	silent bool
	// blocked is set when the author is blocked.
	blocked bool
}

// classifyInbox tells whether m goes in the inbox, and why. Messages of
// direct messages do not, as in Discord's inbox.
func classifyInbox(m discord.Message, r inboxRules) inboxKind {
	if !m.GuildID.IsValid() || m.Author.ID == r.me || r.blocked {
		return inboxNone
	}
	// A reply is one whether it pings or not; a forward or a thread's
	// first message also quote a message, but answer nothing.
	if m.Type == discord.InlinedReplyMessage && m.ReferencedMessage != nil && m.ReferencedMessage.Author.ID == r.me &&
		(m.Reference == nil || m.Reference.Type != discord.MessageReferenceTypeForward) {
		return inboxReply
	}
	for _, u := range m.Mentions {
		if u.ID == r.me {
			return inboxMention
		}
	}
	if id := r.me.String(); strings.Contains(m.Content, "<@"+id+">") || strings.Contains(m.Content, "<@!"+id+">") {
		return inboxMention
	}
	if r.silent {
		return inboxNone
	}
	if m.MentionEveryone && !r.suppressEveryone {
		return inboxMention
	}
	if len(m.MentionRoleIDs) > 0 && !r.suppressRoles {
		if r.anyRole {
			return inboxMention
		}
		for _, id := range m.MentionRoleIDs {
			if slices.Contains(r.roles, id) {
				return inboxMention
			}
		}
	}
	return inboxNone
}

// inboxFile is what inbox.json keeps: the entries of one account.
type inboxFile struct {
	User    discord.UserID `json:"user"`
	Entries inboxList      `json:"entries"`
}

func inboxPath() string {
	return filepath.Join(filepath.Dir(logPath()), "inbox.json")
}

// readInbox gives the entries kept in path for user, none for another.
func readInbox(path string, user discord.UserID) inboxList {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f inboxFile
	if json.Unmarshal(data, &f) != nil || f.User != user {
		return nil
	}
	slices.SortFunc(f.Entries, func(a, b inboxEntry) int { return cmp.Compare(b.Message.ID, a.Message.ID) })
	f.Entries = slices.CompactFunc(f.Entries, func(a, b inboxEntry) bool { return a.Message.ID == b.Message.ID })
	return f.Entries[:min(len(f.Entries), inboxMax)]
}

// writeInbox keeps the entries of user in path, through a file beside it so
// that a crash leaves the old one whole.
func writeInbox(path string, user discord.UserID, entries inboxList) error {
	data, err := json.Marshal(inboxFile{User: user, Entries: entries})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// inboxFilter is the tab of the inbox shown.
type inboxFilter int

const (
	inboxAll inboxFilter = iota
	inboxMentions
	inboxReplies
)

var inboxTabNames = [...]string{"All", "Mentions", "Replies"}

// inboxView is the inbox: the latest messages that mention or answer the
// user, under the button at the top right.
type inboxView struct {
	loaded  bool
	entries inboxList
	open    bool
	filter  inboxFilter
	// fetched is set once Discord was asked for the older mentions, and
	// loading while it answers.
	fetched, loading bool
	err              string
	// saveMu keeps the writes of inbox.json in order.
	saveMu sync.Mutex

	btn, markAll widget.Clickable
	tabs         [len(inboxTabNames)]widget.Clickable
	clicks       map[discord.MessageID]*widget.Clickable
	list         widget.List
	// dismiss takes the presses outside the popup, and popup those on it.
	dismiss, popup bool
}

// ensureInbox takes back the entries kept, once the user is known. The demo
// makes up its own.
func (c *chatView) ensureInbox() {
	b := &c.inbox
	if b.loaded || !c.me.ID.IsValid() || c.st == nil {
		return
	}
	b.loaded = true
	if c.demo {
		c.fillDemoInbox()
		return
	}
	b.entries = readInbox(inboxPath(), c.me.ID)
}

// saveInbox keeps the entries, unless in the demo.
func (c *chatView) saveInbox() {
	if c.demo {
		return
	}
	b := &c.inbox
	entries, user := slices.Clone(b.entries), c.me.ID
	go func() {
		b.saveMu.Lock()
		defer b.saveMu.Unlock()
		if err := writeInbox(inboxPath(), user, entries); err != nil {
			slog.Warn("could not keep the inbox", "err", err)
		}
	}()
}

// inboxRulesFor are the rules that decide whether m is for the user.
func (c *chatView) inboxRulesFor(m discord.Message) inboxRules {
	r := inboxRules{me: c.me.ID, blocked: c.st.UserIsBlocked(m.Author.ID)}
	if !m.GuildID.IsValid() {
		return r
	}
	gs := c.st.MutedState.GuildSettings(m.GuildID)
	r.suppressEveryone, r.suppressRoles = gs.SuppressEveryone, gs.SuppressRoles
	level := c.st.MutedState.ChannelOverrides(m.ChannelID).Notifications
	// A thread follows its channel.
	if ch, err := c.st.Cabinet.Channel(m.ChannelID); err == nil && isThread(ch.Type) && level == gateway.GuildDefaults {
		level = c.st.MutedState.ChannelOverrides(ch.ParentID).Notifications
	}
	if level == gateway.GuildDefaults {
		level = gs.Notifications
	}
	r.silent = level == gateway.NoNotifications
	if me, err := c.st.Cabinet.Member(m.GuildID, c.me.ID); err == nil {
		r.roles = me.RoleIDs
	}
	return r
}

// noteInbox puts the messages of the gateway that mention or answer the
// user in the inbox, and takes out those deleted.
func (c *chatView) noteInbox(ev gateway.Event) {
	c.ensureInbox()
	b := &c.inbox
	if !b.loaded {
		return
	}
	changed := false
	switch ev := ev.(type) {
	case *gateway.MessageCreateEvent:
		// Direct messages are not in the inbox.
		m := ev.Message
		if !m.GuildID.IsValid() {
			return
		}
		changed = b.entries.add(inboxEntry{Message: m, Kind: classifyInbox(m, c.inboxRulesFor(m))})
	case *gateway.MessageUpdateEvent:
		// An edit that adds a mention puts the message in; one already in
		// shows its new words. An update without its author is partial.
		m := ev.Message
		if !m.GuildID.IsValid() || !m.Author.ID.IsValid() {
			return
		}
		kind := classifyInbox(m, c.inboxRulesFor(m))
		if i := slices.IndexFunc(b.entries, func(e inboxEntry) bool { return e.Message.ID == m.ID }); i >= 0 {
			kind = cmp.Or(kind, b.entries[i].Kind)
		}
		changed = b.entries.add(inboxEntry{Message: m, Kind: kind})
	case *gateway.MessageDeleteEvent:
		changed = b.entries.remove(ev.ID)
	case *gateway.MessageDeleteBulkEvent:
		for _, id := range ev.IDs {
			changed = b.entries.remove(id) || changed
		}
	}
	if changed {
		c.saveInbox()
	}
}

// fetchInbox asks Discord for the recent mentions, what its inbox shows,
// once, and adds them to those seen on the gateway.
func (c *chatView) fetchInbox() {
	b := &c.inbox
	if b.fetched || c.demo {
		return
	}
	b.fetched, b.loading, b.err = true, true, ""
	go func() {
		msgs, err := c.st.Client.RequestJSON[[]discord.Message]("GET", api.EndpointMe+"/mentions?limit=25&roles=true&everyone=true")
		c.u.post(func() {
			if c.closed {
				return
			}
			b.loading = false
			if err != nil {
				// Asked again the next time the inbox opens.
				b.fetched = false
				b.err = "Discord did not give the mentions: " + err.Error()
				slog.Warn("recent mentions failed", "err", err)
				return
			}
			changed := false
			for _, m := range msgs {
				if !m.GuildID.IsValid() {
					if ch, err := c.st.Cabinet.Channel(m.ChannelID); err == nil {
						m.GuildID = ch.GuildID
					}
				}
				r := c.inboxRulesFor(m)
				// Discord found them to mention the user: a role it names is
				// one of theirs even when their member is not known here.
				r.anyRole = true
				e := inboxEntry{Message: m, Kind: classifyInbox(m, r)}
				// What was read before it came is not new.
				if rs := c.st.ReadState.ReadState(m.ChannelID); rs == nil || rs.LastMessageID >= m.ID {
					e.Read = true
				}
				changed = b.entries.add(e) || changed
				if m.GuildID.IsValid() {
					if _, err := c.st.Cabinet.Member(m.GuildID, m.Author.ID); err != nil {
						c.st.MemberState.RequestMember(m.GuildID, m.Author.ID)
					}
				}
			}
			if changed {
				c.saveInbox()
			}
		})
	}()
}

// inboxUnseen reports whether e is new to the user: not opened, not marked
// read, and after what was read of its channel.
func (c *chatView) inboxUnseen(e inboxEntry) bool {
	if e.Read {
		return false
	}
	if rs := c.st.ReadState.ReadState(e.Message.ChannelID); rs != nil && rs.LastMessageID >= e.Message.ID {
		return false
	}
	return true
}

func (c *chatView) inboxUnseenCount() int {
	n := 0
	for _, e := range c.inbox.entries {
		if c.inboxUnseen(e) {
			n++
		}
	}
	return n
}

// shown are the entries of the tab open.
func (b *inboxView) shown() []inboxEntry {
	if b.filter == inboxAll {
		return b.entries
	}
	want := inboxMention
	if b.filter == inboxReplies {
		want = inboxReply
	}
	var out []inboxEntry
	for _, e := range b.entries {
		if e.Kind == want {
			out = append(out, e)
		}
	}
	return out
}

// layoutInboxButton is the inbox's button at the right of the header, with
// the number of new entries over it.
func (c *chatView) layoutInboxButton(gtx layout.Context) layout.Dimensions {
	c.ensureInbox()
	b := &c.inbox
	if b.btn.Clicked(gtx) {
		b.open = !b.open
		if b.open {
			b.list.Position = layout.Position{}
			c.fetchInbox()
		}
	}
	col := colorMuted
	if b.open {
		col = colorHeader
	}
	n := c.inboxUnseenCount()
	return layout.Stack{Alignment: layout.NE}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, &b.btn, iconInbox, col, colorHeader)
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if n == 0 {
				return layout.Dimensions{}
			}
			return countBadge(gtx, c.u, n)
		}),
	)
}

// updateInbox handles the clicks of the inbox's tabs, entries and button
// to mark all read.
func (c *chatView) updateInbox(gtx layout.Context) {
	b := &c.inbox
	for i := range b.tabs {
		if b.tabs[i].Clicked(gtx) {
			b.filter = inboxFilter(i)
			b.list.Position = layout.Position{}
		}
	}
	if b.markAll.Clicked(gtx) {
		for i := range b.entries {
			b.entries[i].Read = true
		}
		c.saveInbox()
	}
	for id, click := range b.clicks {
		if !click.Clicked(gtx) {
			continue
		}
		for i := range b.entries {
			if b.entries[i].Message.ID == id {
				b.entries[i].Read = true
				c.saveInbox()
				b.open = false
				c.openInboxEntry(b.entries[i])
				return
			}
		}
	}
}

// openInboxEntry shows the message in its channel, in its server, as a
// search result does.
func (c *chatView) openInboxEntry(e inboxEntry) {
	m := e.Message
	if c.selChannel != nil && c.selChannel.ID == m.ChannelID {
		c.jumpToMessage(m.ID)
		return
	}
	ch, err := c.st.Cabinet.Channel(m.ChannelID)
	if err != nil {
		// A thread not joined may not be known yet.
		c.openThreadByID(m.ChannelID, m.GuildID)
		c.search.jump, c.search.jumpChan = m.ID, m.ChannelID
		return
	}
	if isThread(ch.Type) {
		c.openThread(*ch)
	} else {
		if ch.GuildID.IsValid() && ch.GuildID != c.selGuild {
			// The server opens on the channel, without loading another
			// first.
			c.lastChannel[ch.GuildID] = ch.ID
			c.selectGuild(ch.GuildID)
		}
		c.selectChannel(*ch)
	}
	c.search.jump, c.search.jumpChan = m.ID, m.ChannelID
}

// layoutInbox draws the inbox under its button, while open. A press
// anywhere else closes it.
func (c *chatView) layoutInbox(gtx layout.Context) {
	b := &c.inbox
	if !b.open || c.viewerOnly {
		return
	}
	paneX, paneW := c.comp.paneX, c.comp.paneW
	if paneW <= 0 {
		paneX, paneW = 0, gtx.Constraints.Max.X
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &b.dismiss, Kinds: pointer.Press})
		if !ok {
			break
		}
		// The button toggles the inbox itself.
		if e, ok := ev.(pointer.Event); ok && !(e.Position.Y < float32(gtx.Dp(48)) && e.Position.X >= float32(paneX+paneW-gtx.Dp(44))) {
			b.open = false
			return
		}
	}
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &b.dismiss)
	pass.Pop()
	area.Pop()
	c.updateInbox(gtx)
	if !b.open {
		return
	}
	margin := gtx.Dp(8)
	w := min(gtx.Dp(460), gtx.Constraints.Max.X-2*margin)
	h := min(gtx.Dp(620), gtx.Constraints.Max.Y-gtx.Dp(44)-margin)
	if w <= 0 || h <= 0 {
		return
	}
	x := max(paneX+paneW-w-margin, margin)
	pgtx := gtx
	pgtx.Constraints = layout.Exact(image.Pt(w, h))
	macro := op.Record(gtx.Ops)
	dims := rounded(pgtx, colorMenu, 8, c.layoutInboxPopup)
	call := macro.Stop()
	t := op.Offset(image.Pt(x, gtx.Dp(44))).Push(gtx.Ops)
	solid(gtx, &b.popup, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// layoutInboxPopup is the inside of the inbox: its title, the tabs, and
// the entries.
func (c *chatView) layoutInboxPopup(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	b := &c.inbox
	gtx.Constraints.Min = gtx.Constraints.Max
	b.list.Axis = layout.Vertical
	if b.clicks == nil {
		b.clicks = map[discord.MessageID]*widget.Clickable{}
	}
	shown := b.shown()
	head := layout.Inset{Left: unit.Dp(16), Right: unit.Dp(12), Top: unit.Dp(12), Bottom: unit.Dp(8)}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return head.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconInbox, 22, colorHeader) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Flexed(1, oneLine(bold(label(th, 18, colorHeader, "Inbox"))).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if c.inboxUnseenCount() == 0 {
							return layout.Dimensions{}
						}
						return smallButton(gtx, th, &b.markAll, colorSecondaryButton, "Mark All as Read")
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				var tabs []layout.FlexChild
				for i, name := range inboxTabNames {
					tabs = append(tabs, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						cl := &b.tabs[i]
						return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								bg, col := colorT{}, colorMuted
								switch {
								case b.filter == inboxFilter(i):
									bg, col = colorSelected, colorHeader
								case cl.Hovered():
									bg, col = colorHover, colorText
								}
								return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx,
										bold(label(th, 14, col, name)).Layout)
								})
							})
						})
					}))
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, tabs...)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, colorServers, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}.Op())
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			n := len(shown)
			extra := 0
			if n == 0 || b.loading || b.err != "" {
				extra = 1
			}
			return listLayout(gtx, th, &b.list, n+extra, func(gtx layout.Context, i int) layout.Dimensions {
				if i == n {
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						switch {
						case b.err != "":
							return label(th, 14, colorRed, b.err).Layout(gtx)
						case b.loading:
							return label(th, 14, colorMuted, "Loading…").Layout(gtx)
						}
						return label(th, 14, colorMuted, "Nothing here yet. Mentions and replies to you show here.").Layout(gtx)
					})
				}
				return c.layoutInboxEntry(gtx, shown[i])
			})
		}),
	)
}

// inboxPlace is the server and channel of a message, and the thread's
// channel before a thread.
func (c *chatView) inboxPlace(m discord.Message) string {
	var parts []string
	if g, err := c.st.Cabinet.Guild(m.GuildID); err == nil {
		parts = append(parts, g.Name)
	}
	if ch, err := c.st.Cabinet.Channel(m.ChannelID); err == nil {
		if isThread(ch.Type) {
			if p, err := c.st.Cabinet.Channel(ch.ParentID); err == nil {
				parts = append(parts, "#"+p.Name)
			}
			parts = append(parts, ch.Name)
		} else {
			parts = append(parts, "#"+ch.Name)
		}
	}
	return strings.Join(parts, " › ")
}

// layoutInboxEntry is one message of the inbox: where it was sent, who
// sent it and when, why it is there, and its text. A click shows it in its
// channel.
func (c *chatView) layoutInboxEntry(gtx layout.Context, e inboxEntry) layout.Dimensions {
	th := c.u.th
	b := &c.inbox
	m := e.Message
	click := b.clicks[m.ID]
	if click == nil {
		click = new(widget.Clickable)
		b.clicks[m.ID] = click
	}
	name := c.memberName(m.GuildID, m.Author.ID, nil)
	if name == "" {
		name = m.Author.DisplayOrUsername()
	}
	text := searchMarkup.Replace(c.renderContent(m))
	if text == "" && len(m.Attachments) > 0 {
		text = fmt.Sprintf("%d attachment%s", len(m.Attachments), plural(len(m.Attachments)))
	}
	tag, tagColor := "Mention", colorMentioned
	if e.Kind == inboxReply {
		tag, tagColor = "Reply", colorAccent
	}
	unseen := c.inboxUnseen(e)
	place := c.inboxPlace(m)
	return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorChat
			if click.Hovered() {
				bg = colorHover
			}
			return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										// New entries have a red dot before their place.
										if !unseen {
											return layout.Dimensions{}
										}
										return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return dot(gtx, colorRed, 8)
										})
									}),
									layout.Flexed(1, oneLine(bold(label(th, 12, colorMuted, place))).Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return rounded(gtx, withAlpha(tagColor, 0x40), 3, func(gtx layout.Context) layout.Dimensions {
											return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx,
												bold(label(th, 11, tagColor, tag)).Layout)
										})
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Start}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return c.u.picture(gtx, c.authorAvatarURL(m.GuildID, m.Author), 32, colorFor(uint64(m.Author.ID)), name, -1)
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
												layout.Rigid(oneLine(bold(label(th, 15, colorHeader, name))).Layout),
												layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
												layout.Rigid(oneLine(label(th, 11, colorMuted, formatTime(m.Timestamp.Time()))).Layout),
											)
										}),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											l := label(th, 14, colorText, text)
											l.MaxLines, l.Truncator = 4, "…"
											return l.Layout(gtx)
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

// fillDemoInbox makes up the demo's inbox from its messages: those that
// mention the user, and a reply to one of theirs.
func (c *chatView) fillDemoInbox() {
	b := &c.inbox
	for _, g := range c.guildOrder {
		channels, _ := c.st.Cabinet.Channels(g)
		for _, ch := range channels {
			msgs, _ := c.st.Cabinet.Messages(ch.ID)
			var mine *discord.Message
			for i := len(msgs) - 1; i >= 0; i-- {
				m := msgs[i]
				m.GuildID = g
				if m.Author.ID == c.me.ID {
					mine = &m
					continue
				}
				kind := classifyInbox(m, c.inboxRulesFor(m))
				// The first answer after a message of the user's in a thread
				// is shown as a reply to it, without a ping.
				if kind == inboxNone && mine != nil && isThread(ch.Type) {
					ref := *mine
					m.Type, m.ReferencedMessage = discord.InlinedReplyMessage, &ref
					m.Reference = &discord.MessageReference{MessageID: ref.ID, ChannelID: ch.ID, GuildID: g}
					kind, mine = inboxReply, nil
				}
				b.entries.add(inboxEntry{Message: m, Kind: kind})
			}
		}
	}
}
