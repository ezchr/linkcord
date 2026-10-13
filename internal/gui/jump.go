package gui

import (
	"log/slog"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// flashFor is how long a message jumped to stays lit.
const flashFor = 1500 * time.Millisecond

// jumpToMessage shows the message id of the open channel, lit for a moment:
// the one a reply quotes. When it is not loaded, the messages around it are
// fetched in place of the newest, with a bar to go back to them.
func (c *chatView) jumpToMessage(id discord.MessageID) {
	if c.selChannel == nil || !id.IsValid() {
		return
	}
	if i := c.messageIndex(id); i >= 0 {
		c.scrollToMessage(i)
		return
	}
	if c.demo {
		return
	}
	ch, epoch := c.selChannel.ID, c.historyEpoch
	c.loading = true
	go func() {
		msgs, err := c.st.Client.MessagesAround(ch, id, 50)
		c.u.post(func() {
			if c.closed || c.historyEpoch != epoch || c.selChannel == nil || c.selChannel.ID != ch {
				return
			}
			c.loading = false
			if err != nil {
				slog.Warn("could not load the replied message", "err", err)
				c.loadErr = "Could not load the message: " + err.Error()
				return
			}
			c.messages = c.deleted.merge(ch, sortedMessages(msgs), historyStart(msgs, true), 0)
			// Newer messages are not loaded now; a bar leads back to them.
			c.detached, c.noOlder = true, false
			clear(c.selectables)
			clear(c.richStates)
			c.requestAuthors(c.messages)
			if i := c.messageIndex(id); i >= 0 {
				c.scrollToMessage(i)
			}
		})
	}()
}

// scrollToMessage scrolls message i near the top of the list, with a little
// of what came before, and lights it.
func (c *chatView) scrollToMessage(i int) {
	// The list's first element is the top of the history.
	c.messageList.Position = layout.Position{First: max(i+1-1, 0), BeforeEnd: true}
	c.flashID, c.flashUntil = c.messages[i].ID, time.Now().Add(flashFor)
	time.AfterFunc(flashFor+50*time.Millisecond, c.u.win.Invalidate)
	c.u.win.Invalidate()
}

// flashing reports whether message id is lit after a jump.
func (c *chatView) flashing(id discord.MessageID) bool {
	return id == c.flashID && time.Now().Before(c.flashUntil)
}

// jumpToPresent loads the newest messages again after a jump.
func (c *chatView) jumpToPresent() {
	if c.selChannel == nil || !c.detached {
		return
	}
	c.detached = false
	c.messageList.Position = layout.Position{}
	ch, epoch := c.selChannel.ID, c.historyEpoch
	c.loading = true
	go func() {
		msgs, err := c.st.Client.Messages(ch, 50)
		c.u.post(func() {
			if c.closed || c.historyEpoch != epoch || c.selChannel == nil || c.selChannel.ID != ch {
				return
			}
			c.loading = false
			if err != nil {
				c.loadErr = "Could not load messages: " + err.Error()
				return
			}
			c.noOlder = len(msgs) < 50
			c.messages = c.deleted.merge(ch, sortedMessages(msgs), historyStart(msgs, !c.noOlder), 0)
			clear(c.selectables)
			clear(c.richStates)
			c.messageList.Position = layout.Position{}
			c.requestAuthors(c.messages)
		})
	}()
}

// layoutJumpBar is the bar shown over older messages, which leads back to
// the newest.
func (c *chatView) layoutJumpBar(gtx layout.Context) layout.Dimensions {
	if !c.detached {
		return layout.Dimensions{}
	}
	if c.jumpPresent.Clicked(gtx) {
		c.jumpToPresent()
	}
	return c.jumpPresent.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rounded(gtx, colorPopup, 6, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, label(c.u.th, 13, colorText, "You're viewing older messages").Layout),
					layout.Rigid(bold(label(c.u.th, 13, colorLink, "Jump to present")).Layout),
				)
			})
		})
	})
}
