package gui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// What messages said before they were edited is kept while Linkcord is
// open, as it keeps those deleted: "(edited)" opens it.

// editVersion is what a message said before an edit, and since when.
type editVersion struct {
	content string
	at      time.Time
}

// noteEdit keeps what old said, as new replaces it, if its text changed.
func (c *chatView) noteEdit(old, new discord.Message) {
	if c.u.app.NoEditHistory || old.Content == "" || new.Content == old.Content || !new.EditedTimestamp.IsValid() {
		return
	}
	at := old.Timestamp.Time()
	if old.EditedTimestamp.IsValid() {
		at = old.EditedTimestamp.Time()
	}
	if c.edits == nil {
		c.edits = make(map[discord.MessageID][]editVersion)
	}
	c.edits[old.ID] = append(c.edits[old.ID], editVersion{old.Content, at})
}

// editClickIndex is the clickable of a message's "(edited)".
const editClickIndex = 20

// layoutEditedMark is "(edited)" under a message, which opens what it said
// before when that is known.
func (c *chatView) layoutEditedMark(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	versions := c.edits[m.ID]
	if len(versions) == 0 {
		return label(th, 11, colorMuted, "(edited)").Layout(gtx)
	}
	cl := c.msgClick(m.ID, editClickIndex)
	if cl.Clicked(gtx) {
		if c.showEdits == nil {
			c.showEdits = make(map[discord.MessageID]bool)
		}
		c.showEdits[m.ID] = !c.showEdits[m.ID]
	}
	txt := fmt.Sprintf(tr("(edited · %d before, show)"), len(versions))
	if c.showEdits[m.ID] {
		txt = tr("(edited · hide what it said before)")
	}
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return label(th, 11, colorLink, txt).Layout(gtx)
	})
}

// layoutEditHistory is what a message said before, the oldest first, each
// with when it was written, above what it says now.
func (c *chatView) layoutEditHistory(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	versions := c.edits[m.ID]
	if !c.showEdits[m.ID] || len(versions) == 0 {
		return layout.Dimensions{}
	}
	var rows []layout.FlexChild
	for _, v := range versions {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(label(th, 11, colorMuted, formatTime(v.at)).Layout),
					layout.Rigid(label(th, 14, colorMuted, v.content).Layout),
				)
			})
		}))
	}
	// A bar on the left sets what it said before apart from what it says.
	return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
		paint.FillShape(gtx.Ops, colorSecondaryButton, clip.Rect{Max: image.Pt(gtx.Dp(3), dims.Size.Y)}.Op())
		return dims
	})
}
