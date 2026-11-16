package gui

import (
	"cmp"
	"errors"
	"fmt"
	"image"
	"slices"
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
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// Forwarding messages: the form that asks where to, as Discord's "Forward
// To", and the requests that send them.

// maxForwardDests is how many places Discord lets one forward go to.
const maxForwardDests = 5

// forwardable reports whether Discord lets m be forwarded: a message
// someone wrote, that Discord keeps and others see, without a poll. kept is
// that it was deleted and is only shown here, and canRead that the user may
// read the history of its channel.
func forwardable(m discord.Message, kept, canRead bool) bool {
	return m.ID.IsValid() && isRegular(m) && !isEphemeral(m) && !kept && canRead && m.Poll == nil
}

// canForward reports whether the message of the channel open can be
// forwarded.
func (c *chatView) canForward(m discord.Message) bool {
	ch := c.selChannel
	if ch == nil {
		return false
	}
	canRead := !ch.GuildID.IsValid() || c.can(ch.ID, discord.PermissionReadMessageHistory)
	return forwardable(m, c.deleted.has(m.ID), canRead)
}

// forwardData is the message that forwards src, of the server guild, as the
// web app sends it: a reference of type forward to it, and no text.
func forwardData(src discord.Message, guild discord.GuildID) api.SendMessageData {
	if src.GuildID.IsValid() {
		guild = src.GuildID
	}
	return api.SendMessageData{
		Nonce: newNonce(),
		Reference: &discord.MessageReference{
			Type:      discord.MessageReferenceTypeForward,
			MessageID: src.ID,
			ChannelID: src.ChannelID,
			GuildID:   guild,
		},
	}
}

// forwardDest is a place a message can be forwarded to.
type forwardDest struct {
	ch discord.Channel
	// name is the person, the group or the channel, and place the server,
	// with the channel of a thread; "" for direct messages.
	name, place string
	// icon is the picture of the group or the server.
	icon string
}

// dmDests are the direct messages and groups, the most recently active
// first, without those in skip, the message requests.
func dmDests(chs []discord.Channel, skip map[discord.ChannelID]bool, name func(discord.Channel) string) []forwardDest {
	chs = slices.Clone(chs)
	slices.SortStableFunc(chs, func(a, b discord.Channel) int {
		return cmp.Compare(lastActivity(b), lastActivity(a))
	})
	var out []forwardDest
	for _, ch := range chs {
		if skip[ch.ID] || (ch.Type != discord.DirectMessage && ch.Type != discord.GroupDM) {
			continue
		}
		d := forwardDest{ch: ch, name: name(ch)}
		if ch.Icon != "" {
			d.icon = cdnSize(ch.IconURLWithType(discord.PNGImage))
		}
		out = append(out, d)
	}
	return out
}

// guildDests are the channels of a server the user can send in, in the
// order of its channel list: those outside categories, then each
// category's, and under each channel its threads joined. canSend reports
// whether the user can send in a channel.
func guildDests(g discord.Guild, chs []discord.Channel, joined map[discord.ChannelID]bool, canSend func(discord.Channel) bool) []forwardDest {
	icon := ""
	if g.Icon != "" {
		icon = cdnSize(g.IconURLWithType(discord.PNGImage))
	}
	byPosition := func(a, b discord.Channel) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	}
	var top, categories []discord.Channel
	children := make(map[discord.ChannelID][]discord.Channel)
	for _, ch := range chs {
		switch {
		case ch.Type == discord.GuildCategory:
			categories = append(categories, ch)
		case isThread(ch.Type) || !isTextual(ch.Type):
		case ch.ParentID.IsValid():
			children[ch.ParentID] = append(children[ch.ParentID], ch)
		default:
			top = append(top, ch)
		}
	}
	slices.SortFunc(top, byPosition)
	slices.SortFunc(categories, byPosition)
	ordered := top
	for _, cat := range categories {
		slices.SortFunc(children[cat.ID], byPosition)
		ordered = append(ordered, children[cat.ID]...)
	}
	var out []forwardDest
	for _, ch := range ordered {
		if canSend(ch) {
			out = append(out, forwardDest{ch: ch, name: "# " + ch.Name, place: g.Name, icon: icon})
		}
		for _, th := range listedThreads(chs, ch.ID, joined, 0) {
			if canSend(th) {
				out = append(out, forwardDest{ch: th, name: th.Name, place: g.Name + " › # " + ch.Name, icon: icon})
			}
		}
	}
	return out
}

// filterDests are the places that match what was typed, in their order:
// those whose name starts with it, then those whose name has it, then those
// of a server that has it.
func filterDests(all []forwardDest, query string) []forwardDest {
	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "#")))
	if q == "" {
		return all
	}
	var ranks [3][]forwardDest
	for _, d := range all {
		name := strings.ToLower(strings.TrimPrefix(d.name, "# "))
		switch {
		case strings.HasPrefix(name, q):
			ranks[0] = append(ranks[0], d)
		case strings.Contains(name, q):
			ranks[1] = append(ranks[1], d)
		case strings.Contains(strings.ToLower(d.place), q):
			ranks[2] = append(ranks[2], d)
		}
	}
	return slices.Concat(ranks[0], ranks[1], ranks[2])
}

// forwardStep is one message sent by a forward: the message msg to the
// place dest, or the text written with them when note.
type forwardStep struct {
	msg, dest int
	note      bool
}

// forwardPlan is what a forward of msgs messages to dests places sends, in
// order: each message, the oldest first, to each place, then the text
// written with them to each. sent is how many messages each place already
// got and noted which got the text, when what failed is sent again.
func forwardPlan(msgs, dests int, note bool, sent []int, noted []bool) []forwardStep {
	var steps []forwardStep
	for m := range msgs {
		for d := range dests {
			if d < len(sent) && sent[d] > m {
				continue
			}
			steps = append(steps, forwardStep{msg: m, dest: d})
		}
	}
	if note {
		for d := range dests {
			if d < len(noted) && noted[d] {
				continue
			}
			// It follows the last message, as one more place of it.
			steps = append(steps, forwardStep{msg: max(msgs-1, 0), dest: d, note: true})
		}
	}
	return steps
}

// sendErrorText says why Discord refused a message, in a line.
func sendErrorText(err error) string {
	var herr *httputil.HTTPError
	if !errors.As(err, &herr) {
		return err.Error()
	}
	switch {
	case herr.Code == 20016:
		return tr("Slow mode: wait before sending here again")
	case herr.Status == 429:
		return tr("Discord asked to slow down; try again later")
	case herr.Code == 50007:
		return tr("This person does not accept your messages")
	case herr.Code == 50013, herr.Code == 50001, herr.Status == 403:
		return tr("You do not have permission to send messages here")
	case herr.Status == 404:
		return tr("The channel or the message is gone")
	case herr.Message != "":
		return herr.Message
	}
	return err.Error()
}

// destProgress is how far a forward got with one place.
type destProgress struct {
	// sent is how many of the messages it got, and noted whether it got the
	// text written with them.
	sent  int
	noted bool
	// sending is that a message is on its way to it, and err why one was
	// refused.
	sending bool
	err     string
}

// forwardForm is the form that asks where to forward messages.
type forwardForm struct {
	open bool
	// msgs are the messages to forward, the oldest first, of the server
	// guild; selection is that they are those selected.
	msgs      []discord.Message
	guild     discord.GuildID
	selection bool

	search, note widget.Editor
	focused      bool
	// all are the places to choose from, shown those that match query.
	all, shown []forwardDest
	query      string
	// picked are the places ticked, in the order they were.
	picked []forwardDest
	state  map[discord.ChannelID]*destProgress
	clicks map[discord.ChannelID]*widget.Clickable
	list   widget.List

	send, cancel, x, backdrop widget.Clickable
	// job is the forward under way, and err what to say under the list.
	job *bulkJob
	err string
}

// openForward opens the form to forward msgs, messages of the channel open.
func (c *chatView) openForward(msgs []discord.Message, selection bool) {
	ch := c.selChannel
	if ch == nil || len(msgs) == 0 {
		return
	}
	msgs = slices.Clone(msgs)
	slices.SortFunc(msgs, func(a, b discord.Message) int { return cmp.Compare(a.ID, b.ID) })
	for i := range msgs {
		if !msgs[i].ChannelID.IsValid() {
			msgs[i].ChannelID = ch.ID
		}
	}
	f := &c.fwd
	*f = forwardForm{
		open: true, msgs: msgs, guild: ch.GuildID, selection: selection,
		state:  make(map[discord.ChannelID]*destProgress),
		clicks: make(map[discord.ChannelID]*widget.Clickable),
	}
	f.search.SingleLine = true
	f.note.MaxLen = c.messageLimit()
	f.list.Axis = layout.Vertical
	f.all = c.forwardDests()
	f.shown = f.all
	c.comp.emoji = nil
}

// forwardDests are the places the user can forward to: the direct messages
// first, then the channels of their servers, in the order of the server
// list.
func (c *chatView) forwardDests() []forwardDest {
	out := dmDests(c.privateChannels(), c.friends.requestIDs, c.dmName)
	canSend := func(ch discord.Channel) bool {
		return c.can(ch.ID, discord.PermissionViewChannel) && c.can(ch.ID, discord.PermissionSendMessages)
	}
	for _, id := range c.guildOrder {
		g, err := c.st.Cabinet.Guild(id)
		if err != nil {
			continue
		}
		chs, _ := c.st.Cabinet.Channels(id)
		out = append(out, guildDests(*g, chs, c.threads.joined, canSend)...)
	}
	return out
}

func (c *chatView) closeForward() {
	if j := c.fwd.job; j != nil {
		j.stop()
	}
	c.fwd = forwardForm{}
}

// isPicked reports whether the place is ticked.
func (f *forwardForm) isPicked(id discord.ChannelID) bool {
	return slices.ContainsFunc(f.picked, func(d forwardDest) bool { return d.ch.ID == id })
}

// toggle ticks the place, or takes its tick away.
func (f *forwardForm) toggle(d forwardDest) {
	if f.isPicked(d.ch.ID) {
		f.picked = slices.DeleteFunc(f.picked, func(p forwardDest) bool { return p.ch.ID == d.ch.ID })
		delete(f.state, d.ch.ID)
		f.err = ""
		return
	}
	if len(f.picked) >= maxForwardDests {
		f.err = fmt.Sprintf(tr("You can forward to up to %d places at a time."), maxForwardDests)
		return
	}
	f.picked = append(f.picked, d)
	f.err = ""
}

// progress is how far the forward got with the place.
func (f *forwardForm) progress(id discord.ChannelID) *destProgress {
	p, ok := f.state[id]
	if !ok {
		p = new(destProgress)
		f.state[id] = p
	}
	return p
}

// pending reports whether something is left to send to a place ticked.
func (f *forwardForm) pending(note bool) bool {
	for _, d := range f.picked {
		p := f.progress(d.ch.ID)
		if p.sent < len(f.msgs) || note && !p.noted {
			return true
		}
	}
	return false
}

// sendForwards forwards the messages to the places ticked, one request at
// a time: what the single forward sends, for each. What a place refuses is
// said on its line, and the others still get theirs.
func (c *chatView) sendForwards() {
	f := &c.fwd
	if len(f.picked) == 0 || (f.job != nil && !f.job.finished) {
		return
	}
	note := strings.TrimSpace(f.note.Text())
	dests := slices.Clone(f.picked)
	sent, noted := make([]int, len(dests)), make([]bool, len(dests))
	for i, d := range dests {
		p := f.progress(d.ch.ID)
		p.err = ""
		sent[i], noted[i] = p.sent, p.noted
	}
	plan := forwardPlan(len(f.msgs), len(dests), note != "", sent, noted)
	if len(plan) == 0 {
		return
	}
	f.err = ""
	msgs, guild := f.msgs, f.guild
	// failed are the places that refused a message, which get no more. It
	// belongs to the run's goroutine.
	failed := make(map[discord.ChannelID]bool)
	steps := make([]bulkStep, 0, len(plan))
	for _, s := range plan {
		d, m := dests[s.dest], msgs[s.msg]
		p := f.progress(d.ch.ID)
		step := bulkStep{
			item: s.msg,
			skip: func() bool { return failed[d.ch.ID] },
			done: func(err error) {
				p.sending = false
				switch {
				case err != nil:
					p.err = sendErrorText(err)
				case s.note:
					p.noted = true
				default:
					p.sent = s.msg + 1
				}
				if err == nil && c.demo {
					c.demoForward(d.ch, m, s.note, note)
				}
			},
		}
		if !c.demo {
			step.run = func() error {
				c.u.post(func() { p.sending = true })
				data := forwardData(m, guild)
				if s.note {
					// The text goes as a message of its own, as the box sends it.
					data = api.SendMessageData{Content: note, Nonce: newNonce()}
				}
				_, err := c.st.SendMessageComplex(d.ch.ID, data)
				if err != nil {
					failed[d.ch.ID] = true
				}
				return err
			}
		}
		steps = append(steps, step)
	}
	j := &bulkJob{verb: "Forwarding"}
	f.job = j
	c.startBulk(j, paceForward, steps, func() {
		if c.fwd.job != j {
			return
		}
		c.forwardFinished(j, note != "")
	})
}

// forwardFinished closes the form when everything was sent, and otherwise
// leaves it open with what happened.
func (c *chatView) forwardFinished(j *bulkJob, note bool) {
	f := &c.fwd
	switch {
	case j.stopped == "rate limited":
		f.err = tr("Discord asked to slow down, so the rest was not sent. Send again later for what is left.")
	case j.stopped == "cancelled":
		f.err = tr("Stopped. What was sent stays sent.")
	case !f.pending(note):
		n := len(f.msgs)
		c.closeForward()
		if n == 1 {
			c.showNotice("Message forwarded")
		} else {
			c.showNotice(fmt.Sprintf(tr("%d messages forwarded"), n))
		}
		gtxFocus(c)
	}
}

// demoForward shows in the demo the message a forward makes in dest.
func (c *chatView) demoForward(dest discord.Channel, src discord.Message, isNote bool, note string) {
	now := time.Now()
	m := discord.Message{
		ID: discord.MessageID(discord.NewSnowflake(now)), ChannelID: dest.ID, GuildID: dest.GuildID,
		Author: c.me, Timestamp: discord.NewTimestamp(now),
	}
	if isNote {
		m.Content = note
	} else {
		snap := discord.MessageSnapshotMessage{
			Type: src.Type, Content: src.Content, Embeds: src.Embeds, Attachments: src.Attachments,
			Timestamp: src.Timestamp, EditedTimestamp: src.EditedTimestamp, Flags: src.Flags,
			MentionRoleIDs: src.MentionRoleIDs, Stickers: src.Stickers,
		}
		// A forward forwarded carries what the first one did.
		if inner := forwardedSnapshot(src); inner != nil {
			snap = *inner
		}
		m.Reference = forwardData(src, c.fwd.guild).Reference
		m.MessageSnapshots = []discord.MessageSnapshot{{Message: snap}}
	}
	c.st.Cabinet.MessageSet(&m, false)
	if c.selChannel != nil && c.selChannel.ID == dest.ID {
		c.messages = append(c.messages, m)
	}
}

// layoutForward draws the form over everything.
func (c *chatView) layoutForward(gtx layout.Context) {
	th := c.u.th
	f := &c.fwd
	size := gtx.Constraints.Max
	busy := f.job != nil && !f.job.finished
	stop := func() {
		// While it sends, closing stops it first; what was sent stays.
		if busy {
			f.job.stop()
			return
		}
		c.closeForward()
		gtxFocus(c)
	}
	if f.x.Clicked(gtx) || f.cancel.Clicked(gtx) || f.backdrop.Clicked(gtx) {
		stop()
	}
	for {
		e, ok := gtx.Event(
			key.Filter{Focus: &f.search, Name: key.NameEscape},
			key.Filter{Focus: &f.note, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		if ev, ok := e.(key.Event); ok && ev.State == key.Press {
			stop()
		}
	}
	if !f.open {
		return
	}
	for {
		if _, ok := f.search.Update(gtx); !ok {
			break
		}
	}
	for {
		if _, ok := f.note.Update(gtx); !ok {
			break
		}
	}
	if q := f.search.Text(); q != f.query {
		f.query = q
		f.shown = filterDests(f.all, q)
		f.list.Position = layout.Position{}
	}
	if !busy {
		for _, d := range f.shown {
			if clickOf(f.clicks, d.ch.ID).Clicked(gtx) {
				f.toggle(d)
			}
		}
		if f.send.Clicked(gtx) {
			c.sendForwards()
			busy = f.job != nil && !f.job.finished
		}
	}
	if !f.focused {
		f.focused = true
		gtx.Execute(key.FocusCmd{Tag: &f.search})
	}

	f.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(480), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(48))}
	sub := tr("Forwarding 1 message")
	if n := len(f.msgs); n > 1 {
		sub = fmt.Sprintf(tr("Forwarding %d messages, the oldest first"), n)
		if !busy {
			sub += " · " + aboutDuration(bulkEstimate(paceForward, n, max(len(f.picked)-1, 0)))
		}
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, bold(label(th, 20, colorHeader, "Forward To")).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.x.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return icon(gtx, iconClose, 24, colorMuted)
					})
				}),
			)
		}),
		layout.Rigid(label(th, 13, colorMuted, sub).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &f.search, "Search") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The list keeps its height, so that the form does not jump as
			// the search narrows it.
			h := min(gtx.Dp(300), max(gtx.Constraints.Max.Y-gtx.Dp(180), gtx.Dp(80)))
			gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = h, h
			if len(f.shown) == 0 {
				return layout.Center.Layout(gtx, label(th, 14, colorMuted, "No results").Layout)
			}
			return listLayout(gtx, th, &f.list, len(f.shown), func(gtx layout.Context, i int) layout.Dimensions {
				return c.layoutForwardDest(gtx, f.shown[i])
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
	}
	// The places ticked that the search hides still say what happened.
	for _, d := range f.picked {
		if slices.ContainsFunc(f.shown, func(s forwardDest) bool { return s.ch.ID == d.ch.ID }) {
			continue
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.layoutForwardDest(gtx, d)
		}))
	}
	rows = append(rows,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(90))
			return textField(gtx, th, &f.note, "Add an optional message…")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			txt, col := f.err, colorRed
			if busy {
				txt, col = f.job.progress(), colorMuted
				if f.job.cancelled() {
					txt = tr("Stopping…")
				}
			}
			if txt == "" {
				return layout.Spacer{Height: unit.Dp(16)}.Layout(gtx)
			}
			return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, label(th, 13, col, txt).Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			half := func(click *widget.Clickable, bg colorT, txt string) layout.FlexChild {
				return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return button(gtx, th, click, bg, txt)
				})
			}
			sendCol, sendTxt := colorAccent, "Send"
			if n := len(f.picked); n > 1 {
				sendTxt = fmt.Sprintf(tr("Send to %d"), n)
			}
			if f.job != nil && f.job.finished {
				sendTxt = "Send Again"
			}
			if len(f.picked) == 0 || busy {
				sendCol = withAlpha(colorAccent, 0x80)
			}
			cancelTxt := "Cancel"
			if busy {
				cancelTxt = "Stop"
			}
			return layout.Flex{}.Layout(gtx,
				half(&f.cancel, colorSecondaryButton, cancelTxt),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				half(&f.send, sendCol, sendTxt),
			)
		}),
	)
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, f, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// layoutForwardDest is a place of the form: its picture, its name and where
// it is, or what happened with it, and its tick.
func (c *chatView) layoutForwardDest(gtx layout.Context, d forwardDest) layout.Dimensions {
	th := c.u.th
	f := &c.fwd
	picked := f.isPicked(d.ch.ID)
	return rowItem(gtx, clickOf(f.clicks, d.ch.ID), func(gtx layout.Context, hovered bool) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if d.ch.Type == discord.DirectMessage && len(d.ch.DMRecipients) > 0 {
					u := d.ch.DMRecipients[0]
					return c.u.picture(gtx, cdnSize(u.AvatarURLWithType(discord.PNGImage)), 32, colorFor(uint64(u.ID)), d.name, -1)
				}
				radius := -1
				name := d.name
				if d.ch.GuildID.IsValid() {
					// A server's picture has the corners of the server list's.
					radius, name = gtx.Dp(10), d.place
				}
				return c.u.picture(gtx, d.icon, 32, colorFor(uint64(d.ch.ID)), name, radius)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				sub, col := d.place, colorMuted
				if p, ok := f.state[d.ch.ID]; ok && picked {
					switch {
					case p.err != "":
						sub, col = p.err, colorRed
					case p.sending:
						sub = tr("Sending…")
					case p.sent >= len(f.msgs) && p.sent > 0:
						sub, col = tr("Sent"), colorGreen
					case p.sent > 0:
						sub = fmt.Sprintf(tr("Sent %d of %d"), p.sent, len(f.msgs))
					}
				}
				lines := []layout.FlexChild{layout.Rigid(oneLine(label(th, 15, colorHeader, d.name)).Layout)}
				if sub != "" {
					l := label(th, 12, col, sub)
					l.MaxLines = 2
					lines = append(lines, layout.Rigid(l.Layout))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return checkBox(gtx, picked, false) }),
		)
	}, false)
}
