package gui

import (
	"fmt"
	"image"
	"log/slog"
	"time"

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
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// pollVote is a vote for an answer of a poll, given or taken back, which
// Discord sends to everyone who sees the poll. Arikawa does not know these.
type pollVote struct {
	UserID    discord.UserID    `json:"user_id"`
	ChannelID discord.ChannelID `json:"channel_id"`
	MessageID discord.MessageID `json:"message_id"`
	GuildID   discord.GuildID   `json:"guild_id"`
	AnswerID  int               `json:"answer_id"`
}

type pollVoteAddEvent struct{ pollVote }
type pollVoteRemoveEvent struct{ pollVote }

func (*pollVoteAddEvent) Op() ws.OpCode              { return 0 }
func (*pollVoteAddEvent) EventType() ws.EventType    { return "MESSAGE_POLL_VOTE_ADD" }
func (*pollVoteRemoveEvent) Op() ws.OpCode           { return 0 }
func (*pollVoteRemoveEvent) EventType() ws.EventType { return "MESSAGE_POLL_VOTE_REMOVE" }

func init() {
	// Before any gateway is made, as each copies the registry.
	gateway.OpUnmarshalers.Add(
		func() ws.Event { return new(pollVoteAddEvent) },
		func() ws.Event { return new(pollVoteRemoveEvent) },
	)
}

// watchPolls keeps the votes of the polls shown as people vote.
func (c *chatView) watchPolls() func() {
	stopAdd := c.st.AddHandler(func(ev *pollVoteAddEvent) {
		c.u.post(func() { c.pollVoted(ev.pollVote, true) })
	})
	stopRemove := c.st.AddHandler(func(ev *pollVoteRemoveEvent) {
		c.u.post(func() { c.pollVoted(ev.pollVote, false) })
	})
	return func() {
		stopAdd()
		stopRemove()
	}
}

// pollVoted counts a vote given, if add, or taken back, in the poll shown.
func (c *chatView) pollVoted(v pollVote, add bool) {
	i := c.messageIndex(v.MessageID)
	if i < 0 || c.messages[i].Poll == nil {
		return
	}
	p := c.messages[i].Poll
	if p.Results == nil {
		p.Results = new(discord.PollResults)
	}
	j := -1
	for k, ac := range p.Results.AnswerCounts {
		if ac.ID == v.AnswerID {
			j = k
		}
	}
	if j < 0 {
		p.Results.AnswerCounts = append(p.Results.AnswerCounts, discord.PollAnswerCount{ID: v.AnswerID})
		j = len(p.Results.AnswerCounts) - 1
	}
	ac := &p.Results.AnswerCounts[j]
	if add {
		ac.Count++
	} else {
		ac.Count = max(ac.Count-1, 0)
	}
	if v.UserID == c.me.ID {
		ac.MeVoted = add
		if add {
			delete(c.pollChoice, v.MessageID)
		}
	}
}

// pollClick is the click of what is named key on the poll of message id.
func (c *chatView) pollClick(id discord.MessageID, key string) *widget.Clickable {
	if c.pollClicks == nil {
		c.pollClicks = make(map[string]*widget.Clickable)
	}
	return clickOf(c.pollClicks, id.String()+":"+key)
}

// pollState is what a poll shows: how many voted for each answer, whether
// the user voted, and whether voting ended.
func pollState(p *discord.Poll) (counts map[int]int, mine map[int]bool, total int, closed bool) {
	counts, mine = make(map[int]int), make(map[int]bool)
	if r := p.Results; r != nil {
		for _, ac := range r.AnswerCounts {
			counts[ac.ID] = ac.Count
			total += ac.Count
			if ac.MeVoted {
				mine[ac.ID] = true
			}
		}
		closed = r.IsFinalized
	}
	if exp := p.Expiry.Time(); !exp.IsZero() && time.Now().After(exp) {
		closed = true
	}
	return counts, mine, total, closed
}

// votePoll votes for the answers ids of the poll of m, none taking the
// vote back.
func (c *chatView) votePoll(m discord.Message, ids []int) {
	if c.demo {
		return
	}
	if ids == nil {
		ids = []int{}
	}
	path := api.EndpointChannels + m.ChannelID.String() + "/polls/" + m.ID.String() + "/answers/@me"
	go func() {
		if err := c.st.Client.FastRequest("PUT", path, httputil.WithJSONBody(map[string]any{"answer_ids": ids})); err != nil {
			slog.Warn("could not vote", "err", err)
			c.u.post(func() { c.sendErr = "Could not vote: " + err.Error() })
		}
	}()
}

// endPoll ends the poll of m, which only its author can.
func (c *chatView) endPoll(m discord.Message) {
	if c.demo {
		return
	}
	path := api.EndpointChannels + m.ChannelID.String() + "/polls/" + m.ID.String() + "/expire"
	go func() {
		if err := c.st.Client.FastRequest("POST", path); err != nil {
			c.u.post(func() { c.sendErr = "Could not end the poll: " + err.Error() })
		}
	}()
}

// updatePoll handles the clicks on the poll of m.
func (c *chatView) updatePoll(gtx layout.Context, m discord.Message) {
	p := m.Poll
	_, mine, _, closed := pollState(p)
	voted := len(mine) > 0
	if c.pollChoice == nil {
		c.pollChoice = make(map[discord.MessageID]map[int]bool)
	}
	for _, a := range p.Answers {
		if !c.pollClick(m.ID, fmt.Sprint(a.AnswerID)).Clicked(gtx) || closed || voted {
			continue
		}
		if !p.AllowMultiselect {
			c.votePoll(m, []int{a.AnswerID})
			continue
		}
		sel := c.pollChoice[m.ID]
		if sel == nil {
			sel = make(map[int]bool)
			c.pollChoice[m.ID] = sel
		}
		sel[a.AnswerID] = !sel[a.AnswerID]
	}
	if c.pollClick(m.ID, "vote").Clicked(gtx) {
		var ids []int
		for id, on := range c.pollChoice[m.ID] {
			if on {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			c.votePoll(m, ids)
		}
	}
	if c.pollClick(m.ID, "remove").Clicked(gtx) {
		c.votePoll(m, nil)
	}
	if c.pollClick(m.ID, "end").Clicked(gtx) {
		c.endPoll(m)
	}
}

// layoutPoll is the poll of m: its question, its answers to vote for, and,
// once the user voted or voting ended, how many voted for each.
func (c *chatView) layoutPoll(gtx layout.Context, m discord.Message) layout.Dimensions {
	c.updatePoll(gtx, m)
	th := c.u.th
	p := m.Poll
	counts, mine, total, closed := pollState(p)
	voted := len(mine) > 0
	results := voted || closed
	sel := c.pollChoice[m.ID]
	w := min(gtx.Constraints.Max.X, gtx.Dp(440))
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w

	question := p.Question.Text
	if e := p.Question.Emoji; e != nil && !e.ID.IsValid() && e.Name != "" {
		question = e.Name + " " + question
	}
	hint := "Select one answer"
	if p.AllowMultiselect {
		hint = "Select one or more answers"
	}
	rows := []layout.FlexChild{
		layout.Rigid(bold(label(th, 16, colorHeader, question)).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(10)}.Layout(gtx, label(th, 13, colorMuted, hint).Layout)
		}),
	}
	for _, a := range p.Answers {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutPollAnswer(gtx, m, a, counts[a.AnswerID], total, mine[a.AnswerID], sel[a.AnswerID], results, closed || voted)
			})
		}))
	}
	// Under the answers: how many voted, how long is left, and what can be
	// done.
	status := fmt.Sprintf("%d votes", total)
	if total == 1 {
		status = "1 vote"
	}
	switch exp := p.Expiry.Time(); {
	case p.Results != nil && p.Results.IsFinalized:
		status += " · Final results"
	case closed:
		status += " · Poll closed"
	case !exp.IsZero():
		status += " · " + timeLeft(time.Until(exp))
	}
	var actions []layout.FlexChild
	link := func(key, txt string, col colorT) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				cl := c.pollClick(m.ID, key)
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return label(th, 13, col, txt).Layout(gtx)
				})
			})
		})
	}
	if !closed && voted {
		actions = append(actions, link("remove", "Remove Vote", colorLink))
	}
	if !closed && m.Author.ID == c.me.ID {
		actions = append(actions, link("end", "End Poll", colorRed))
	}
	footer := []layout.FlexChild{layout.Flexed(1, label(th, 13, colorMuted, status).Layout)}
	footer = append(footer, actions...)
	if !results && p.AllowMultiselect {
		footer = append(footer, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				bg := colorSecondaryButton
				for _, on := range sel {
					if on {
						bg = colorAccent
					}
				}
				return button(gtx, th, c.pollClick(m.ID, "vote"), bg, "Vote")
			})
		}))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, footer...)
		})
	}))
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			})
		})
	})
}

// layoutPollAnswer is an answer of a poll: to click to vote, and with its
// share of the votes once they show.
func (c *chatView) layoutPollAnswer(gtx layout.Context, m discord.Message, a discord.PollAnswer, count, total int, mine, chosen, results, locked bool) layout.Dimensions {
	th := c.u.th
	cl := c.pollClick(m.ID, fmt.Sprint(a.AnswerID))
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !locked {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(10), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			text := a.PollMedia.Text
			if e := a.PollMedia.Emoji; e != nil && !e.ID.IsValid() && e.Name != "" {
				text = e.Name + "  " + text
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 15, colorText, text).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if results {
						pct := 0
						if total > 0 {
							pct = count * 100 / total
						}
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, bold(label(th, 13, colorHeader, fmt.Sprintf("%d · %d%%", count, pct))).Layout)
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return checkBox(gtx, chosen, cl.Hovered())
					})
				}),
			)
		})
		call := macro.Stop()
		size := image.Pt(gtx.Constraints.Max.X, dims.Size.Y)
		r := gtx.Dp(6)
		bg := colorInput
		if cl.Hovered() && !locked {
			bg = colorHover
		}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, r).Op(gtx.Ops))
		// The share of the votes fills the answer, the user's own in the
		// accent.
		if results && total > 0 && count > 0 {
			fill := withAlpha(colorMuted, 0x40)
			if mine {
				fill = withAlpha(colorAccent, 0x80)
			}
			st := clip.UniformRRect(image.Rectangle{Max: size}, r).Push(gtx.Ops)
			paint.FillShape(gtx.Ops, fill, clip.Rect{Max: image.Pt(size.X*count/total, size.Y)}.Op())
			st.Pop()
		}
		if mine || chosen {
			paint.FillShape(gtx.Ops, colorAccent, clip.Stroke{Path: clip.UniformRRect(image.Rectangle{Max: size}, r).Path(gtx.Ops), Width: float32(max(gtx.Dp(1), 1))}.Op())
		}
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

// timeLeft says how long is left, as Discord does.
func timeLeft(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days left", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours left", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes left", int(d.Minutes()))
	}
	return "Less than a minute left"
}
