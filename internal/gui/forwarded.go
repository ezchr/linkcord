package gui

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconForward = mustIcon(icons.ContentForward)

// forwardedSnapshot is the message a forward carries, if m is one.
func forwardedSnapshot(m discord.Message) *discord.MessageSnapshotMessage {
	if m.Reference == nil || m.Reference.Type != discord.MessageReferenceTypeForward || len(m.MessageSnapshots) == 0 {
		return nil
	}
	return &m.MessageSnapshots[0].Message
}

// layoutForwarded draws a forwarded message as Discord does: a bar on the
// left, "Forwarded", the message with its files and embeds, and when it was
// first sent.
func (c *chatView) layoutForwarded(gtx layout.Context, m discord.Message, fwd discord.MessageSnapshotMessage) layout.Dimensions {
	th := c.u.th
	// The forward has no text of its own, so the snapshot's text takes the
	// message's place for selection and links.
	inner := discord.Message{
		ID: m.ID, ChannelID: m.ChannelID, GuildID: m.GuildID,
		Content: fwd.Content, Embeds: fwd.Embeds, Attachments: fwd.Attachments,
		Timestamp: fwd.Timestamp, EditedTimestamp: fwd.EditedTimestamp,
		Mentions: fwd.Mentions, MentionRoleIDs: fwd.MentionRoleIDs, Flags: fwd.Flags,
	}
	for _, s := range fwd.Stickers {
		inner.Stickers = append(inner.Stickers, s)
	}

	var children []layout.FlexChild
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconForward, 16, colorMuted) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(italic(label(th, 13, colorMuted, "Forwarded")).Layout),
		)
	}))
	if fwd.Content != "" {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.layoutContent(gtx, inner)
		}))
	}
	children = append(children, c.mediaChildren(inner)...)
	if fwd.Timestamp.IsValid() {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx,
				label(th, 12, colorMuted, formatTime(fwd.Timestamp.Time())).Layout)
		}))
	}

	return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bar := gtx.Dp(4)
		gap := gtx.Dp(12)
		macro := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		cgtx.Constraints.Max.X = max(gtx.Constraints.Max.X-bar-gap, 0)
		t := op.Offset(image.Pt(bar+gap, 0)).Push(gtx.Ops)
		dims := layout.Flex{Axis: layout.Vertical}.Layout(cgtx, children...)
		t.Pop()
		call := macro.Stop()
		paint.FillShape(gtx.Ops, colorMuted, clip.UniformRRect(image.Rect(0, 0, bar, dims.Size.Y), bar/2).Op(gtx.Ops))
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: image.Pt(dims.Size.X+bar+gap, dims.Size.Y)}
	})
}
