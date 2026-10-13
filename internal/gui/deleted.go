package gui

import (
	"cmp"
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// maxDeleted is how many deleted messages are kept, the newest.
const maxDeleted = 5000

// deletedLog keeps the messages deleted while Linkcord watched, so that they
// stay in their place with a sign. Discord's history no longer returns them,
// so they are kept on disk and put back among the loaded messages.
type deletedLog struct {
	mu   sync.Mutex
	path string // empty: kept in memory only, as in the demo
	msgs map[discord.MessageID]deletedEntry
	// mine are the messages deleted from here, which just go.
	mine map[discord.MessageID]bool
}

type deletedEntry struct {
	Message   discord.Message `json:"message"`
	DeletedAt time.Time       `json:"deleted_at"`
}

func newDeletedLog(path string) *deletedLog {
	d := &deletedLog{path: path, msgs: make(map[discord.MessageID]deletedEntry), mine: make(map[discord.MessageID]bool)}
	if path == "" {
		return d
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	var entries []deletedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("failed to read deleted messages", "err", err)
		return d
	}
	for _, e := range entries {
		d.msgs[e.Message.ID] = e
	}
	return d
}

// deletedLogPath is %LocalAppData%\linkcord\deleted.json.
func deletedLogPath() string {
	return filepath.Join(filepath.Dir(logPath()), "deleted.json")
}

// record keeps a message that was just deleted. It may be called from any
// goroutine.
func (d *deletedLog) record(m discord.Message) {
	if d == nil || !m.ID.IsValid() {
		return
	}
	d.mu.Lock()
	if _, ok := d.msgs[m.ID]; ok || d.mine[m.ID] {
		d.mu.Unlock()
		return
	}
	d.msgs[m.ID] = deletedEntry{Message: m, DeletedAt: time.Now()}
	if len(d.msgs) > maxDeleted {
		oldest := slices.SortedFunc(func(yield func(deletedEntry) bool) {
			for _, e := range d.msgs {
				if !yield(e) {
					return
				}
			}
		}, func(a, b deletedEntry) int { return a.DeletedAt.Compare(b.DeletedAt) })
		for _, e := range oldest[:len(d.msgs)-maxDeleted] {
			delete(d.msgs, e.Message.ID)
		}
	}
	entries := make([]deletedEntry, 0, len(d.msgs))
	for _, e := range d.msgs {
		entries = append(entries, e)
	}
	d.mu.Unlock()
	if d.path != "" {
		go d.save(entries)
	}
}

var deletedSaveMu sync.Mutex

func (d *deletedLog) save(entries []deletedEntry) {
	slices.SortFunc(entries, func(a, b deletedEntry) int { return cmp.Compare(a.Message.ID, b.Message.ID) })
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	deletedSaveMu.Lock()
	defer deletedSaveMu.Unlock()
	// Written whole and then renamed, so that a crash leaves the old file.
	tmp := d.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Warn("failed to save deleted messages", "err", err)
		return
	}
	if err := os.Rename(tmp, d.path); err != nil {
		slog.Warn("failed to save deleted messages", "err", err)
	}
}

// historyStart is where loaded history begins: its oldest message, or the
// beginning of the channel when there is nothing older.
func historyStart(msgs []discord.Message, more bool) discord.MessageID {
	if !more || len(msgs) == 0 {
		return 0
	}
	return slices.MinFunc(msgs, func(a, b discord.Message) int { return cmp.Compare(a.ID, b.ID) }).ID
}

// deleting marks a message the user deletes, which is not kept.
func (d *deletedLog) deleting(id discord.MessageID) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.mine[id] = true
	d.mu.Unlock()
}

func (d *deletedLog) has(id discord.MessageID) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.msgs[id]
	return ok
}

// merge puts the deleted messages of a channel from from up to, but not
// including, to back among msgs, in order; to zero has no end. The stretch is
// what the loaded history covers, so that older ones come with older history.
func (d *deletedLog) merge(ch discord.ChannelID, msgs []discord.Message, from, to discord.MessageID) []discord.Message {
	if d == nil {
		return msgs
	}
	have := make(map[discord.MessageID]bool, len(msgs))
	for _, m := range msgs {
		have[m.ID] = true
	}
	d.mu.Lock()
	added := false
	for _, e := range d.msgs {
		m := e.Message
		if m.ChannelID == ch && m.ID >= from && (to == 0 || m.ID < to) && !have[m.ID] {
			msgs = append(msgs, m)
			added = true
		}
	}
	d.mu.Unlock()
	if added {
		msgs = sortedMessages(msgs)
	}
	return msgs
}

// messageDeleted keeps a deleted message in its place, marked, when its
// contents are known; otherwise it goes.
func (c *chatView) messageDeleted(id discord.MessageID) {
	if i := c.messageIndex(id); i >= 0 {
		c.deleted.record(c.messages[i])
	}
	c.forgetMessage(id)
	if !c.deleted.has(id) {
		c.messages = slices.DeleteFunc(c.messages, func(m discord.Message) bool { return m.ID == id })
	}
}

// layoutDeletedSign draws the sign at the end of a deleted message, which
// says so under the pointer.
func (c *chatView) layoutDeletedSign(gtx layout.Context, id discord.MessageID) layout.Dimensions {
	cl := c.msgClick(id, 9)
	return layout.Inset{Left: unit.Dp(8), Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, iconDelete, 16, colorRed)
		})
		if cl.Hovered() {
			// The tooltip goes to the left of the sign, on top of the text.
			macro := op.Record(gtx.Ops)
			tgtx := gtx
			tgtx.Constraints.Min = image.Point{}
			tip := rounded(tgtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx,
					oneLine(label(c.u.th, 13, colorHeader, "Deleted")).Layout)
			})
			call := macro.Stop()
			off := image.Pt(-tip.Size.X-gtx.Dp(4), (dims.Size.Y-tip.Size.Y)/2)
			defer op.Offset(off).Push(gtx.Ops).Pop()
			op.Defer(gtx.Ops, call)
		}
		return dims
	})
}
