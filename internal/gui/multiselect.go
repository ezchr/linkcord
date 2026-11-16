package gui

import (
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// Selecting several messages, as on WhatsApp: holding the button on a
// message, or "Select" in its menu, starts it; then a click on a message
// ticks it, and a bar in place of the box to write forwards, reacts to,
// copies or deletes those ticked. See bulkpace.go for how the requests of
// many messages are spaced.

const (
	// holdToSelect is how long the button is held on a message, without
	// moving, to start selecting.
	holdToSelect = 500 * time.Millisecond
	// holdSlop is how far the pointer may move meanwhile.
	holdSlop = unit.Dp(6)
)

// multiSelect is the messages selected in the channel open.
type multiSelect struct {
	on  bool
	ids map[discord.MessageID]bool
	// anchor is the message last ticked, where a Shift+click starts from.
	anchor discord.MessageID
	// The message the button is held on, since when and where.
	holdID  discord.MessageID
	holdAt  time.Time
	holdPos f32.Point

	forward, react, copy, del, cancel widget.Clickable
	// confirm asks whether to delete those selected.
	confirm                           bool
	confirmFocus                      bool
	confirmOK, confirmNo, confirmBack widget.Clickable
	// job is what is being done to them, and note a line under the bar.
	job  *bulkJob
	note string
}

// rangeIDs are the messages from anchor to target, both included, in the
// order they are shown in; only target when anchor is not among them.
func rangeIDs(order []discord.MessageID, anchor, target discord.MessageID) []discord.MessageID {
	a, b := slices.Index(order, anchor), slices.Index(order, target)
	if b < 0 {
		return nil
	}
	if a < 0 {
		return []discord.MessageID{target}
	}
	if a > b {
		a, b = b, a
	}
	return slices.Clone(order[a : b+1])
}

// add selects the messages, up to the most a run takes, and reports
// whether all of them fit.
func (s *multiSelect) add(ids ...discord.MessageID) bool {
	if s.ids == nil {
		s.ids = make(map[discord.MessageID]bool)
	}
	for _, id := range ids {
		if s.ids[id] {
			continue
		}
		if len(s.ids) >= bulkLimit {
			return false
		}
		s.ids[id] = true
	}
	return true
}

// toggle selects the message, or takes it out, and reports whether it
// could.
func (s *multiSelect) toggle(id discord.MessageID) bool {
	if s.ids[id] {
		delete(s.ids, id)
		return true
	}
	return s.add(id)
}

// selectableMessage reports whether a message can be selected: one someone
// wrote, that Discord keeps. kept is that it was deleted and only shows
// here.
func selectableMessage(m discord.Message, kept bool) bool {
	return m.ID.IsValid() && isRegular(m) && !isEphemeral(m) && !kept
}

// canDeleteMessage reports whether the user me may delete m: their own
// messages, and anyone's where they manage messages.
func canDeleteMessage(m discord.Message, me discord.UserID, manage bool) bool {
	return m.Author.ID == me || manage
}

// splitDeletable are those of msgs the user may delete, and how many they
// may not.
func splitDeletable(msgs []discord.Message, me discord.UserID, manage bool) (del []discord.Message, skipped int) {
	for _, m := range msgs {
		if canDeleteMessage(m, me, manage) {
			del = append(del, m)
		} else {
			skipped++
		}
	}
	return del, skipped
}

// copyLines is the text of msgs for the clipboard: a line with who wrote
// each and when, then what it says and the files it carries.
func copyLines(msgs []discord.Message, name, text func(discord.Message) string) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%s — %s", name(m), m.Timestamp.Time().Local().Format("02/01/2006 15:04"))
		if t := text(m); t != "" {
			b.WriteString("\n" + t)
		}
		for _, a := range m.Attachments {
			b.WriteString("\n" + string(a.URL))
		}
	}
	return b.String()
}

// selectedMessages are the messages selected, in the order shown.
func (c *chatView) selectedMessages() []discord.Message {
	var out []discord.Message
	for _, m := range c.messages {
		if c.msel.ids[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// enterSelection starts selecting, with m selected.
func (c *chatView) enterSelection(m discord.Message) {
	s := &c.msel
	if s.on || !selectableMessage(m, c.deleted.has(m.ID)) {
		return
	}
	*s = multiSelect{on: true, anchor: m.ID}
	s.add(m.ID)
	// What a selection starts over is put away.
	c.sel.state = nil
	c.act.editing, c.act.replyTo = 0, nil
	c.comp.emoji, c.comp.mentionOn = nil, false
	c.u.win.Invalidate()
}

// leaveSelection stops selecting, and what is being done to the messages
// after the request on its way.
func (c *chatView) leaveSelection() {
	if j := c.msel.job; j != nil {
		j.stop()
	}
	if c.msel.on {
		c.comp.emoji = nil
	}
	c.msel = multiSelect{}
}

// toggleSelected ticks a message, or takes its tick away; with Shift, it
// selects from the one last ticked to it.
func (c *chatView) toggleSelected(id discord.MessageID, shift bool) {
	s := &c.msel
	if !s.on || s.busy() {
		return
	}
	i := c.messageIndex(id)
	if i < 0 || !selectableMessage(c.messages[i], c.deleted.has(id)) {
		return
	}
	s.note = ""
	fits := true
	if shift && s.anchor.IsValid() {
		var order []discord.MessageID
		for _, m := range c.messages {
			if selectableMessage(m, c.deleted.has(m.ID)) {
				order = append(order, m.ID)
			}
		}
		fits = s.add(rangeIDs(order, s.anchor, id)...)
	} else {
		fits = s.toggle(id)
	}
	s.anchor = id
	if !fits {
		s.note = fmt.Sprintf(tr("You can select up to %d messages at a time."), bulkLimit)
	}
	if len(s.ids) == 0 {
		c.leaveSelection()
	}
}

// busy reports whether something is being done to the messages selected.
func (s *multiSelect) busy() bool {
	return s.job != nil && !s.job.finished
}

// updateSelectPointer follows the button on a message: held, it starts
// selecting; while selecting, a press ticks the message.
func (c *chatView) updateSelectPointer(gtx layout.Context, m discord.Message, tags *hoverTags) {
	s := &c.msel
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &tags.sel, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch pe.Kind {
		case pointer.Press:
			if !pe.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			if s.on {
				id, shift := m.ID, pe.Modifiers.Contain(key.ModShift)
				c.u.post(func() { c.toggleSelected(id, shift) })
			} else if c.act.editing != m.ID && selectableMessage(m, c.deleted.has(m.ID)) {
				s.holdID, s.holdAt, s.holdPos = m.ID, gtx.Now, pe.Position
			}
		case pointer.Drag:
			if d := pe.Position.Sub(s.holdPos); s.holdID == m.ID && max(abs32(d.X), abs32(d.Y)) > float32(gtx.Dp(holdSlop)) {
				s.holdID = 0
			}
		case pointer.Release, pointer.Cancel:
			if s.holdID == m.ID {
				s.holdID = 0
			}
		}
	}
	if s.on || s.holdID != m.ID {
		return
	}
	switch held := gtx.Now.Sub(s.holdAt); {
	case held > holdToSelect+time.Second:
		// A press whose end was never told, as of a message scrolled away.
		s.holdID = 0
	case held >= holdToSelect:
		s.holdID = 0
		c.u.post(func() { c.enterSelection(m) })
	default:
		gtx.Execute(op.InvalidateCmd{At: s.holdAt.Add(holdToSelect)})
	}
}

// selectArea has the area of a message of size watched for the button: it
// lets the presses through to what is under it, but while selecting, when
// links, pictures and names must not answer them.
func (c *chatView) selectArea(gtx layout.Context, tags *hoverTags, size image.Point) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	if c.msel.on {
		pointer.CursorPointer.Add(gtx.Ops)
		event.Op(gtx.Ops, &tags.sel)
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &tags.sel)
}

// selectGutter is how far the messages move right while selecting, for the
// ticks at their left.
const selectGutter = unit.Dp(28)

// paintSelectMark draws the tick of a message at its left, filled when it
// is selected.
func (c *chatView) paintSelectMark(gtx layout.Context, m discord.Message, size image.Point) {
	if !selectableMessage(m, c.deleted.has(m.ID)) {
		return
	}
	d := gtx.Dp(18)
	y := gtx.Dp(12)
	if size.Y < gtx.Dp(44) {
		y = max((size.Y-d)/2, 0)
	}
	defer op.Offset(image.Pt(gtx.Dp(12), y)).Push(gtx.Ops).Pop()
	r := image.Rectangle{Max: image.Pt(d, d)}
	if !c.msel.ids[m.ID] {
		w := max(gtx.Dp(2), 1)
		paint.FillShape(gtx.Ops, colorMuted, clip.Stroke{Path: clip.Ellipse(r.Inset(w / 2)).Path(gtx.Ops), Width: float32(w)}.Op())
		return
	}
	paint.FillShape(gtx.Ops, colorAccent, clip.Ellipse(r).Op(gtx.Ops))
	gtx.Constraints = layout.Exact(r.Max)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconCheck, 14, colorHeader) })
}

// updateMultiSelect handles the bar's buttons and Escape.
func (c *chatView) updateMultiSelect(gtx layout.Context) {
	s := &c.msel
	if !s.on {
		return
	}
	if c.selChannel == nil {
		c.leaveSelection()
		return
	}
	// Escape leaves, unless something open over the chat takes it.
	if !c.fwd.open && !s.confirm && c.comp.emoji == nil && !c.menu.open {
		for {
			ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press && !s.busy() {
				c.leaveSelection()
				gtxFocus(c)
				return
			}
		}
	}
	if s.cancel.Clicked(gtx) {
		// While something is done to them, the button stops it.
		if s.busy() {
			s.job.stop()
		} else {
			c.leaveSelection()
			gtxFocus(c)
		}
		return
	}
	if s.busy() {
		return
	}
	msgs := c.selectedMessages()
	if len(msgs) == 0 {
		// Those selected are gone.
		c.leaveSelection()
		return
	}
	switch {
	case s.forward.Clicked(gtx):
		var ok []discord.Message
		for _, m := range msgs {
			if c.canForward(m) {
				ok = append(ok, m)
			}
		}
		if len(ok) == 0 {
			s.note = tr("None of the messages selected can be forwarded.")
			break
		}
		c.openForward(ok, true)
	case s.react.Clicked(gtx):
		if !c.canReact() {
			s.note = tr("You do not have permission to add reactions in this channel.")
			break
		}
		p := newEmojiPicker()
		p.reactAll = true
		c.comp.emoji, c.comp.mentionOn = p, false
	case s.copy.Clicked(gtx):
		guild := c.selChannel.GuildID
		c.copyText = copyLines(msgs,
			func(m discord.Message) string { return c.memberName(guild, m.Author.ID, nil) },
			func(m discord.Message) string {
				if fwd := forwardedSnapshot(m); fwd != nil && m.Content == "" {
					m.Content = fwd.Content
				}
				return c.renderContent(m)
			})
		n := len(msgs)
		c.leaveSelection()
		if n == 1 {
			c.showNotice("Text copied")
		} else {
			c.showNotice(fmt.Sprintf(tr("%d messages copied"), n))
		}
	case s.del.Clicked(gtx):
		if del, _ := c.deletableSelected(); len(del) == 0 {
			s.note = tr("You cannot delete any of the messages selected.")
			break
		}
		s.confirm, s.confirmFocus = true, true
	}
}

// deletableSelected are the messages selected the user may delete, and how
// many they may not.
func (c *chatView) deletableSelected() ([]discord.Message, int) {
	ch := c.selChannel
	manage := ch != nil && ch.GuildID.IsValid() && c.can(ch.ID, discord.PermissionManageMessages)
	return splitDeletable(c.selectedMessages(), c.me.ID, manage)
}

// bulkDone ends what was done to the messages selected: it leaves the
// selection and says how it went.
func (c *chatView) bulkDone(j *bulkJob, done, failed string) {
	if c.msel.job != j {
		return
	}
	ok := max(j.at-j.failed, 0)
	c.leaveSelection()
	gtxFocus(c)
	switch {
	case j.stopped == "rate limited":
		c.sendErr = fmt.Sprintf(tr("Discord asked to slow down: stopped after %d of %d. Try the rest later."), ok, j.total)
	case j.stopped == "cancelled":
		c.showNotice(fmt.Sprintf(tr("Stopped after %d of %d"), ok, j.total))
	case j.failed > 0:
		c.sendErr = fmt.Sprintf(tr(failed), j.failed, j.total)
	default:
		c.showNotice(fmt.Sprintf(tr(done), j.total))
	}
}

// requestReact adds the user's reaction e to a message, and requestDelete
// deletes one: the requests of doing it to one message, which doing it to
// many sends for each.
func (c *chatView) requestReact(ch discord.ChannelID, id discord.MessageID, e discord.Emoji) error {
	return c.st.React(ch, id, e.APIString())
}

func (c *chatView) requestDelete(ch discord.ChannelID, id discord.MessageID) error {
	return c.st.DeleteMessage(ch, id, "")
}

// bulkReact adds the reaction e to each message selected, from the top,
// leaving out those that have the user's already.
func (c *chatView) bulkReact(e discord.Emoji) {
	s := &c.msel
	ch := c.selChannel
	if !s.on || s.busy() || ch == nil {
		return
	}
	var steps []bulkStep
	for _, m := range c.selectedMessages() {
		if slices.ContainsFunc(m.Reactions, func(r discord.Reaction) bool { return r.Me && emojiKey(r.Emoji) == emojiKey(e) }) {
			continue
		}
		chID, id := ch.ID, m.ID
		step := bulkStep{item: len(steps), done: func(err error) {
			// It shows as each is added; Discord's report of it is not
			// counted twice.
			if i := c.messageIndex(id); err == nil && i >= 0 && c.selChannel != nil && c.selChannel.ID == chID {
				applyReaction(&c.messages[i], e, true, true)
			}
		}}
		if !c.demo {
			step.run = func() error { return c.requestReact(chID, id, e) }
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		c.leaveSelection()
		c.showNotice("The messages selected already have that reaction")
		return
	}
	j := &bulkJob{verb: "Reacting to"}
	s.job = j
	c.startBulk(j, paceReact, steps, func() {
		c.bulkDone(j, "Reacted to %d messages", "%d of %d reactions could not be added")
	})
}

// bulkDelete deletes each message selected that the user may delete, from
// the top. There is no request that deletes many for a user's account, so
// each goes as deleting one does. Those deleted before it is stopped stay
// deleted.
func (c *chatView) bulkDelete() {
	s := &c.msel
	ch := c.selChannel
	if !s.on || s.busy() || ch == nil {
		return
	}
	del, _ := c.deletableSelected()
	if len(del) == 0 {
		return
	}
	chID := ch.ID
	steps := make([]bulkStep, 0, len(del))
	for i, m := range del {
		id := m.ID
		step := bulkStep{item: i, done: func(err error) {
			if err != nil || c.selChannel == nil || c.selChannel.ID != chID {
				return
			}
			c.messages = slices.DeleteFunc(c.messages, func(x discord.Message) bool { return x.ID == id })
			c.forgetMessage(id)
			delete(s.ids, id)
		}}
		if !c.demo {
			step.run = func() error {
				// The user's own delete is not kept as a deleted message.
				c.deleted.deleting(id)
				return c.requestDelete(chID, id)
			}
		}
		steps = append(steps, step)
	}
	j := &bulkJob{verb: "Deleting"}
	s.job = j
	c.startBulk(j, paceDelete, steps, func() {
		c.bulkDone(j, "%d messages deleted", "%d of %d messages could not be deleted")
	})
}

// layoutSelectionBar is the bar in place of the box to write while
// selecting: how many are selected and what to do with them, or how far
// that got.
func (c *chatView) layoutSelectionBar(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.msel
	n := len(c.selectedMessages())
	small := func(click *widget.Clickable, bg colorT, txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, click, bg, txt)
			})
		})
	}
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorInput, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if s.busy() {
							txt := s.job.progress()
							if s.job.cancelled() {
								txt = tr("Stopping…")
							} else if s.job.left > 5*time.Second {
								txt += " " + fmt.Sprintf(tr("%s left"), aboutDuration(s.job.left))
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Flexed(1, oneLine(label(th, 15, colorHeader, txt)).Layout),
								small(&s.cancel, colorSecondaryButton, "Stop"),
							)
						}
						count := fmt.Sprintf(tr("%d selected"), n)
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return iconButton(gtx, &s.cancel, iconClose, colorMuted, colorHeader)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
							layout.Flexed(1, oneLine(bold(label(th, 15, colorHeader, count))).Layout),
							small(&s.forward, colorAccent, "Forward"),
							small(&s.react, colorSecondaryButton, "React"),
							small(&s.copy, colorSecondaryButton, "Copy"),
							small(&s.del, colorRed, "Delete"),
						)
					})
				})
			}),
			// The line below says what could not be done, as under the box.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt, col := tr("Click a message to select it, Shift+click to select up to it. Esc cancels."), colorMuted
				if s.note != "" {
					txt, col = s.note, colorRed
				}
				h := gtx.Dp(24)
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
				return vcenter(gtx, oneLine(label(th, 12, col, txt)).Layout)
			}),
		)
	})
}

// layoutBulkDeleteConfirm asks once whether to delete the messages
// selected, over everything.
func (c *chatView) layoutBulkDeleteConfirm(gtx layout.Context) {
	th := c.u.th
	s := &c.msel
	if !s.on || !s.confirm {
		return
	}
	del, skipped := c.deletableSelected()
	closeIt := func() {
		s.confirm = false
		gtxFocus(c)
	}
	if s.confirmFocus {
		// With the focus on the button, Enter deletes.
		gtx.Execute(key.FocusCmd{Tag: &s.confirmOK})
		s.confirmFocus = false
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &s.confirmOK, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			closeIt()
			return
		}
	}
	if s.confirmNo.Clicked(gtx) || s.confirmBack.Clicked(gtx) || len(del) == 0 {
		closeIt()
		return
	}
	if s.confirmOK.Clicked(gtx) {
		closeIt()
		c.u.post(c.bulkDelete)
		return
	}

	size := gtx.Constraints.Max
	s.confirmBack.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x99), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	title := fmt.Sprintf(tr("Delete %d messages"), len(del))
	ask := fmt.Sprintf(tr("Are you sure you want to delete these %d messages? This cannot be undone."), len(del))
	if len(del) == 1 {
		title, ask = tr("Delete message"), tr("Are you sure you want to delete this message?")
	}
	lines := []string{ask}
	if skipped > 0 {
		lines = append(lines, fmt.Sprintf(tr("%d of the messages selected are not yours to delete and will be skipped."), skipped))
	}
	if len(del) > 1 {
		lines = append(lines, fmt.Sprintf(tr("They are deleted one at a time, at the pace of doing it by hand. It takes %s, and you can stop it midway."), aboutDuration(bulkEstimate(paceDelete, len(del), 0))))
	}
	w := min(gtx.Dp(440), size.X-gtx.Dp(24))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(24))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{layout.Rigid(bold(label(th, 20, colorHeader, title)).Layout)}
			for i, l := range lines {
				col := colorText
				if i > 0 {
					col = colorMuted
				}
				rows = append(rows,
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(label(th, 15, col, l).Layout))
			}
			rows = append(rows,
				layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, &s.confirmNo, colorChat, "Cancel")
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, &s.confirmOK, colorRed, "Delete")
						}),
					)
				}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	defer op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops).Pop()
	solid(gtx, &s.confirm, dims)
	call.Add(gtx.Ops)
}
