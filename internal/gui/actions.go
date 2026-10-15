package gui

import (
	"fmt"
	"image"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// What can be done to a message: the toolbar shown while the pointer is over
// it (react, reply, edit, copy, delete), replying, editing in place,
// deleting after a confirmation, and the reactions under it.

var (
	iconEdit   = mustIcon(icons.EditorModeEdit)
	iconDelete = mustIcon(icons.ActionDelete)
)


type actionKind int

const (
	actReact actionKind = iota
	actReply
	actEdit
	actCopy
	actDelete
)

type actionKey struct {
	id   discord.MessageID
	kind actionKind
}

type reactionKey struct {
	id    discord.MessageID
	emoji string
}

// hoverTags are the pointer targets of a message and of its toolbar.
type hoverTags struct{ row, bar int }

type actionState struct {
	tags map[discord.MessageID]*hoverTags
	// hover is the message under the pointer, and barOver the message whose
	// toolbar is under it; the toolbar can stick out of its message.
	hover, barOver discord.MessageID
	buttons        map[actionKey]*widget.Clickable
	reactions      map[reactionKey]*widget.Clickable

	// replyTo is the message the next one answers, and replyPing whether its
	// author is notified.
	replyTo                  *discord.Message
	replyPing                bool
	replyClose, replyPingBtn widget.Clickable

	// editing is the message being edited in place.
	editing              discord.MessageID
	editor               widget.Editor
	editFocus            bool
	editCancel, editSave widget.Clickable

	// deleting is the message waiting for the confirmation to delete it.
	deleting                      *discord.Message
	delFocus                      bool
	delOK, delCancel, delBackdrop widget.Clickable

	// notice is a short confirmation shown under the box, such as "copied".
	notice      string
	noticeUntil time.Time
}

func newActionState() actionState {
	a := actionState{
		tags:      make(map[discord.MessageID]*hoverTags),
		buttons:   make(map[actionKey]*widget.Clickable),
		reactions: make(map[reactionKey]*widget.Clickable),
	}
	a.editor.Submit = true
	return a
}

// reset forgets everything tied to the messages of the channel shown.
func (a *actionState) reset() {
	a.hover, a.barOver, a.editing = 0, 0, 0
	a.replyTo, a.deleting = nil, nil
	clear(a.tags)
	clear(a.buttons)
	clear(a.reactions)
}

func (c *chatView) hoverTagsFor(id discord.MessageID) *hoverTags {
	t, ok := c.act.tags[id]
	if !ok {
		t = new(hoverTags)
		c.act.tags[id] = t
	}
	return t
}

func (c *chatView) actionButton(id discord.MessageID, kind actionKind) *widget.Clickable {
	k := actionKey{id, kind}
	b, ok := c.act.buttons[k]
	if !ok {
		b = new(widget.Clickable)
		c.act.buttons[k] = b
	}
	return b
}

func (c *chatView) reactionButton(id discord.MessageID, e discord.Emoji) *widget.Clickable {
	k := reactionKey{id, emojiKey(e)}
	b, ok := c.act.reactions[k]
	if !ok {
		b = new(widget.Clickable)
		c.act.reactions[k] = b
	}
	return b
}

// emojiKey identifies an emoji: custom emoji by ID, the others by
// themselves.
func emojiKey(e discord.Emoji) string {
	if e.IsCustom() {
		return e.ID.String()
	}
	return e.Name
}

func (c *chatView) canSendHere() bool {
	ch := c.selChannel
	return ch != nil && (!ch.GuildID.IsValid() || c.can(ch.ID, discord.PermissionSendMessages))
}

func (c *chatView) canReact() bool {
	ch := c.selChannel
	return ch != nil && (!ch.GuildID.IsValid() || c.can(ch.ID, discord.PermissionAddReactions))
}

func (c *chatView) canDelete(m discord.Message) bool {
	if m.Author.ID == c.me.ID {
		return true
	}
	ch := c.selChannel
	return ch != nil && ch.GuildID.IsValid() && c.can(ch.ID, discord.PermissionManageMessages)
}

func (c *chatView) messageIndex(id discord.MessageID) int {
	return slices.IndexFunc(c.messages, func(m discord.Message) bool { return m.ID == id })
}

// showNotice shows txt under the box for a moment.
func (c *chatView) showNotice(txt string) {
	c.act.notice = txt
	c.act.noticeUntil = time.Now().Add(2 * time.Second)
	time.AfterFunc(2*time.Second+100*time.Millisecond, c.u.win.Invalidate)
}

// layoutWithActions lays out a message with what goes over it: a highlight
// and the toolbar while the pointer is over it.
func (c *chatView) layoutWithActions(gtx layout.Context, m discord.Message, w layout.Widget) layout.Dimensions {
	a := &c.act
	tags := c.hoverTagsFor(m.ID)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &tags.row, Kinds: pointer.Enter | pointer.Leave | pointer.Cancel})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			if e.Kind == pointer.Enter {
				a.hover = m.ID
			} else if a.hover == m.ID {
				a.hover = 0
			}
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &tags.bar, Kinds: pointer.Enter | pointer.Leave | pointer.Cancel})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			if e.Kind == pointer.Enter {
				a.barOver = m.ID
			} else if a.barOver == m.ID {
				a.barOver = 0
			}
		}
	}
	over := a.hover == m.ID || a.barOver == m.ID
	// A deleted message is gone from Discord: nothing can be done with it.
	// Neither can one that only this user sees, which Discord never stored.
	showBar := over && a.editing != m.ID && a.deleting == nil && !isEphemeral(m)
	if !showBar && a.barOver == m.ID {
		// A toolbar that is not drawn gets no Leave; forget it.
		a.barOver = 0
	}

	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	switch {
	case isEphemeral(m):
		// Messages only this user sees have a tint and a bar on their left.
		paint.FillShape(gtx.Ops, withAlpha(colorAccent, 0x14), clip.Rect{Max: dims.Size}.Op())
		paint.FillShape(gtx.Ops, colorAccent, clip.Rect{Max: image.Pt(gtx.Dp(2), dims.Size.Y)}.Op())
	case c.mentionsMe(m):
		// So do those that mention the user, in gold, as in Discord.
		paint.FillShape(gtx.Ops, withAlpha(colorMentioned, 0x0a), clip.Rect{Max: dims.Size}.Op())
		paint.FillShape(gtx.Ops, colorMentioned, clip.Rect{Max: image.Pt(gtx.Dp(2), dims.Size.Y)}.Op())
	}
	if over || a.editing == m.ID {
		// A faint veil of the text's color, which suits every theme, as
		// Discord's barely lighter row.
		paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x0a), clip.Rect{Max: dims.Size}.Op())
	}
	if c.flashing(m.ID) {
		paint.FillShape(gtx.Ops, withAlpha(colorAccent, 0x30), clip.Rect{Max: dims.Size}.Op())
	}
	call.Add(gtx.Ops)
	// The area passes clicks on, so that links and names keep working.
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &tags.row)
	pass.Pop()
	area.Pop()
	if showBar {
		c.layoutToolbar(gtx, m, dims, tags)
	}
	return dims
}

// mentionsMe reports whether the message mentions the user, by name, by a
// role of theirs, by a reply that pings, or by @everyone or @here where
// those are not suppressed.
func (c *chatView) mentionsMe(m discord.Message) bool {
	if m.Author.ID == c.me.ID {
		return false
	}
	// Whether Discord would notify of it is no test: in a channel set to
	// notify of all messages, every message would be marked.
	for _, u := range m.Mentions {
		if u.ID == c.me.ID {
			return true
		}
	}
	if id := c.me.ID.String(); strings.Contains(m.Content, "<@"+id+">") || strings.Contains(m.Content, "<@!"+id+">") {
		return true
	}
	if c.demo || c.selChannel == nil || !c.selChannel.GuildID.IsValid() {
		return m.MentionEveryone
	}
	guildID := c.selChannel.GuildID
	settings := c.st.MutedState.GuildSettings(guildID)
	if m.MentionEveryone && !settings.SuppressEveryone {
		return true
	}
	if len(m.MentionRoleIDs) == 0 || settings.SuppressRoles {
		return false
	}
	me, err := c.st.Cabinet.Member(guildID, c.me.ID)
	if err != nil {
		return false
	}
	for _, r := range m.MentionRoleIDs {
		if slices.Contains(me.RoleIDs, r) {
			return true
		}
	}
	return false
}

// layoutToolbar draws the buttons of a message over its top right corner.
func (c *chatView) layoutToolbar(gtx layout.Context, m discord.Message, row layout.Dimensions, tags *hoverTags) {
	var kinds []actionKind
	// A message someone deleted, kept here, can be changed or taken away
	// on this screen, whoever wrote it, but not reacted or replied to.
	gone := c.deleted.has(m.ID)
	if c.canReact() && !gone {
		kinds = append(kinds, actReact)
	}
	if c.canSendHere() && !gone {
		kinds = append(kinds, actReply)
	}
	if m.Author.ID == c.me.ID || c.u.app.EditOthers || gone {
		kinds = append(kinds, actEdit)
	}
	if m.Content != "" {
		kinds = append(kinds, actCopy)
	}
	if c.canDelete(m) || gone {
		kinds = append(kinds, actDelete)
	}
	if len(kinds) == 0 {
		return
	}
	for _, k := range kinds {
		btn := c.actionButton(m.ID, k)
		for {
			click, ok := btn.Update(gtx)
			if !ok {
				break
			}
			c.runAction(gtx, m, k, click.Modifiers.Contain(key.ModShift))
		}
	}

	macro := op.Record(gtx.Ops)
	bgtx := gtx
	bgtx.Constraints.Min = image.Point{}
	dims := rounded(bgtx, colorChat, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			var children []layout.FlexChild
			for _, k := range kinds {
				ic, hover := actionIcon(k)
				btn := c.actionButton(m.ID, k)
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if k == actReact {
						return glyphButton(gtx, btn, false, emojiGlyph, colorChat)
					}
					return iconButton(gtx, btn, ic, colorMuted, hover)
				}))
			}
			return layout.Flex{}.Layout(gtx, children...)
		})
	})
	call := macro.Stop()

	x := row.Size.X - dims.Size.X - gtx.Dp(16)
	y := -gtx.Dp(16)
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	border := image.Rectangle{Min: image.Pt(-1, -1), Max: dims.Size.Add(image.Pt(1, 1))}
	paint.FillShape(gtx.Ops, colorServers, clip.UniformRRect(border, gtx.Dp(7)).Op(gtx.Ops))
	call.Add(gtx.Ops)
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &tags.bar)
	pass.Pop()
	area.Pop()
}

func actionIcon(k actionKind) (*widget.Icon, colorT) {
	switch k {
	case actReact:
		return iconEmoji, colorYellow
	case actReply:
		return iconReply, colorHeader
	case actEdit:
		return iconEdit, colorHeader
	case actCopy:
		return iconCopy, colorHeader
	default:
		return iconDelete, colorRed
	}
}

// runAction does what a toolbar button asks. shift skips the confirmation of
// a delete, as in Discord.
func (c *chatView) runAction(gtx layout.Context, m discord.Message, k actionKind, shift bool) {
	switch k {
	case actCopy:
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(c.renderContent(m)))})
		c.showNotice("Text copied")
	case actReact:
		// The picker opens above the box; the messages must not change while
		// they are laid out, so the rest waits for the next frame.
		c.u.post(func() { c.openReactionPicker(m) })
	case actReply:
		c.u.post(func() { c.startReply(m) })
	case actEdit:
		c.u.post(func() { c.startEdit(m) })
	case actDelete:
		c.u.post(func() { c.askDelete(m, shift) })
	}
}

// Replies.

func (c *chatView) startReply(m discord.Message) {
	c.act.replyTo = &m
	// Like Discord, answering someone else pings them unless turned off.
	c.act.replyPing = m.Author.ID != c.me.ID
	gtxFocus(c)
}

// updateReply handles the bar above the box while replying.
func (c *chatView) updateReply(gtx layout.Context) {
	a := &c.act
	if a.replyClose.Clicked(gtx) {
		a.replyTo = nil
	}
	if a.replyPingBtn.Clicked(gtx) {
		a.replyPing = !a.replyPing
	}
}

// layoutReplyBar says which message the next one answers, above the box.
func (c *chatView) layoutReplyBar(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	a := &c.act
	if a.replyTo == nil {
		return layout.Dimensions{}
	}
	name := c.memberName(c.selChannel.GuildID, a.replyTo.Author.ID, nil)
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Min.Y)
			paint.FillShape(gtx.Ops, colorSidebar, clip.RRect{Rect: image.Rectangle{Max: size}, NW: gtx.Dp(8), NE: gtx.Dp(8)}.Op(gtx.Ops))
			return layout.Dimensions{Size: size}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(label(th, 14, colorMuted, "Replying to ").Layout),
					layout.Flexed(1, oneLine(bold(label(th, 14, colorText, name))).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if a.replyTo.Author.ID == c.me.ID {
							return layout.Dimensions{}
						}
						txt, col := "@ ON", colorLink
						if !a.replyPing {
							txt, col = "@ OFF", colorMuted
						}
						return a.replyPingBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, bold(label(th, 12, col, txt)).Layout)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &a.replyClose, iconClose, colorMuted, colorHeader)
					}),
				)
			})
		},
	)
}

// Editing.

func (c *chatView) startEdit(m discord.Message) {
	a := &c.act
	a.editing = m.ID
	a.editor.SetText(m.Content)
	n := utf8.RuneCountInString(m.Content)
	a.editor.SetCaret(n, n)
	a.editFocus = true
	c.u.win.Invalidate()
}

// editLast edits the newest message of the user, like the up arrow in an
// empty box does in Discord.
func (c *chatView) editLast() bool {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if m := c.messages[i]; m.Author.ID == c.me.ID && isRegular(m) {
			c.startEdit(m)
			return true
		}
	}
	return false
}

func (c *chatView) cancelEdit() {
	c.act.editing = 0
	gtxFocus(c)
}

func (c *chatView) saveEdit(id discord.MessageID) {
	a := &c.act
	if a.editing != id {
		return
	}
	i := c.messageIndex(id)
	text := strings.TrimSpace(a.editor.Text())
	c.cancelEdit()
	if i < 0 {
		return
	}
	m := c.messages[i]
	// A deleted message, kept here, changes in what is kept, for good:
	// Discord has nothing to change.
	if c.deleted.has(m.ID) {
		if text == "" {
			c.askDelete(m, false)
			return
		}
		c.deleted.setContent(m.ID, text)
		c.messages[i].Content = text
		delete(c.selectables, id)
		delete(c.richStates, id)
		return
	}
	if m.Author.ID != c.me.ID {
		c.editLocally(m, text)
		return
	}
	if text == "" {
		// Like Discord, emptying a message offers to delete it.
		c.askDelete(m, false)
		return
	}
	if text == m.Content {
		return
	}
	if n, limit := utf8.RuneCountInString(text), c.messageLimit(); n > limit {
		c.sendErr = fmt.Sprintf(tr("The message is too long (%d of %d characters)."), n, limit)
		return
	}
	// Shown at once; the update from Discord confirms it.
	c.messages[i].Content = text
	c.messages[i].EditedTimestamp = discord.NewTimestamp(time.Now())
	delete(c.selectables, id)
	delete(c.richStates, id)
	if c.demo {
		return
	}
	chID := c.selChannel.ID
	go func() {
		if _, err := c.st.EditMessage(chID, id, text); err != nil {
			c.u.post(func() { c.sendErr = "Could not edit the message: " + err.Error() })
		}
	}()
}

// editLocally has others' message m say text, on this screen only; no text
// brings back what it says.
func (c *chatView) editLocally(m discord.Message, text string) {
	if c.localEdits == nil {
		c.localEdits = make(map[discord.MessageID]string)
	}
	if text == "" || text == m.Content {
		delete(c.localEdits, m.ID)
	} else {
		c.localEdits[m.ID] = text
	}
	delete(c.selectables, m.ID)
	delete(c.richStates, m.ID)
}

// layoutEditor is a message being edited: its text in a box, and how to save
// or cancel.
func (c *chatView) layoutEditor(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	a := &c.act
	if a.editFocus {
		gtx.Execute(key.FocusCmd{Tag: &a.editor})
		a.editFocus = false
	}
	// Escape is asked for before the editor reads the keys.
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &a.editor, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.u.post(c.cancelEdit)
		}
	}
	for {
		ev, ok := a.editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			c.u.post(func() { c.saveEdit(m.ID) })
		}
	}
	if a.editCancel.Clicked(gtx) {
		c.u.post(c.cancelEdit)
	}
	if a.editSave.Clicked(gtx) {
		c.u.post(func() { c.saveEdit(m.ID) })
	}
	link := func(click *widget.Clickable, txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return label(th, 12, colorLink, txt).Layout(gtx)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorInput, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(300))
						e := materialEditor(th, &a.editor, "")
						return e.Layout(gtx)
					})
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(label(th, 12, colorText, "escape to ").Layout),
				link(&a.editCancel, "cancel"),
				layout.Rigid(label(th, 12, colorText, " • enter to ").Layout),
				link(&a.editSave, "save"),
			)
		}),
	)
}

// Deleting.

func (c *chatView) askDelete(m discord.Message, skipConfirm bool) {
	if skipConfirm {
		c.deleteMessage(m)
		return
	}
	c.act.deleting = &m
	c.act.delFocus = true
	c.u.win.Invalidate()
}

// isEphemeral reports whether only this user sees m, as with some answers of
// bots. Discord keeps no such message, so it can only be dismissed here.
func isEphemeral(m discord.Message) bool {
	return m.Flags&discord.EphemeralMessage != 0
}

// dismissMessage removes a message that only this user sees.
func (c *chatView) dismissMessage(id discord.MessageID) {
	c.messages = slices.DeleteFunc(c.messages, func(m discord.Message) bool { return m.ID == id })
}

func (c *chatView) deleteMessage(m discord.Message) {
	if isEphemeral(m) {
		c.dismissMessage(m.ID)
		return
	}
	// A deleted message, kept here, goes from what is kept.
	if c.deleted.has(m.ID) {
		c.act.deleting = nil
		c.deleted.forget(m.ID)
		c.messages = slices.DeleteFunc(c.messages, func(x discord.Message) bool { return x.ID == m.ID })
		c.forgetMessage(m.ID)
		gtxFocus(c)
		return
	}
	c.act.deleting = nil
	gtxFocus(c)
	remove := func() {
		c.messages = slices.DeleteFunc(c.messages, func(x discord.Message) bool { return x.ID == m.ID })
		c.forgetMessage(m.ID)
	}
	c.deleted.deleting(m.ID)
	if c.demo {
		remove()
		return
	}
	chID := c.selChannel.ID
	go func() {
		err := c.st.DeleteMessage(chID, m.ID, "")
		c.u.post(func() {
			if err != nil {
				c.sendErr = "Could not delete the message: " + err.Error()
				return
			}
			if c.selChannel != nil && c.selChannel.ID == chID {
				remove()
			}
		})
	}()
}

// forgetMessage drops what refers to a message that is gone.
func (c *chatView) forgetMessage(id discord.MessageID) {
	a := &c.act
	if a.editing == id {
		a.editing = 0
	}
	if a.replyTo != nil && a.replyTo.ID == id {
		a.replyTo = nil
	}
	if a.deleting != nil && a.deleting.ID == id {
		a.deleting = nil
	}
	delete(a.tags, id)
}

// layoutDeleteConfirm asks whether to delete a message, over everything.
func (c *chatView) layoutDeleteConfirm(gtx layout.Context) {
	th := c.u.th
	a := &c.act
	m := a.deleting
	if m == nil {
		return
	}
	if a.delFocus {
		// With the focus on the button, Enter deletes; the box to write
		// would otherwise send its text.
		gtx.Execute(key.FocusCmd{Tag: &a.delOK})
		a.delFocus = false
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &a.delOK, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			a.deleting = nil
			gtxFocus(c)
			return
		}
	}
	if a.delCancel.Clicked(gtx) || a.delBackdrop.Clicked(gtx) {
		a.deleting = nil
		gtxFocus(c)
		return
	}
	if a.delOK.Clicked(gtx) {
		target := *m
		c.u.post(func() { c.deleteMessage(target) })
	}

	size := gtx.Constraints.Max
	a.delBackdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x99), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(440), size.X-gtx.Dp(24))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(24))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			name := c.memberName(c.selChannel.GuildID, m.Author.ID, nil)
			snippet := c.renderContent(*m)
			if r := []rune(snippet); len(r) > 300 {
				snippet = string(r[:300]) + "…"
			}
			if snippet == "" && len(m.Attachments) > 0 {
				snippet = "Attachment"
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(bold(label(th, 20, colorHeader, "Delete message")).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(label(th, 15, colorText, "Are you sure you want to delete this message?").Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(oneLine(bold(label(th, 14, colorHeader, name))).Layout),
								layout.Rigid(label(th, 14, colorText, snippet).Layout),
							)
						})
					})
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(label(th, 12, colorMuted, "Tip: Shift-click Delete to skip confirmation.").Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, &a.delCancel, colorChat, "Cancel")
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, &a.delOK, colorRed, "Delete")
						}),
					)
				}),
			)
		})
	})
	call := macro.Stop()
	defer op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops).Pop()
	solid(gtx, m, dims)
	call.Add(gtx.Ops)
}

// Reactions.

// layoutReactions shows the reactions of a message as buttons that add or
// remove the user's own.
func (c *chatView) layoutReactions(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	var chips []layout.FlexChild
	for _, r := range m.Reactions {
		btn := c.reactionButton(m.ID, r.Emoji)
		if btn.Clicked(gtx) {
			id, e := m.ID, r.Emoji
			c.u.post(func() { c.toggleReaction(id, e) })
		}
		chips = append(chips, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(4), Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					bg, border, count := colorSidebar, colorSidebar, colorMuted
					if btn.Hovered() {
						border = colorSelected
					}
					if r.Me {
						bg, border, count = colorMentionB, colorAccent, colorMention
					}
					macro := op.Record(gtx.Ops)
					dims := layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutEmoji(gtx, r.Emoji, 18) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(bold(label(th, 14, count, fmt.Sprint(r.Count))).Layout),
						)
					})
					call := macro.Stop()
					radius := gtx.Dp(8)
					rect := image.Rectangle{Max: dims.Size}
					paint.FillShape(gtx.Ops, border, clip.UniformRRect(rect, radius).Op(gtx.Ops))
					inner := image.Rectangle{Min: image.Pt(1, 1), Max: dims.Size.Sub(image.Pt(1, 1))}
					paint.FillShape(gtx.Ops, bg, clip.UniformRRect(inner, radius-1).Op(gtx.Ops))
					call.Add(gtx.Ops)
					return dims
				})
			})
		}))
	}
	return wrapLayout(gtx, chips)
}

// layoutEmoji draws an emoji of a reaction: the picture of a custom emoji,
// or the character.
func (c *chatView) layoutEmoji(gtx layout.Context, e discord.Emoji, size unit.Dp) layout.Dimensions {
	if e.IsCustom() {
		url := "https://cdn.discordapp.com/emojis/" + e.ID.String() + ".png?size=48"
		return c.u.picture(gtx, url, size, colorSidebar, ":"+e.Name+":", 0)
	}
	if u := twemojiURL(e.Name, int(size)); isEmoji(e.Name) && !c.u.images.failed(u, gtx.Dp(size)) {
		return c.twemoji(gtx, u, size)
	}
	return label(c.u.th, unit.Sp(float32(size)*0.85), colorHeader, e.Name).Layout(gtx)
}

// toggleReaction adds the user's reaction with emoji e to a message, or
// takes it away if it is there. It shows at once; Discord confirms it.
func (c *chatView) toggleReaction(id discord.MessageID, e discord.Emoji) {
	i := c.messageIndex(id)
	if i < 0 || c.selChannel == nil {
		return
	}
	m := &c.messages[i]
	j := slices.IndexFunc(m.Reactions, func(r discord.Reaction) bool { return emojiKey(r.Emoji) == emojiKey(e) })
	add := j < 0 || !m.Reactions[j].Me
	if add && j < 0 && !c.canReact() {
		c.sendErr = "You do not have permission to add reactions in this channel."
		return
	}
	applyReaction(m, e, add, true)
	if c.demo {
		return
	}
	chID := c.selChannel.ID
	go func() {
		var err error
		if add {
			err = c.st.React(chID, id, e.APIString())
		} else {
			err = c.st.Unreact(chID, id, e.APIString())
		}
		if err != nil {
			c.u.post(func() { c.sendErr = "Could not update the reaction: " + err.Error() })
		}
	}()
}

// applyReaction counts one reaction with e more (add) or less on m. me says
// whether it is the user's own. The user's own changes are applied when made
// and again when Discord reports them, so those that are already there are
// skipped.
func applyReaction(m *discord.Message, e discord.Emoji, add, me bool) {
	j := slices.IndexFunc(m.Reactions, func(r discord.Reaction) bool { return emojiKey(r.Emoji) == emojiKey(e) })
	if add {
		switch {
		case j < 0:
			m.Reactions = append(m.Reactions, discord.Reaction{Count: 1, Me: me, Emoji: e})
		case me && m.Reactions[j].Me:
		default:
			m.Reactions[j].Count++
			if me {
				m.Reactions[j].Me = true
			}
		}
		return
	}
	if j < 0 || (me && !m.Reactions[j].Me) {
		return
	}
	m.Reactions[j].Count--
	if me {
		m.Reactions[j].Me = false
	}
	if m.Reactions[j].Count <= 0 {
		m.Reactions = slices.Delete(m.Reactions, j, j+1)
	}
}

// reactionEvent applies a reaction someone added or removed.
func (c *chatView) reactionEvent(chID discord.ChannelID, id discord.MessageID, e discord.Emoji, user discord.UserID, add bool) {
	if c.selChannel == nil || c.selChannel.ID != chID {
		return
	}
	if i := c.messageIndex(id); i >= 0 {
		applyReaction(&c.messages[i], e, add, user == c.me.ID)
	}
}

func (c *chatView) openReactionPicker(m discord.Message) {
	p := newEmojiPicker()
	p.react = m.ID
	c.comp.emoji = p
	c.comp.mentionOn = false
}

var reCustomEmojiMarkup = regexp.MustCompile(`^<(a?):([^:>]+):(\d+)>`)

// emojiOfCell is the emoji of a cell of the picker.
func emojiOfCell(cl *emojiCell) discord.Emoji {
	if mm := reCustomEmojiMarkup.FindStringSubmatch(cl.text); mm != nil {
		id, _ := discord.ParseSnowflake(mm[3])
		return discord.Emoji{ID: discord.EmojiID(id), Name: mm[2], Animated: mm[1] == "a"}
	}
	return discord.Emoji{Name: strings.TrimSpace(cl.text)}
}

// materialEditor is an editor styled like the box to write.
func materialEditor(th *material.Theme, ed *widget.Editor, hint string) material.EditorStyle {
	e := material.Editor(th, ed, tr(hint))
	e.HintColor = colorMuted
	e.Color = colorText
	e.TextSize = 15
	e.SelectionColor = withAlpha(colorAccent, 0x60)
	return e
}
