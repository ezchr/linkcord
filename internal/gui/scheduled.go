package gui

import (
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Messages can be written now and sent later, while Linkcord is open: they
// wait in scheduled.json, and those whose time passed while it was closed
// go when it opens.

var iconSchedule = mustIcon(icons.ActionSchedule)

// scheduledMsg is a message waiting to be sent at At.
type scheduledMsg struct {
	ID          int64             `json:"id"`
	ChannelID   discord.ChannelID `json:"channel_id"`
	ChannelName string            `json:"channel_name"`
	Content     string            `json:"content"`
	At          time.Time         `json:"at"`
}

// scheduleView is the window to schedule the message being written.
type scheduleView struct {
	open     bool
	focus    bool
	btn      widget.Clickable
	backdrop widget.Clickable
	presets  [6]widget.Clickable
	custom   widget.Clickable
	at       widget.Editor
	err      string
	cancels  map[int64]*widget.Clickable
	// msgs are the messages waiting, of every channel.
	msgs []scheduledMsg
}

// schedulePresets are how long after now the presets send.
var schedulePresets = []struct {
	name string
	in   time.Duration
}{
	{"In 5 minutes", 5 * time.Minute},
	{"In 15 minutes", 15 * time.Minute},
	{"In 30 minutes", 30 * time.Minute},
	{"In 1 hour", time.Hour},
	{"In 3 hours", 3 * time.Hour},
	{"Tomorrow at this time", 24 * time.Hour},
}

func scheduledPath() string { return filepath.Join(filepath.Dir(logPath()), "scheduled.json") }

func (c *chatView) saveScheduled() {
	if c.demo {
		return
	}
	data, err := json.Marshal(c.sched.msgs)
	if err != nil {
		return
	}
	go func() {
		if err := os.WriteFile(scheduledPath(), data, 0o600); err != nil {
			slog.Warn("could not keep the scheduled messages", "err", err)
		}
	}()
}

// watchScheduled loads the messages waiting and sends each when its time
// comes, until done.
func (c *chatView) watchScheduled() {
	if data, err := os.ReadFile(scheduledPath()); err == nil {
		json.Unmarshal(data, &c.sched.msgs)
	}
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			c.u.post(c.sendDue)
			select {
			case <-c.done:
				return
			case <-tick.C:
			}
		}
	}()
}

// sendDue sends the messages whose time came.
func (c *chatView) sendDue() {
	now := time.Now()
	var due []scheduledMsg
	c.sched.msgs = slices.DeleteFunc(c.sched.msgs, func(m scheduledMsg) bool {
		if m.At.After(now) {
			return false
		}
		due = append(due, m)
		return true
	})
	if len(due) == 0 {
		return
	}
	c.saveScheduled()
	for _, m := range due {
		if c.demo {
			if c.selChannel != nil && c.selChannel.ID == m.ChannelID {
				c.messages = append(c.messages, discord.Message{
					ID: discord.MessageID(discord.NewSnowflake(time.Now())), ChannelID: m.ChannelID,
					Author: c.me, Content: m.Content, Timestamp: discord.NewTimestamp(time.Now()),
				})
			}
			continue
		}
		go func() {
			_, err := c.st.SendMessageComplex(m.ChannelID, api.SendMessageData{
				Content: m.Content, Nonce: strconv.FormatInt(time.Now().UnixNano(), 10),
			})
			if err != nil {
				slog.Warn("could not send a scheduled message", "channel", m.ChannelID, "err", err)
				c.u.post(func() {
					c.u.voiceInfo(fmt.Sprintf(tr("Could not send the message scheduled for #%s: "), m.ChannelName) + err.Error())
				})
			}
		}()
	}
}

// schedule keeps the message being written to be sent at at.
func (c *chatView) schedule(at time.Time) {
	s := &c.sched
	text := strings.TrimSpace(c.composer.Text())
	if text == "" {
		s.err = tr("Write the message first.")
		return
	}
	if n, limit := len([]rune(text)), c.messageLimit(); n > limit {
		s.err = fmt.Sprintf(tr("The message is too long (%d of %d characters)."), n, limit)
		return
	}
	ch := c.selChannel
	s.msgs = append(s.msgs, scheduledMsg{
		ID: time.Now().UnixNano(), ChannelID: ch.ID, ChannelName: cmp0(ch.Name, c.dmName(*ch)),
		Content: c.withMarkup(text), At: at,
	})
	c.saveScheduled()
	c.composer.SetText("")
	c.comp.mentions = nil
	s.open, s.err = false, ""
	c.showNotice(fmt.Sprintf(tr("Scheduled for %s"), tr(formatTime(at))))
	gtxFocus(c)
}

// cmp0 is a, or b when a is empty.
func cmp0(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseClock is the next time of day hh:mm, today or tomorrow.
func parseClock(s string, now time.Time) (time.Time, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return time.Time{}, false
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(h))
	mm, err2 := strconv.Atoi(strings.TrimSpace(m))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return time.Time{}, false
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, true
}

// updateSchedule handles the clock beside the box and its window.
func (c *chatView) updateSchedule(gtx layout.Context) {
	s := &c.sched
	if s.btn.Clicked(gtx) {
		s.open, s.err, s.focus = !s.open, "", true
		s.at.SingleLine, s.at.Submit = true, true
	}
	if !s.open {
		return
	}
	if s.backdrop.Clicked(gtx) {
		s.open = false
		return
	}
	for i := range schedulePresets {
		if s.presets[i].Clicked(gtx) {
			c.schedule(time.Now().Add(schedulePresets[i].in))
			return
		}
	}
	submitted := false
	for {
		ev, ok := s.at.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submitted = true
		}
	}
	if s.custom.Clicked(gtx) || submitted {
		if at, ok := parseClock(s.at.Text(), time.Now()); ok {
			c.schedule(at)
		} else {
			s.err = tr("Write the time as hours and minutes, such as 18:30.")
		}
	}
	if s.cancels == nil {
		s.cancels = make(map[int64]*widget.Clickable)
	}
	for id, cl := range s.cancels {
		if cl.Clicked(gtx) {
			s.msgs = slices.DeleteFunc(s.msgs, func(m scheduledMsg) bool { return m.ID == id })
			c.saveScheduled()
		}
	}
}

// layoutScheduleButton is the clock beside the box, accented while
// messages wait in the channel.
func (c *chatView) layoutScheduleButton(gtx layout.Context) layout.Dimensions {
	col := colorMuted
	if c.selChannel != nil && slices.ContainsFunc(c.sched.msgs, func(m scheduledMsg) bool { return m.ChannelID == c.selChannel.ID }) {
		col = colorAccent
	}
	return iconButton(gtx, &c.sched.btn, iconSchedule, col, colorHeader)
}

// layoutSchedule is the window to schedule the message being written, with
// those waiting in the channel.
func (c *chatView) layoutSchedule(gtx layout.Context) {
	s := &c.sched
	if !s.open || c.selChannel == nil {
		return
	}
	th := c.u.th
	if s.focus {
		gtx.Execute(key.FocusCmd{Tag: &s.at})
		s.focus = false
	}
	size := gtx.Constraints.Max
	s.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x99), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(460), size.X-gtx.Dp(24))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(24))}
	text := strings.TrimSpace(c.composer.Text())
	var waiting []scheduledMsg
	for _, m := range s.msgs {
		if m.ChannelID == c.selChannel.ID {
			waiting = append(waiting, m)
		}
	}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{
				layout.Rigid(bold(label(th, 20, colorHeader, "Schedule Message")).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			}
			if text == "" {
				rows = append(rows, layout.Rigid(label(th, 14, colorMuted, "Write the message in the box, then choose when it goes.").Layout))
			} else {
				preview := text
				if r := []rune(preview); len(r) > 200 {
					preview = string(r[:200]) + "…"
				}
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(10)).Layout(gtx, label(th, 14, colorText, preview).Layout)
					})
				}))
			}
			var presets []layout.FlexChild
			for i, p := range schedulePresets {
				presets = append(presets, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6), Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &s.presets[i], colorSecondaryButton, tr(p.name))
					})
				}))
			}
			rows = append(rows,
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return wrapLayout(gtx, presets) }),
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(label(th, 14, colorText, "At ").Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return rounded(gtx, colorInput, 6, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(90)
								gtx.Constraints.Max.X = gtx.Dp(90)
								return layout.UniformInset(unit.Dp(8)).Layout(gtx, materialEditor(th, &s.at, "18:30").Layout)
							})
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, &s.custom, colorAccent, tr("Schedule"))
						}),
					)
				}),
			)
			if s.err != "" {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 13, colorRed, s.err).Layout)
				}))
			}
			if len(waiting) > 0 {
				rows = append(rows,
					layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
					layout.Rigid(bold(label(th, 12, colorText, "WAITING IN THIS CHANNEL")).Layout),
				)
				for _, m := range waiting {
					cancel := clickOf(s.cancels, m.ID)
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(label(th, 13, colorMuted, formatTime(m.At)+"  ").Layout),
								layout.Flexed(1, oneLine(label(th, 14, colorText, m.Content)).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return cancel.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										pointer.CursorPointer.Add(gtx.Ops)
										return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, label(th, 13, colorRed, "Cancel").Layout)
									})
								}),
							)
						})
					}))
				}
			}
			rows = append(rows,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(label(th, 12, colorMuted, "Messages are sent while Linkcord is open; those whose time passed while it was closed go when it opens.").Layout),
			)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	defer op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops).Pop()
	solid(gtx, s, dims)
	call.Add(gtx.Ops)
}
